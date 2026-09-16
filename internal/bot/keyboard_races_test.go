package bot

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/EugenSleptsov/hoarder/internal/sqlstore"
	"github.com/EugenSleptsov/hoarder/internal/telegram"
)

type interleavedMessenger struct {
	Messenger
	onEdit    func()
	onClear   func()
	editError error
}

func (m *interleavedMessenger) Edit(ctx context.Context, text telegram.Text) error {
	if m.onEdit != nil {
		f := m.onEdit
		m.onEdit = nil
		f()
	}
	if m.editError != nil {
		return m.editError
	}
	return m.Messenger.Edit(ctx, text)
}
func (m *interleavedMessenger) ClearKeyboard(ctx context.Context, chat, message int64) error {
	if m.onClear != nil {
		f := m.onClear
		m.onClear = nil
		f()
	}
	return m.Messenger.ClearKeyboard(ctx, chat, message)
}

func TestFailedInflightEditCannotDeleteNewerReceiptDelivery(t *testing.T) {
	h := newHarness(t)
	h.add("Паста", true, "Одна", "Полная")
	h.command("/items")
	h.press("Паста")
	h.press("Нет")
	reply := h.button("50%")
	id := strings.Split(reply.Callback.Data, ":")[1]
	if err := h.db.Transaction(testContext, func(tx *sqlstore.Tx) error { return h.service.queue(testContext, tx, id, h.now, false, "") }); err != nil {
		t.Fatal(err)
	}
	m := &interleavedMessenger{Messenger: h.api, editError: &telegram.APIError{Code: 403}}
	m.onEdit = func() {
		if _, err := h.service.Handle(testContext, reply, h.now); err != nil {
			t.Fatal(err)
		}
	}
	if err := h.service.Flush(testContext, m, h.now); err != nil {
		t.Fatal(err)
	}
	if h.state("Паста").Revision != 1 {
		t.Fatal("accepted answer lost")
	}
	if err := h.service.Flush(testContext, h.api, h.now); err != nil {
		t.Fatal(err)
	}
	assertNoKeyboard(t, h.wire.snapshot())
	if !strings.Contains(h.wire.snapshot().Text, "сохранён") {
		t.Fatal("new receipt was lost behind old failed delivery")
	}
}

func TestInflightCleanupReconcilesNewerKeyboard(t *testing.T) {
	h := newHarness(t)
	h.command("/add Паста")
	old := h.wire.snapshot()
	data := h.button("С резервом").Callback.Data
	id := strings.Split(data, ":")[1]
	if err := h.db.Transaction(testContext, func(tx *sqlstore.Tx) error {
		var v screen
		if err := tx.Get(testContext, "screen", id, &v); err != nil {
			return err
		}
		return h.service.retireScreen(testContext, tx, v, h.now)
	}); err != nil {
		t.Fatal(err)
	}
	m := &interleavedMessenger{Messenger: h.api, onClear: func() {
		if err := h.db.Transaction(testContext, func(tx *sqlstore.Tx) error {
			return h.service.menu(testContext, tx, "all", 0, nil, old.MessageID, h.now)
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
	if len(h.wire.message(old.MessageID).Keyboard.Rows) == 0 {
		t.Fatal("inflight cleanup left new screen without buttons")
	}
	h.press("Добавить предмет")
}

func TestLegacyObsoleteScreenCannotClearReplacementWithoutLedger(t *testing.T) {
	h := newHarness(t)
	h.command("/items")
	old := h.button("Добавить предмет")
	h.process(old)
	fresh := h.wire.snapshot()
	if err := h.db.Transaction(testContext, func(tx *sqlstore.Tx) error {
		return tx.Delete(testContext, "message_screen", messageKey(fresh.MessageID))
	}); err != nil {
		t.Fatal(err)
	}
	h.sequence++
	old.ID = h.sequence
	old.Callback.ID = "legacy-stale"
	h.process(old)
	if !reflect.DeepEqual(fresh, h.wire.message(fresh.MessageID)) {
		t.Fatal("legacy stale tap erased a newer screen")
	}
}

func TestCompletedTapRepairsReceiptWithoutReapplyingAnswer(t *testing.T) {
	h := newHarness(t)
	h.command("/add Паста")
	h.press("Без резерва")
	h.press("Сейчас нет")
	u := h.button("Добавить")
	h.wire.setFail(true)
	h.process(u)
	h.sequence++
	u.ID = h.sequence
	u.Callback.ID = "repeated-save"
	h.wire.setFail(false)
	h.process(u)
	if h.itemCount() != 1 {
		t.Fatal("duplicate creation")
	}
	assertNoKeyboard(t, h.wire.snapshot())
	if !strings.Contains(h.wire.snapshot().Text, "Предмет добавлен") {
		t.Fatal("receipt text replaced by cleanup-only")
	}
}

func TestForeignTapCannotQueueKeyboardCleanup(t *testing.T) {
	h := newHarness(t)
	h.command("/add Паста")
	before := h.wire.snapshot()
	u := h.button("Без резерва")
	u.Callback.From.ID = 999
	h.process(u)
	if !reflect.DeepEqual(before, h.wire.snapshot()) {
		t.Fatal("foreign tap changed keyboard")
	}
	if err := h.db.Transaction(testContext, func(tx *sqlstore.Tx) error {
		rows, err := tx.List(testContext, "delivery")
		if err != nil {
			return err
		}
		for _, raw := range rows {
			var d delivery
			if err = json.Unmarshal(raw, &d); err != nil {
				return err
			}
			if d.ClearOnly {
				return errors.New("foreign callback queued cleanup")
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
