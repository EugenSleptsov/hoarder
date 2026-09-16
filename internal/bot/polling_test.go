package bot

import (
	"reflect"
	"testing"

	"github.com/EugenSleptsov/hoarder/internal/telegram"
)

func TestPollingResetReacknowledgesReceiptWithoutAnotherMutation(t *testing.T) {
	h := newHarness(t)
	h.command("/add Паста")
	h.press("Без резерва")
	h.press("Сейчас нет")
	save := h.press("Добавить")
	state := h.state("Паста")
	plan := h.plan(state.Config.ID)
	if err := h.service.BeginPolling(testContext); err != nil {
		t.Fatal(err)
	}
	if offset, err := h.service.Offset(testContext); err != nil || offset != 0 {
		t.Fatal(offset, err)
	}
	if _, err := h.service.Handle(testContext, save, h.now); err != nil {
		t.Fatal(err)
	}
	if offset, err := h.service.Offset(testContext); err != nil || offset != save.ID+1 {
		t.Fatal(offset, err)
	}
	if !reflect.DeepEqual(state, h.state("Паста")) || !reflect.DeepEqual(plan, h.plan(state.Config.ID)) {
		t.Fatal("receipt replay mutated item or plan")
	}
}

func TestEmptyPollingResponseCannotResetConcurrentlyAdvancedCursor(t *testing.T) {
	h := newHarness(t)
	h.command("/items")
	offset, err := h.service.Offset(testContext)
	if err != nil {
		t.Fatal(err)
	}
	h.sequence++
	if _, err = h.service.Handle(testContext, telegram.Update{ID: h.sequence}, h.now); err != nil {
		t.Fatal(err)
	}
	if err = h.service.CompleteEmptyPoll(testContext, offset); err != nil {
		t.Fatal(err)
	}
	latest, err := h.service.Offset(testContext)
	if err != nil || latest != h.sequence+1 {
		t.Fatalf("lost newer cursor: %d %v", latest, err)
	}
	if err = h.service.CompleteEmptyPoll(testContext, latest); err != nil {
		t.Fatal(err)
	}
	if n, err := h.service.Offset(testContext); err != nil || n != 0 {
		t.Fatal(n, err)
	}
}
