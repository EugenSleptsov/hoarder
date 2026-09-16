package bot

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/EugenSleptsov/hoarder/internal/app"
	"github.com/EugenSleptsov/hoarder/internal/sqlstore"
	"github.com/EugenSleptsov/hoarder/internal/telegram"
)

type delivery struct {
	ScreenID    string
	NextAttempt time.Time
	Proactive   bool
	Date        string
	Attempts    int
}

type session struct {
	Date      string
	ItemIDs   []string
	CreatedAt time.Time
}

type Messenger interface {
	Send(context.Context, telegram.Text) (telegram.Message, error)
	Edit(context.Context, telegram.Text) error
}

func (s *Service) queue(ctx context.Context, tx *sqlstore.Tx, id string, now time.Time, proactive bool, date string) error {
	return tx.Put(ctx, "delivery", id, delivery{ScreenID: id, NextAttempt: now, Proactive: proactive, Date: date})
}
func (s *Service) window(now time.Time) (string, bool, error) {
	date, e := s.daily.DateKey(now)
	if e != nil {
		return "", false, e
	}
	day, e := time.Parse("2006-01-02", date)
	if e != nil {
		return "", false, e
	}
	slot, e := s.daily.Slot(day.Year(), day.Month(), day.Day())
	if e != nil {
		return "", false, e
	}
	return date, !now.Before(slot) && now.Before(slot.Add(s.cfg.CatchUp)), nil
}

// Tick reads persisted dates, never re-plans overdue items. One digest includes
// every due item; pagination is presentation, never a competing-item budget.
func (s *Service) Tick(ctx context.Context, now time.Time) error {
	date, allowed, e := s.window(now)
	if e != nil || !allowed {
		return e
	}
	return s.db.Transaction(ctx, func(tx *sqlstore.Tx) error {
		var old session
		e := tx.Get(ctx, "session", date, &old)
		if e == nil {
			return nil
		}
		if !errors.Is(e, sqlstore.ErrNotFound) {
			return e
		}
		ids, e := s.selectItems(ctx, tx, "due", now)
		if e != nil {
			return e
		}
		if len(ids) == 0 {
			return nil
		}
		if e = tx.Insert(ctx, "session", date, session{date, ids, now}); e != nil {
			return e
		}
		id, e := s.menuScreen(ctx, tx, "due", 0, ids, 0, now)
		if e != nil {
			return e
		}
		return s.queue(ctx, tx, id, now, true, date)
	})
}

// Flush sends outside database transactions. A first send can be duplicated if
// Telegram accepts it but the process crashes before MessageID is saved.
// Call from one process per database; the mutex serialises this process's sender.
func (s *Service) Flush(ctx context.Context, api Messenger, now time.Time) error {
	if api == nil {
		return errors.New("Telegram messenger required")
	}
	s.delivery.Lock()
	defer s.delivery.Unlock()
	var jobs []json.RawMessage
	e := s.db.Transaction(ctx, func(tx *sqlstore.Tx) error { var e error; jobs, e = tx.List(ctx, "delivery"); return e })
	if e != nil {
		return e
	}
	for _, raw := range jobs {
		var job delivery
		if e = json.Unmarshal(raw, &job); e != nil {
			return e
		}
		if job.NextAttempt.After(now) {
			continue
		}
		var v screen
		skip := false
		e = s.db.Transaction(ctx, func(tx *sqlstore.Tx) error {
			e := tx.Get(ctx, "screen", job.ScreenID, &v)
			if e != nil {
				return e
			}
			if v.Used || !now.Before(v.ExpiresAt) {
				skip = true
			}
			if job.Proactive {
				date, allowed, err := s.window(now)
				if err != nil {
					return err
				}
				if date != job.Date || !allowed {
					skip = true
				}
			}
			if !skip {
				var err error
				skip, err = s.refreshPending(ctx, tx, &v, job, now)
				if err != nil {
					return err
				}
			}
			if skip {
				return tx.Delete(ctx, "delivery", job.ScreenID)
			}
			return nil
		})
		if e != nil {
			return e
		}
		if skip {
			continue
		}
		text := render(v, s.db.Owner())
		var sent telegram.Message
		if v.MessageID == 0 {
			sent, e = api.Send(ctx, text)
		} else {
			e = api.Edit(ctx, text)
		}
		if e != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			var apiErr *telegram.APIError
			retry := time.Duration(1<<min(job.Attempts, 8)) * time.Second
			if errors.As(e, &apiErr) && apiErr.RetryAfterSeconds > 0 {
				retry = time.Duration(apiErr.RetryAfterSeconds) * time.Second
			}
			job.Attempts++
			job.NextAttempt = now.Add(retry)
			// An invalid/deleted message cannot be repaired by endless identical edits.
			permanent := errors.As(e, &apiErr) && (apiErr.Code == 400 || apiErr.Code == 403)
			saveErr := s.db.Transaction(ctx, func(tx *sqlstore.Tx) error {
				if permanent {
					if err := tx.Put(ctx, "failed_delivery", job.ScreenID, job); err != nil {
						return err
					}
					return tx.Delete(ctx, "delivery", job.ScreenID)
				}
				return tx.Put(ctx, "delivery", job.ScreenID, job)
			})
			if saveErr != nil {
				return saveErr
			}
			// A server flood limit applies to the bot, not to independent forecasts.
			if errors.As(e, &apiErr) && apiErr.Code == 429 {
				return e
			}
			continue
		}
		e = s.db.Transaction(ctx, func(tx *sqlstore.Tx) error {
			// Read current state so a callback that raced a network request is not lost.
			var latest screen
			if err := tx.Get(ctx, "screen", v.ID, &latest); err != nil {
				return err
			}
			if latest.MessageID == 0 {
				latest.MessageID = sent.ID
			}
			if err := tx.Put(ctx, "screen", v.ID, latest); err != nil {
				return err
			}
			// If a callback advanced this screen in flight, retain its reconciliation.
			before, _ := hash(v.Dialog)
			after, _ := hash(latest.Dialog)
			if before != after {
				return s.queue(ctx, tx, v.ID, now, false, "")
			}
			return tx.Delete(ctx, "delivery", v.ID)
		})
		if e != nil {
			return e
		}
	}
	return nil
}

func (s *Service) selectItems(ctx context.Context, tx *sqlstore.Tx, view string, now time.Time) ([]string, error) {
	states, e := tx.Items(ctx)
	if e != nil {
		return nil, e
	}
	ids := []string{}
	for _, state := range states {
		if state.Archived != (view == "archive") {
			continue
		}
		if state.Paused && (view == "due" || view == "shop") {
			continue
		}
		switch view {
		case "all", "manage", "archive":
		case "due":
			var p app.QuestionPlan
			if e = tx.Get(ctx, "plan", state.Config.ID, &p); e != nil {
				return nil, e
			}
			if p.ExpectedRevision != state.Revision {
				return nil, errors.New("stored question revision mismatch")
			}
			if p.SendAt.After(now) {
				continue
			}
		case "shop":
			var in intent
			e = tx.Get(ctx, "intent", state.Config.ID, &in)
			if errors.Is(e, sqlstore.ErrNotFound) {
				continue
			}
			if e != nil {
				return nil, e
			}
		default:
			return nil, errors.New("invalid item view")
		}
		ids = append(ids, state.Config.ID)
	}
	return ids, nil
}
