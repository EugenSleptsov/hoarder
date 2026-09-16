package bot

import (
	"reflect"
	"testing"
	"time"

	"github.com/EugenSleptsov/hoarder/internal/sqlstore"
)

func TestAdversarialFloodCooldownExpiresWithoutChangingItems(t *testing.T) {
	h := newHarness(t)
	h.add("Паста", true, "Одна", "Полная")
	before := h.state("Паста")
	plan := h.plan(before.Config.ID)
	if err := h.db.Transaction(testContext, func(tx *sqlstore.Tx) error {
		return h.service.note(testContext, tx, "Pending message", nil, 0, h.now)
	}); err != nil {
		t.Fatal(err)
	}
	if err := h.service.Flush(testContext, &floodMessenger{Messenger: h.api}, h.now); err == nil {
		t.Fatal("expected rate limit")
	}
	h.reopen()
	counted := &countingMessenger{Messenger: h.api}
	if err := h.service.Flush(testContext, counted, h.now.Add(119*time.Second)); err != nil {
		t.Fatal(err)
	}
	if counted.calls != 0 {
		t.Fatal("cooldown ended early")
	}
	if err := h.service.Flush(testContext, counted, h.now.Add(120*time.Second)); err != nil {
		t.Fatal(err)
	}
	if counted.calls != 1 {
		t.Fatalf("retry not delivered at cooldown boundary: %d calls", counted.calls)
	}
	if !reflect.DeepEqual(before, h.state("Паста")) || !reflect.DeepEqual(plan, h.plan(before.Config.ID)) {
		t.Fatal("transport cooldown changed forecast evidence or deadline")
	}
}

func TestAdversarialDeferredCandidateKeepsUpdatedRetryDate(t *testing.T) {
	h := newHarness(t)
	const first = "000000000000000000000001"
	const second = "ffffffffffffffffffffffff"
	later := h.now.Add(time.Hour)
	if err := h.db.Transaction(testContext, func(tx *sqlstore.Tx) error {
		for i, id := range []string{first, second} {
			v := screen{ID: id, MessageID: int64(701 + i), Text: "Pending", ExpiresAt: later.Add(time.Hour)}
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
	counted := &countingMessenger{Messenger: h.api}
	m := &interleavedMessenger{Messenger: counted, onEdit: func() {
		if err := h.db.Transaction(testContext, func(tx *sqlstore.Tx) error {
			var job delivery
			if err := tx.Get(testContext, "delivery", second, &job); err != nil {
				return err
			}
			job.NextAttempt = later
			return tx.Put(testContext, "delivery", second, job)
		}); err != nil {
			t.Fatal(err)
		}
	}}
	if err := h.service.Flush(testContext, m, h.now); err != nil {
		t.Fatal(err)
	}
	if counted.calls != 1 {
		t.Fatal("sent stale candidate before its new retry date")
	}
	if err := h.db.Transaction(testContext, func(tx *sqlstore.Tx) error {
		var job delivery
		if err := tx.Get(testContext, "delivery", second, &job); err != nil {
			return err
		}
		if !job.NextAttempt.Equal(later) {
			t.Fatal("lost deferred job")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := h.service.Flush(testContext, counted, later); err != nil {
		t.Fatal(err)
	}
	if counted.calls != 2 {
		t.Fatal("deferred delivery disappeared")
	}
}
