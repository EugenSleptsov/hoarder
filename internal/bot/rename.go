package bot

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/EugenSleptsov/hoarder/internal/app"
	"github.com/EugenSleptsov/hoarder/internal/item"
	"github.com/EugenSleptsov/hoarder/internal/sqlstore"
)

// renaming waits for the next ordinary text message. It is bound to one prompt
// screen and to the item revision that prompt showed. The short window keeps a
// much later message from being taken as a name.
type renaming struct {
	ScreenID         string
	ItemID           string
	ExpectedRevision uint64
}

const renameWindow = 30 * time.Minute

func (s *Service) beginRename(ctx context.Context, tx *sqlstore.Tx, state item.State, at place, now time.Time) error {
	if err := s.dropRename(ctx, tx, at.Chat, now); err != nil {
		return err
	}
	id, err := opaqueID()
	if err != nil {
		return err
	}
	text := state.Config.Name + "\n\nПришлите новое название сообщением: одна строка, до 80 символов. Остаток, история, настройки, дата проверки и связи с магазином сохранятся.\n/cancel — отмена"
	v := screen{ID: id, Text: text, Actions: []action{itemAction("Отмена", "manage", state)}, MessageID: at.Message, ChatID: at.Chat, ExpiresAt: now.Add(renameWindow)}
	if err = tx.Insert(ctx, "screen", id, v); err != nil {
		return err
	}
	if err = tx.Put(ctx, "runtime", inputKey("renaming", at.Chat), renaming{ScreenID: id, ItemID: state.Config.ID, ExpectedRevision: state.Revision}); err != nil {
		return err
	}
	return s.queue(ctx, tx, id, now, false, "")
}

// activeRename reports a pending rename while its prompt has not been used.
// Any tap on the prompt, including Cancel, consumes it and ends the rename.
func (s *Service) activeRename(ctx context.Context, tx *sqlstore.Tx, chat int64) (renaming, screen, bool, error) {
	var r renaming
	var v screen
	err := tx.Get(ctx, "runtime", inputKey("renaming", chat), &r)
	if errors.Is(err, sqlstore.ErrNotFound) {
		return r, v, false, nil
	}
	if err != nil {
		return r, v, false, err
	}
	err = tx.Get(ctx, "screen", r.ScreenID, &v)
	if err != nil && !errors.Is(err, sqlstore.ErrNotFound) {
		return r, v, false, err
	}
	// A missing prompt must not turn every later message into a failing update.
	if err != nil || v.Used {
		return r, v, false, tx.Delete(ctx, "runtime", inputKey("renaming", chat))
	}
	return r, v, true, nil
}

func (s *Service) dropRename(ctx context.Context, tx *sqlstore.Tx, chat int64, now time.Time) error {
	_, prompt, active, err := s.activeRename(ctx, tx, chat)
	if err != nil || !active {
		return err
	}
	if err = s.retireScreen(ctx, tx, prompt, now); err != nil {
		return err
	}
	return tx.Delete(ctx, "runtime", inputKey("renaming", chat))
}

// finishRename changes only the name. The saved, possibly overdue, question
// date is kept: a name is not evidence and must not move the plan.
func (s *Service) finishRename(ctx context.Context, tx *sqlstore.Tx, chat int64, r renaming, prompt screen, text string, now time.Time) error {
	at := place{Chat: chat}
	if !now.Before(prompt.ExpiresAt) {
		if err := s.dropRename(ctx, tx, chat, now); err != nil {
			return err
		}
		return s.note(ctx, tx, "Время на ввод названия истекло, название не изменено. Откройте /manage и нажмите «Переименовать» ещё раз.", nil, at, now)
	}
	if text == "" || strings.ContainsAny(text, "\r\n") {
		return s.note(ctx, tx, "Пришлите одно название одной строкой или /cancel.", nil, at, now)
	}
	if err := validName(text); err != nil {
		return s.note(ctx, tx, err.Error()+" Пришлите другое название или /cancel.", nil, at, now)
	}
	state, err := tx.LoadItem(ctx, r.ItemID)
	if err != nil && !errors.Is(err, sqlstore.ErrNotFound) {
		return err
	}
	if err != nil || state.Archived || state.Revision != r.ExpectedRevision {
		if err = s.dropRename(ctx, tx, chat, now); err != nil {
			return err
		}
		return s.note(ctx, tx, "Предмет изменился, пока вы вводили название. Откройте /manage и начните заново.", nil, at, now)
	}
	if text == state.Config.Name {
		if err = s.dropRename(ctx, tx, chat, now); err != nil {
			return err
		}
		return s.managementCardWith(ctx, tx, state, "Название не изменилось.", prompt.at(), now)
	}
	states, err := tx.Items(ctx)
	if err != nil {
		return err
	}
	for _, other := range states {
		if other.Config.ID != state.Config.ID && strings.EqualFold(other.Config.Name, text) {
			return s.note(ctx, tx, "Название «"+other.Config.Name+"» уже занято в реестре или архиве. Пришлите другое или /cancel.", nil, at, now)
		}
	}
	cfg := state.Config
	cfg.Name = text
	event := item.Event{ID: "control:" + r.ScreenID, ItemID: state.Config.ID, Kind: item.Reconfigure, At: now, Configuration: &cfg}
	// Validate before storage so a rejected rename is a reply, not a poison update.
	if _, err = state.Apply(event); err != nil {
		if err = s.dropRename(ctx, tx, chat, now); err != nil {
			return err
		}
		return s.note(ctx, tx, "Переименовать не удалось. Откройте /manage.", nil, at, now)
	}
	next, err := tx.ApplyEvent(ctx, event, state.Revision)
	if err != nil {
		return err
	}
	var plan app.QuestionPlan
	if err = tx.Get(ctx, "plan", state.Config.ID, &plan); err != nil {
		return err
	}
	if plan.ExpectedRevision != state.Revision {
		return errors.New("rename found a stale plan")
	}
	plan.ExpectedRevision = next.Revision
	if err = tx.SaveQuestion(ctx, plan); err != nil {
		return err
	}
	// An open stock question still shows the old name and revision.
	if err = s.invalidateQuestion(ctx, tx, state.Config.ID, now); err != nil {
		return err
	}
	if err = s.dropRename(ctx, tx, chat, now); err != nil {
		return err
	}
	return s.managementCardWith(ctx, tx, next, "Название изменено: «"+state.Config.Name+"» → «"+next.Config.Name+"».", prompt.at(), now)
}
