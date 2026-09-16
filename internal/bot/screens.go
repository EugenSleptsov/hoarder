package bot

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/EugenSleptsov/hoarder/internal/sqlstore"
)

func messageKey(id int64) string { return strconv.FormatInt(id, 10) }

// ownsMessage distinguishes an obsolete screen from a fresh keyboard that has
// already replaced it IN THE SAME Telegram message. Missing entries are legacy
// screens and first-send recovery; they are bound on their next delivery.
func (s *Service) ownsMessage(ctx context.Context, tx *sqlstore.Tx, v screen) (bool, error) {
	if v.MessageID == 0 {
		return true, nil
	}
	var current string
	err := tx.Get(ctx, "message_screen", messageKey(v.MessageID), &current)
	if errors.Is(err, sqlstore.ErrNotFound) {
		// Before message ledgers existed a used menu could share its message
		// with a newer screen. Adopt only an unambiguous live successor.
		rows, e := tx.List(ctx, "screen")
		if e != nil {
			return false, e
		}
		candidate := ""
		for _, raw := range rows {
			var other screen
			if e = json.Unmarshal(raw, &other); e != nil {
				return false, e
			}
			if other.MessageID != v.MessageID || other.Used {
				continue
			}
			if other.Dialog != nil && other.Dialog.Step != "done" {
				var active string
				e = tx.Get(ctx, "active", other.Dialog.ItemID, &active)
				if errors.Is(e, sqlstore.ErrNotFound) {
					continue
				}
				if e != nil {
					return false, e
				}
				if active != other.ID {
					continue
				}
			}
			if candidate != "" && candidate != other.ID {
				return false, nil
			}
			candidate = other.ID
		}
		if candidate == "" {
			return true, nil
		}
		return candidate == v.ID, tx.Put(ctx, "message_screen", messageKey(v.MessageID), candidate)
	}
	return current == v.ID, err
}

// retireScreen invalidates callbacks immediately and durably queues markup-only
// cleanup. Network failure cannot make the old buttons valid again. A newer
// screen owning the same message makes cleanup unnecessary and unsafe.
func (s *Service) retireScreen(ctx context.Context, tx *sqlstore.Tx, v screen, now time.Time) error {
	v.Used = true
	if err := tx.Put(ctx, "screen", v.ID, v); err != nil {
		return err
	}
	if err := tx.Delete(ctx, "delivery", v.ID); err != nil {
		return err
	}
	if v.MessageID == 0 {
		return nil
	}
	owned, err := s.ownsMessage(ctx, tx, v)
	if err != nil || !owned {
		return err
	}
	return tx.Put(ctx, "delivery", v.ID, delivery{ScreenID: v.ID, NextAttempt: now, ClearOnly: true})
}
