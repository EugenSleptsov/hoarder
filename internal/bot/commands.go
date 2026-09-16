package bot

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/EugenSleptsov/hoarder/internal/app"
	"github.com/EugenSleptsov/hoarder/internal/dialog"
	"github.com/EugenSleptsov/hoarder/internal/forecast"
	"github.com/EugenSleptsov/hoarder/internal/item"
	"github.com/EugenSleptsov/hoarder/internal/sqlstore"
)

func (s *Service) command(ctx context.Context, tx *sqlstore.Tx, text string, now time.Time) error {
	text = strings.TrimSpace(text)
	if text == "/start" || text == "/items" {
		return s.menu(ctx, tx, "all", 0, nil, 0, now)
	}
	if text == "/manage" {
		return s.menu(ctx, tx, "manage", 0, nil, 0, now)
	}
	if text == "/archive" {
		return s.menu(ctx, tx, "archive", 0, nil, 0, now)
	}
	if text == "/today" {
		return s.menu(ctx, tx, "due", 0, nil, 0, now)
	}
	if text == "/shopping" {
		return s.menu(ctx, tx, "shop", 0, nil, 0, now)
	}
	if text == "/cancel" {
		return tx.Put(ctx, "runtime", "awaiting_name", false)
	}
	name := ""
	if strings.HasPrefix(text, "/add ") {
		name = strings.TrimSpace(strings.TrimPrefix(text, "/add "))
	} else {
		var awaiting bool
		e := tx.Get(ctx, "runtime", "awaiting_name", &awaiting)
		if e != nil && !errors.Is(e, sqlstore.ErrNotFound) {
			return e
		}
		if awaiting && !strings.HasPrefix(text, "/") {
			name = text
		}
	}
	if text == "/add" {
		if e := tx.Put(ctx, "runtime", "awaiting_name", true); e != nil {
			return e
		}
		return s.note(ctx, tx, "Введите название предмета одним сообщением.", []action{{Label: "Отмена", Kind: "cancel_add"}}, 0, now)
	}
	if name == "" {
		return s.note(ctx, tx, "Команды: /items, /today, /shopping, /add, /manage, /archive. Ответы на вопросы — кнопками.", nil, 0, now)
	}
	if utf8.RuneCountInString(name) > 80 || strings.IndexFunc(name, unicode.IsControl) >= 0 {
		return s.note(ctx, tx, "Название: одна строка, не более 80 символов.", nil, 0, now)
	}
	items, e := tx.Items(ctx)
	if e != nil {
		return e
	}
	for _, existing := range items {
		if strings.EqualFold(existing.Config.Name, name) {
			destination := "/items"
			if existing.Archived {
				destination = "/archive"
			}
			return s.note(ctx, tx, "Предмет с таким названием уже есть. Откройте "+destination+".", nil, 0, now)
		}
	}
	id, e := opaqueID()
	if e != nil {
		return e
	}
	d, e := dialog.New(id, id, name, true, false, 0, time.Time{}, time.Time{})
	if e != nil {
		return e
	}
	if e = tx.Put(ctx, "runtime", "awaiting_name", false); e != nil {
		return e
	}
	return s.saveNewDialog(ctx, tx, d, 0, now)
}

func (s *Service) saveNewDialog(ctx context.Context, tx *sqlstore.Tx, d dialog.State, messageID int64, now time.Time) error {
	v := screen{ID: d.ID, Dialog: &d, MessageID: messageID, ExpiresAt: now.Add(7 * 24 * time.Hour)}
	if e := tx.Insert(ctx, "screen", v.ID, v); e != nil {
		return e
	}
	if e := tx.Put(ctx, "active", d.ItemID, d.ID); e != nil {
		return e
	}
	return s.queue(ctx, tx, v.ID, now, false, "")
}
func (s *Service) beginCheck(ctx context.Context, tx *sqlstore.Tx, id string, bought bool, messageID int64, now time.Time) error {
	current, e := tx.LoadItem(ctx, id)
	if e != nil {
		return e
	}
	if current.Archived {
		return s.note(ctx, tx, "Предмет в архиве. Восстановите его через /archive.", nil, messageID, now)
	}
	dialogID, e := opaqueID()
	if e != nil {
		return e
	}
	history, since := current.Anchor.At, current.AsOf
	if bought {
		history = time.Time{}
		since = time.Time{}
	}
	d, e := dialog.New(dialogID, id, current.Config.Name, false, current.Config.WithReserve, current.Revision, history, since)
	if e != nil {
		return e
	}
	return s.saveNewDialog(ctx, tx, d, messageID, now)
}

func (s *Service) finish(ctx context.Context, tx *sqlstore.Tx, v *screen, now time.Time) error {
	d := *v.Dialog
	v.Text = d.Text()
	v.Actions = []action{{Label: "Остальные вопросы", Kind: "view", View: "due"}, {Label: "Список покупок", Kind: "view", View: "shop"}, {Label: "Все предметы", Kind: "view", View: "all"}}
	if d.Cancelled {
		return nil
	}
	var state item.State
	var e error
	if d.Creating {
		reserve := 0.0
		if d.WithReserve {
			reserve = 1
		}
		cfg := item.Config{ID: d.ItemID, Name: d.Name, WithReserve: d.WithReserve, ReserveUnits: reserve, LeadDays: 3, MaxCheckDays: 90}
		state, e = item.New(cfg, item.Interval{Low: d.Low, High: d.High}, item.Interval{Low: .5 / d.DurationDays, High: 1.5 / d.DurationDays}, d.ObservedAt)
		if e == nil {
			e = tx.CreateItem(ctx, state)
		}
	} else {
		event := item.Event{ID: "dialog:" + d.ID, ItemID: d.ItemID, Kind: item.Snapshot, At: d.ObservedAt, Quantity: item.Interval{Low: d.Low, High: d.High}, HistoryComplete: d.HistoryComplete}
		if d.Unknown {
			event.Kind = item.Unknown
			event.Quantity = item.Interval{}
			event.HistoryComplete = false
		}
		if d.Unused {
			event.Kind = item.NoUse
			event.Quantity = item.Interval{}
			event.From = d.NoUseSince
		}
		state, e = tx.ApplyEvent(ctx, event, d.ItemRevision)
	}
	if e != nil {
		return e
	}
	// Replan only this changed item; keep unresolved physical deadlines visible.
	plan, e := app.PlanItem(state, forecast.Baseline{}, s.daily, now.Add(time.Second))
	if e != nil {
		return e
	}
	if e = tx.SaveQuestion(ctx, plan); e != nil {
		return e
	}
	if state.Paused {
		v.Text += "\nОпросы приостановлены. Остаток обновлён, но уведомления не включены."
	} else {
		v.Text += "\nСледующий опрос: " + plan.LocalDate + " (" + s.cfg.Zone + ")."
	}
	if !d.Unknown {
		needed, err := s.updateIntent(ctx, tx, state, now)
		if err != nil {
			return err
		}
		if needed && !state.Paused {
			v.Text += "\nДобавлено в список покупок. Это рекомендация, а не заказ."
		}
		if needed && state.Paused {
			v.Text += "\nРекомендация пополнить сохранена, но скрыта из списка покупок на время паузы."
		}
	}
	return nil
}

func (s *Service) Offset(ctx context.Context) (int64, error) {
	var n int64
	e := s.db.Transaction(ctx, func(tx *sqlstore.Tx) error { var e error; n, e = tx.Offset(ctx); return e })
	return n, e
}

func (s *Service) note(ctx context.Context, tx *sqlstore.Tx, text string, actions []action, messageID int64, now time.Time) error {
	id, e := opaqueID()
	if e != nil {
		return e
	}
	v := screen{ID: id, Text: text, Actions: actions, MessageID: messageID, ExpiresAt: now.Add(7 * 24 * time.Hour)}
	if e = tx.Insert(ctx, "screen", id, v); e != nil {
		return e
	}
	return s.queue(ctx, tx, id, now, false, "")
}
