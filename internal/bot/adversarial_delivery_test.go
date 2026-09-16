package bot

import (
	"context"
	"testing"

	"github.com/EugenSleptsov/hoarder/internal/sqlstore"
	"github.com/EugenSleptsov/hoarder/internal/telegram"
)

// Both jobs are already in Flush's snapshot. A callback while the first request
// runs changes the second job from a render into a keyboard removal.
func TestAdversarialRetirementOfLaterJobSurvivesFlushSnapshot(t *testing.T) {
	h := newHarness(t)
	const first = "000000000000000000000001"
	const second = "ffffffffffffffffffffffff"
	if err := h.db.Transaction(testContext, func(tx *sqlstore.Tx) error {
		for i, id := range []string{first, second} {
			v := screen{ID: id, MessageID: int64(501 + i), Text: "still interactive", ExpiresAt: h.now.AddDate(0, 0, 1), Actions: []action{{Label: "Items", Kind: "view", View: "all"}}}
			if err := tx.Insert(testContext, "screen", id, v); err != nil {
				return err
			}
			if err := h.service.queue(testContext, tx, id, h.now, false, ""); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// Establish the physical keyboard before queuing its replacement.
	if err := h.api.Edit(testContext, telegram.Text{ChatID: 123, MessageID: 502, Text: "still interactive", Keyboard: &telegram.Keyboard{Rows: [][]telegram.Button{{{Text: "Items", Data: "m1:" + second + ":0"}}}}}); err != nil {
		t.Fatal(err)
	}
	m := &interleavedMessenger{Messenger: h.api, onEdit: func() {
		if err := h.db.Transaction(testContext, func(tx *sqlstore.Tx) error {
			var v screen
			if err := tx.Get(testContext, "screen", second, &v); err != nil {
				return err
			}
			return h.service.retireScreen(testContext, tx, v, h.now)
		}); err != nil {
			t.Fatal(err)
		}
	}}
	if err := h.service.Flush(testContext, m, h.now); err != nil {
		t.Fatal(err)
	}
	if err := h.service.Flush(testContext, h.api, h.now); err != nil {
		t.Fatal(err)
	}
	assertNoKeyboard(t, h.wire.message(502))
}

type countingMessenger struct {
	Messenger
	calls int
}

func (m *countingMessenger) Send(ctx context.Context, v telegram.Text) (telegram.Message, error) {
	m.calls++
	return m.Messenger.Send(ctx, v)
}
func (m *countingMessenger) Edit(ctx context.Context, v telegram.Text) error {
	m.calls++
	return m.Messenger.Edit(ctx, v)
}
func (m *countingMessenger) ClearKeyboard(ctx context.Context, c, id int64) error {
	m.calls++
	return m.Messenger.ClearKeyboard(ctx, c, id)
}

func TestAdversarialFloodCooldownCoversOtherJobsAndRestart(t *testing.T) {
	h := newHarness(t)
	h.command("/items")
	// Independent non-domain UI jobs, both ready now.
	if err := h.db.Transaction(testContext, func(tx *sqlstore.Tx) error {
		if err := h.service.note(testContext, tx, "One", nil, 0, h.now); err != nil {
			return err
		}
		return h.service.note(testContext, tx, "Two", nil, 0, h.now)
	}); err != nil {
		t.Fatal(err)
	}
	limited := &floodMessenger{Messenger: h.api}
	if err := h.service.Flush(testContext, limited, h.now); err == nil {
		t.Fatal("expected Telegram flood error")
	}
	h.reopen()
	counted := &countingMessenger{Messenger: h.api}
	if err := h.service.Flush(testContext, counted, h.now); err != nil {
		t.Fatal(err)
	}
	if counted.calls != 0 {
		t.Fatalf("sent %d jobs during bot-wide retry_after after restart", counted.calls)
	}
}

type floodMessenger struct{ Messenger }

func (m *floodMessenger) Send(context.Context, telegram.Text) (telegram.Message, error) {
	return telegram.Message{}, &telegram.APIError{Code: 429, RetryAfterSeconds: 120}
}
