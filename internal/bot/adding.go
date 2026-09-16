package bot

import (
	"context"
	"errors"
	"fmt"
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

func loadAdding(ctx context.Context, tx *sqlstore.Tx) (addFlow, error) {
	var f addFlow
	err := tx.Get(ctx, "runtime", "adding", &f)
	if errors.Is(err, sqlstore.ErrNotFound) {
		err = nil
	}
	return f, err
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
		if !utf8.ValidString(name) || utf8.RuneCountInString(name) > 80 || strings.IndexFunc(name, unicode.IsControl) >= 0 || strings.HasPrefix(name, "/") {
			return nil, errors.New("Название: до 80 символов, без команд и управляющих символов.")
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

func (s *Service) beginAdding(ctx context.Context, tx *sqlstore.Tx, messageID int64, now time.Time) error {
	f, err := loadAdding(ctx, tx)
	if err != nil {
		return err
	}
	if len(f.Names) > f.Index {
		return s.resumeAdding(ctx, tx, f, messageID, now)
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
	states, err := tx.Items(ctx)
	if err != nil {
		return err
	}
	actions := []action{}
	for _, name := range commonNames {
		exists := false
		for _, state := range states {
			if strings.EqualFold(state.Config.Name, name) {
				exists = true
				break
			}
		}
		if !exists {
			actions = append(actions, action{Label: name, Kind: "add_name", Field: name})
		}
	}
	actions = append(actions, action{Label: "Отмена", Kind: "cancel_add"})
	v := screen{ID: id, Text: "Добавление предметов\n\nВыберите название кнопкой или напишите своё. Можно прислать список: по одному предмету на строку, до 20.\nЗатем — резерв, текущий запас и подтверждение. Срок расхода можно уточнить по желанию.", Actions: actions, MessageID: messageID, ExpiresAt: now.Add(7 * 24 * time.Hour)}
	if err = tx.Insert(ctx, "screen", id, v); err != nil {
		return err
	}
	if err = tx.Put(ctx, "runtime", "adding", addFlow{ScreenID: id}); err != nil {
		return err
	}
	if err = tx.Put(ctx, "runtime", "awaiting_name", true); err != nil {
		return err
	}
	return s.queue(ctx, tx, id, now, false, "")
}

func (s *Service) beginNames(ctx context.Context, tx *sqlstore.Tx, text string, messageID int64, now time.Time) error {
	f, err := loadAdding(ctx, tx)
	if err != nil {
		return err
	}
	if f.Index < len(f.Names) {
		return s.note(ctx, tx, "Сначала завершите текущее добавление: /add продолжит, /cancel отменит оставшиеся. Новый список не принят.", nil, 0, now)
	}
	names, err := parseNames(text)
	if err != nil {
		return s.note(ctx, tx, err.Error(), nil, 0, now)
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
		return s.note(ctx, tx, "Эти предметы уже есть в реестре или архиве: /items · /archive. Введите другие названия или /cancel.", nil, 0, now)
	}
	// Reuse the name-prompt message for text input; never leave its Cancel button live.
	if f.ScreenID != "" {
		var old screen
		if err = tx.Get(ctx, "screen", f.ScreenID, &old); err != nil {
			return err
		}
		if messageID == 0 {
			messageID = old.MessageID
		}
		if err = s.retireScreen(ctx, tx, old, now); err != nil {
			return err
		}
	}
	if err = tx.Put(ctx, "runtime", "awaiting_name", false); err != nil {
		return err
	}
	f = addFlow{Names: filtered}
	return s.nextAdding(ctx, tx, f, messageID, now)
}

func (s *Service) nextAdding(ctx context.Context, tx *sqlstore.Tx, f addFlow, messageID int64, now time.Time) error {
	id, err := opaqueID()
	if err != nil {
		return err
	}
	d, err := dialog.NewQuick(id, f.Names[f.Index])
	if err != nil {
		return err
	}
	if err = s.saveNewDialog(ctx, tx, d, messageID, now); err != nil {
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
	return tx.Put(ctx, "runtime", "adding", f)
}

func (s *Service) resumeAdding(ctx context.Context, tx *sqlstore.Tx, f addFlow, messageID int64, now time.Time) error {
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
	if err = s.saveNewDialog(ctx, tx, d, messageID, now); err != nil {
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
	return tx.Put(ctx, "runtime", "adding", f)
}

func (s *Service) advanceAdding(ctx context.Context, tx *sqlstore.Tx, v screen, now time.Time) error {
	if v.Dialog == nil || !v.Dialog.Creating {
		return nil
	}
	f, err := loadAdding(ctx, tx)
	if err != nil {
		return err
	}
	if f.ScreenID != v.ID {
		return nil
	} // A persisted legacy wizard has no queue.
	f.Index++
	if f.Index >= len(f.Names) {
		return tx.Delete(ctx, "runtime", "adding")
	}
	return s.nextAdding(ctx, tx, f, 0, now)
}

func (s *Service) cancelAdding(ctx context.Context, tx *sqlstore.Tx, messageID int64, now time.Time) error {
	f, err := loadAdding(ctx, tx)
	if err != nil {
		return err
	}
	if f.ScreenID != "" {
		var old screen
		if err = tx.Get(ctx, "screen", f.ScreenID, &old); err != nil {
			return err
		}
		if messageID == 0 {
			messageID = old.MessageID
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
	if err = tx.Delete(ctx, "runtime", "adding"); err != nil {
		return err
	}
	if err = tx.Put(ctx, "runtime", "awaiting_name", false); err != nil {
		return err
	}
	return s.note(ctx, tx, "Добавление отменено. Уже сохранённые предметы остались в реестре.\n/add — добавить · /items — предметы", nil, messageID, now)
}
