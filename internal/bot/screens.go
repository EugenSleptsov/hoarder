package bot

import (
	"context"
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
		return true, nil
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
