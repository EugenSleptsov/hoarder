package bot

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/EugenSleptsov/hoarder/internal/sqlstore"
	"github.com/EugenSleptsov/hoarder/internal/telegram"
)

// Handle commits a durable decision before the polling cursor advances. It does
// not make network calls. The caller acknowledges every callback after return.
func (s *Service) Handle(ctx context.Context, u telegram.Update, now time.Time) (string, error) {
	if now.IsZero() {
		return "", errors.New("processing time required")
	}
	digest, e := hash(u)
	if e != nil {
		return "", e
	}
	notice := ""
	e = s.db.Transaction(ctx, func(tx *sqlstore.Tx) error {
		key := strconv.FormatInt(u.ID, 10)
		var previous receipt
		err := tx.Get(ctx, "update", key, &previous)
		if err == nil {
			if previous.Hash != digest {
				return errors.New("update ID payload conflict")
			}
			notice = previous.Notice
			return tx.AdvanceOffset(ctx, u.ID)
		}
		if !errors.Is(err, sqlstore.ErrNotFound) {
			return err
		}
		// A missing receipt is expected for a new update, not a processing
		// failure. Ignored foreign messages and unsupported update types must
		// still acquire a durable receipt and advance the polling cursor.
		err = nil
		if u.Callback != nil {
			notice, err = s.callback(ctx, tx, *u.Callback, now)
		} else if u.Message != nil && u.Message.Chat.ID == s.db.Owner() && u.Message.Chat.Type == "private" {
			err = s.command(ctx, tx, u.Message.Text, now)
		}
		if err != nil {
			return err
		}
		if err = tx.Insert(ctx, "update", key, receipt{digest, notice}); err != nil {
			return err
		}
		return tx.AdvanceOffset(ctx, u.ID)
	})
	return notice, e
}

// BeginPolling starts by reading the oldest unconfirmed Telegram update. A
// persisted offset may be too high after an idle week, when Telegram randomizes
// its next update ID. Receipts, not a forever-high cursor, prevent duplicate
// mutations. Only the single polling worker calls this at startup.
func (s *Service) BeginPolling(ctx context.Context) error {
	return s.db.Transaction(ctx, func(tx *sqlstore.Tx) error {
		return tx.Put(ctx, "runtime", "offset", int64(0))
	})
}

// CompleteEmptyPoll is called ONLY after a successful empty getUpdates response.
// That request has acknowledged the preceding batch. Forget the high cursor
// before the next idle period, but do not reset a concurrently advanced cursor.
func (s *Service) CompleteEmptyPoll(ctx context.Context, requestedOffset int64) error {
	if requestedOffset < 0 {
		return errors.New("invalid polling offset")
	}
	return s.db.Transaction(ctx, func(tx *sqlstore.Tx) error {
		current, err := tx.Offset(ctx)
		if err != nil {
			return err
		}
		if current != requestedOffset {
			return nil
		}
		return tx.Put(ctx, "runtime", "offset", int64(0))
	})
}
