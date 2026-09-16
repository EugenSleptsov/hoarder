package bot

import (
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/EugenSleptsov/hoarder/internal/sqlstore"
	"github.com/EugenSleptsov/hoarder/internal/telegram"
)

func assertNoKeyboard(t *testing.T, msg telegram.Text) {
	t.Helper()
	if msg.Keyboard == nil || len(msg.Keyboard.Rows) != 0 {
		t.Fatalf("keyboard was not explicitly removed: %+v", msg)
	}
}

func TestCompletedAndCancelledDialogsClearKeyboard(t *testing.T) {
	h := newHarness(t)
	h.add("Паста", true, "Одна", "Полная")
	assertNoKeyboard(t, h.wire.snapshot())
	if !strings.Contains(h.wire.snapshot().Text, "сохранён") {
		t.Fatal("missing completion receipt")
	}
	h.command("/items")
	h.press("Паста")
	h.press("Отмена")
	assertNoKeyboard(t, h.wire.snapshot())
	if h.state("Паста").Revision != 0 {
		t.Fatal("cancel became a stock observation")
	}
}

func TestSupersededQuestionClearsOldMessageButNotNew(t *testing.T) {
	h := newHarness(t)
	h.add("Паста", true, "Одна", "Полная")
	h.command("/items")
	h.press("Паста")
	old := h.button("Нет")
	oldID := old.Callback.Message.ID
	h.command("/items")
	h.press("Паста")
	fresh := h.wire.snapshot()
	if fresh.MessageID == oldID {
		t.Fatal("fixture needs distinct messages")
	}
	assertNoKeyboard(t, h.wire.message(oldID))
	h.process(old)
	if !reflect.DeepEqual(fresh, h.wire.message(fresh.MessageID)) {
		t.Fatal("old tap cleared newer keyboard")
	}
	if h.state("Паста").Revision != 0 {
		t.Fatal("old tap changed inventory")
	}
}

func TestStaleGenerationRetainsCurrentKeyboardInSameMessage(t *testing.T) {
	h := newHarness(t)
	h.add("Паста", true, "Одна", "Полная")
	h.command("/items")
	menuTap := h.button("Паста")
	h.process(menuTap)
	old := h.button("Нет")
	h.process(old)
	fresh := h.wire.snapshot()
	for _, u := range []telegram.Update{menuTap, old} {
		h.sequence++
		u.ID = h.sequence
		u.Callback.ID = "old-" + strconv.FormatInt(h.sequence, 10)
		h.process(u)
		if !reflect.DeepEqual(fresh, h.wire.message(fresh.MessageID)) {
			t.Fatal("stale screen/generation removed current keyboard")
		}
	}
	h.press("50%")
	if h.state("Паста").Revision != 1 {
		t.Fatal("fresh screen unusable")
	}
	assertNoKeyboard(t, h.wire.snapshot())
}

func TestExpiredKeyboardCleanupSurvivesRestartAndNetworkFailure(t *testing.T) {
	h := newHarness(t)
	h.add("Паста", true, "Одна", "Полная")
	h.command("/items")
	h.press("Паста")
	u := h.button("Нет")
	id := u.Callback.Message.ID
	h.now = h.now.Add(8 * 24 * time.Hour)
	h.wire.setFail(true)
	h.process(u)
	if len(h.wire.message(id).Keyboard.Rows) == 0 {
		t.Fatal("fake failed transport cleared keyboard")
	}
	h.reopen()
	h.wire.setFail(false)
	h.now = h.now.Add(2 * time.Second)
	if err := h.service.Flush(testContext, h.api, h.now); err != nil {
		t.Fatal(err)
	}
	assertNoKeyboard(t, h.wire.message(id))
	if h.state("Паста").Revision != 0 {
		t.Fatal("expired observation accepted")
	}
}

func TestControlInvalidationRemovesOutstandingQuestionKeyboard(t *testing.T) {
	h := newHarness(t)
	h.add("Паста", true, "Одна", "Полная")
	h.command("/items")
	h.press("Паста")
	id := h.wire.snapshot().MessageID
	h.command("/manage")
	h.press("Паста")
	h.press("Приостановить опросы")
	assertNoKeyboard(t, h.wire.message(id))
	if !h.state("Паста").Paused {
		t.Fatal("pause not applied")
	}
}

func TestCleanupDoesNotEraseReplacementQueuedBeforeFlush(t *testing.T) {
	h := newHarness(t)
	h.add("Паста", true, "Одна", "Полная")
	h.command("/items")
	h.press("Паста")
	msg := h.wire.snapshot()
	if err := h.db.Transaction(testContext, func(tx *sqlstore.Tx) error {
		var active string
		if err := tx.Get(testContext, "active", h.stateID(tx, "Паста"), &active); err != nil {
			return err
		}
		var v screen
		if err := tx.Get(testContext, "screen", active, &v); err != nil {
			return err
		}
		if err := h.service.retireScreen(testContext, tx, v, h.now); err != nil {
			return err
		}
		return h.service.menu(testContext, tx, "all", 0, nil, msg.MessageID, h.now)
	}); err != nil {
		t.Fatal(err)
	}
	if err := h.service.Flush(testContext, h.api, h.now); err != nil {
		t.Fatal(err)
	}
	if h.wire.message(msg.MessageID).Keyboard == nil || len(h.wire.message(msg.MessageID).Keyboard.Rows) == 0 {
		t.Fatal("cleanup removed replacement")
	}
}

// Do not call harness.state inside an existing transaction (single connection).
func (h *harness) stateID(tx *sqlstore.Tx, name string) string {
	items, err := tx.Items(testContext)
	if err != nil {
		h.t.Fatal(err)
	}
	for _, s := range items {
		if s.Config.Name == name {
			return s.Config.ID
		}
	}
	h.t.Fatal("item missing")
	return ""
}
