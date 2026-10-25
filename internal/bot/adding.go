package bot

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/EugenSleptsov/hoarder/internal/dialog"
	"github.com/EugenSleptsov/hoarder/internal/sqlstore"
)

// Names are typing shortcuts only: no category rates or shared model parameters.
var commonNames = []string{"Зубная паста", "Туалетная бумага", "Шампунь", "Мыло", "Таблетки для посудомойки", "Мусорные пакеты"}

type addFlow struct {
	ScreenID string
	Names    []string
	Index    int
}

// inputKey names a runtime record of one chat's pending text input. The
// owner's keys keep their original names.
func inputKey(name string, chat int64) string {
	if chat == 0 {
		return name
	}
	return name + ":" + strconv.FormatInt(chat, 10)
}

func loadAdding(ctx context.Context, tx *sqlstore.Tx, chat int64) (addFlow, error) {
	var f addFlow
	err := tx.Get(ctx, "runtime", inputKey("adding", chat), &f)
	if errors.Is(err, sqlstore.ErrNotFound) {
		err = nil
	}
	return f, err
}

// validName applies to every typed name: new items and renames.
func validName(name string) error {
	if !utf8.ValidString(name) || utf8.RuneCountInString(name) > 80 || strings.IndexFunc(name, unicode.IsControl) >= 0 || strings.HasPrefix(name, "/") {
		return errors.New("Название: до 80 символов, без команд и управляющих символов.")
	}
	return nil
}

func parseNames(text string) ([]string, error) {
	if len(text) > 10000 {
		return nil, errors.New("Список слишком длинный: до 20 названий, по одному на строку.")
	}
	names := []string{}
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		name := strings.TrimSpace(line)
		if name == "" {
			continue
		}
		if err := validName(name); err != nil {
			return nil, err
		}
		found := false
		for _, old := range names {
			if strings.EqualFold(old, name) {
				found = true
				break
			}
		}
		if !found {
			names = append(names, name)
		}
		if len(names) > 20 {
			return nil, errors.New("За один раз можно добавить до 20 предметов.")
		}
	}
	if len(names) == 0 {
		return nil, errors.New("Введите хотя бы одно название.")
	}
	return names, nil
}

// suggestions leaves out names already in the registry or archive and the ones
// the owner hid. Hiding a shortcut never blocks typing the same name.
func (s *Service) suggestions(ctx context.Context, tx *sqlstore.Tx) (visible, hidden []string, err error) {
	var stored []string
	if err = tx.Get(ctx, "runtime", "hidden_names", &stored); err != nil && !errors.Is(err, sqlstore.ErrNotFound) {
		return nil, nil, err
	}
	states, err := tx.Items(ctx)
	if err != nil {
		return nil, nil, err
	}
	for _, name := range commonNames {
		exists := false
		for _, state := range states {
			if strings.EqualFold(state.Config.Name, name) {
				exists = true
				break
			}
		}
		switch {
		case exists:
		case slices.Contains(stored, name):
			hidden = append(hidden, name)
		default:
			visible = append(visible, name)
		}
	}
	return visible, hidden, nil
}

func (s *Service) beginAdding(ctx context.Context, tx *sqlstore.Tx, at place, now time.Time) error {
	// The name prompt is the newer text input; a pending rename ends here.
	if err := s.dropRename(ctx, tx, at.Chat, now); err != nil {
		return err
	}
	f, err := loadAdding(ctx, tx, at.Chat)
	if err != nil {
		return err
	}
	if len(f.Names) > f.Index {
		return s.resumeAdding(ctx, tx, f, at, now)
	}
	if f.ScreenID != "" {
		var old screen
		if err = tx.Get(ctx, "screen", f.ScreenID, &old); err != nil {
			return err
		}
		if err = s.retireScreen(ctx, tx, old, now); err != nil {
			return err
		}
	}
	id, err := opaqueID()
	if err != nil {
		return err
	}
	visible, hidden, err := s.suggestions(ctx, tx)
	if err != nil {
		return err
	}
	actions := []action{}
	for _, name := range visible {
		actions = append(actions, action{Label: name, Kind: "add_name", Field: name})
	}
	if len(visible) > 0 {
		actions = append(actions, action{Label: "Скрыть подсказки", Kind: "hide_menu"})
	} else if len(hidden) > 0 {
		// It shares the Cancel row, so keeping the way back costs no space.
		actions = append(actions, action{Label: "Вернуть подсказки", Kind: "show_names", View: "add"})
	}
	actions = append(actions, action{Label: "Отмена", Kind: "cancel_add"})
	v := screen{ID: id, Text: "Добавление предметов\n\nВыберите название кнопкой или напишите своё. Можно прислать список: по одному предмету на строку, до 20.\nЗатем — резерв, текущий запас и подтверждение. Срок расхода можно уточнить по желанию.", Actions: actions, MessageID: at.Message, ChatID: at.Chat, ExpiresAt: now.Add(7 * 24 * time.Hour)}
	if err = tx.Insert(ctx, "screen", id, v); err != nil {
		return err
	}
	if err = tx.Put(ctx, "runtime", inputKey("adding", at.Chat), addFlow{ScreenID: id}); err != nil {
		return err
	}
	if err = tx.Put(ctx, "runtime", inputKey("awaiting_name", at.Chat), true); err != nil {
		return err
	}
	return s.queue(ctx, tx, id, now, false, "")
}

// suggestionScreen replaces the name prompt and stays part of the same adding
// flow: typed names, Cancel and /cancel keep working while it is open.
func (s *Service) suggestionScreen(ctx context.Context, tx *sqlstore.Tx, at place, now time.Time) error {
	f, err := loadAdding(ctx, tx, at.Chat)
	if err != nil {
		return err
	}
	if len(f.Names) > f.Index {
		return s.resumeAdding(ctx, tx, f, at, now)
	}
	if f.ScreenID != "" {
		var old screen
		if err = tx.Get(ctx, "screen", f.ScreenID, &old); err != nil {
			return err
		}
		if err = s.retireScreen(ctx, tx, old, now); err != nil {
			return err
		}
	}
	visible, hidden, err := s.suggestions(ctx, tx)
	if err != nil {
		return err
	}
	text := "Подсказки в /add\n\nНажмите на подсказку, чтобы скрыть её."
	if len(visible) == 0 {
		text = "Подсказки в /add\n\nВсе подсказки скрыты."
	}
	text += " Любое название по-прежнему можно написать текстом."
	actions := []action{}
	for _, name := range visible {
		actions = append(actions, action{Label: "✕ " + name, Kind: "hide_name", Field: name})
	}
	if len(hidden) > 0 {
		actions = append(actions, action{Label: "Вернуть скрытые", Kind: "show_names"})
	}
	actions = append(actions, action{Label: "Готово", Kind: "add"})
	id, err := opaqueID()
	if err != nil {
		return err
	}
	v := screen{ID: id, Text: text, Actions: actions, MessageID: at.Message, ChatID: at.Chat, ExpiresAt: now.Add(7 * 24 * time.Hour)}
	if err = tx.Insert(ctx, "screen", id, v); err != nil {
		return err
	}
	if err = tx.Put(ctx, "runtime", inputKey("adding", at.Chat), addFlow{ScreenID: id}); err != nil {
		return err
	}
	return s.queue(ctx, tx, id, now, false, "")
}

func (s *Service) hideSuggestion(ctx context.Context, tx *sqlstore.Tx, name string, at place, now time.Time) error {
	var stored []string
	if err := tx.Get(ctx, "runtime", "hidden_names", &stored); err != nil && !errors.Is(err, sqlstore.ErrNotFound) {
		return err
	}
	// A screen from an older shortcut list just re-renders; it must not poison the update.
	if slices.Contains(commonNames, name) && !slices.Contains(stored, name) {
		if err := tx.Put(ctx, "runtime", "hidden_names", append(stored, name)); err != nil {
			return err
		}
	}
	return s.suggestionScreen(ctx, tx, at, now)
}

func (s *Service) showSuggestions(ctx context.Context, tx *sqlstore.Tx, toPrompt bool, at place, now time.Time) error {
	if err := tx.Delete(ctx, "runtime", "hidden_names"); err != nil {
		return err
	}
	if toPrompt {
		return s.beginAdding(ctx, tx, at, now)
	}
	return s.suggestionScreen(ctx, tx, at, now)
}

func (s *Service) beginNames(ctx context.Context, tx *sqlstore.Tx, text string, at place, now time.Time) error {
	f, err := loadAdding(ctx, tx, at.Chat)
	if err != nil {
		return err
	}
	if f.Index < len(f.Names) {
		return s.note(ctx, tx, "Сначала завершите текущее добавление: /add продолжит, /cancel отменит оставшиеся. Новый список не принят.", nil, at.fresh(), now)
	}
	names, err := parseNames(text)
	if err != nil {
		return s.note(ctx, tx, err.Error(), nil, at.fresh(), now)
	}
	states, err := tx.Items(ctx)
	if err != nil {
		return err
	}
	filtered := []string{}
	for _, name := range names {
		exists := false
		for _, state := range states {
			if strings.EqualFold(state.Config.Name, name) {
				exists = true
				break
			}
		}
		if !exists {
			filtered = append(filtered, name)
		}
	}
	if len(filtered) == 0 {
		return s.note(ctx, tx, "Эти предметы уже есть в реестре или архиве: /items · /archive. Введите другие названия или /cancel.", nil, at.fresh(), now)
	}
	// Reuse the name-prompt message for text input; never leave its Cancel button live.
	if f.ScreenID != "" {
		var old screen
		if err = tx.Get(ctx, "screen", f.ScreenID, &old); err != nil {
			return err
		}
		if at.Message == 0 {
			at.Message = old.MessageID
		}
		if err = s.retireScreen(ctx, tx, old, now); err != nil {
			return err
		}
	}
	if err = tx.Put(ctx, "runtime", inputKey("awaiting_name", at.Chat), false); err != nil {
		return err
	}
	f = addFlow{Names: filtered}
	return s.nextAdding(ctx, tx, f, at, now)
}

func (s *Service) nextAdding(ctx context.Context, tx *sqlstore.Tx, f addFlow, at place, now time.Time) error {
	id, err := opaqueID()
	if err != nil {
		return err
	}
	d, err := dialog.NewQuick(id, f.Names[f.Index])
	if err != nil {
		return err
	}
	if err = s.saveNewDialog(ctx, tx, d, at, now); err != nil {
		return err
	}
	f.ScreenID = id
	if len(f.Names) > 1 {
		var v screen
		if err = tx.Get(ctx, "screen", id, &v); err != nil {
			return err
		}
		v.Text = fmt.Sprintf("Предмет %d из %d\n", f.Index+1, len(f.Names))
		if err = tx.Put(ctx, "screen", id, v); err != nil {
			return err
		}
	}
	return tx.Put(ctx, "runtime", inputKey("adding", at.Chat), f)
}

func (s *Service) resumeAdding(ctx context.Context, tx *sqlstore.Tx, f addFlow, at place, now time.Time) error {
	var old screen
	if err := tx.Get(ctx, "screen", f.ScreenID, &old); err != nil {
		return err
	}
	if old.Dialog == nil || old.Dialog.Step == dialog.Done {
		return errors.New("invalid active adding flow")
	}
	d := *old.Dialog
	id, err := opaqueID()
	if err != nil {
		return err
	}
	d.ID = id // Item ID stays fixed; the old keyboard becomes obsolete.
	if err = s.saveNewDialog(ctx, tx, d, at, now); err != nil {
		return err
	}
	f.ScreenID = id
	var v screen
	if err = tx.Get(ctx, "screen", id, &v); err != nil {
		return err
	}
	v.Text = old.Text
	if err = tx.Put(ctx, "screen", id, v); err != nil {
		return err
	}
	return tx.Put(ctx, "runtime", inputKey("adding", at.Chat), f)
}

func (s *Service) advanceAdding(ctx context.Context, tx *sqlstore.Tx, v screen, now time.Time) error {
	if v.Dialog == nil || !v.Dialog.Creating {
		return nil
	}
	f, err := loadAdding(ctx, tx, v.ChatID)
	if err != nil {
		return err
	}
	if f.ScreenID != v.ID {
		return nil
	} // A persisted legacy wizard has no queue.
	f.Index++
	if f.Index >= len(f.Names) {
		return tx.Delete(ctx, "runtime", inputKey("adding", v.ChatID))
	}
	return s.nextAdding(ctx, tx, f, place{Chat: v.ChatID}, now)
}

func (s *Service) cancelAdding(ctx context.Context, tx *sqlstore.Tx, at place, now time.Time) error {
	f, err := loadAdding(ctx, tx, at.Chat)
	if err != nil {
		return err
	}
	if f.ScreenID != "" {
		var old screen
		if err = tx.Get(ctx, "screen", f.ScreenID, &old); err != nil {
			return err
		}
		if at.Message == 0 {
			at.Message = old.MessageID
		}
		if err = s.retireScreen(ctx, tx, old, now); err != nil {
			return err
		}
		if old.Dialog != nil {
			if err = tx.Delete(ctx, "active", old.Dialog.ItemID); err != nil {
				return err
			}
		}
	}
	if err = tx.Delete(ctx, "runtime", inputKey("adding", at.Chat)); err != nil {
		return err
	}
	if err = tx.Put(ctx, "runtime", inputKey("awaiting_name", at.Chat), false); err != nil {
		return err
	}
	return s.note(ctx, tx, "Добавление отменено. Уже сохранённые предметы остались в реестре.\n/add — добавить · /items — предметы", nil, at, now)
}
