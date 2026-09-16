package bot

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestFuturePlansAreNotDueUntilTheirStoredSlot(t *testing.T) {
	h := newHarness(t)
	h.add("Паста", false, "Нет", "Полная")
	before := h.state("Паста")
	plan := h.plan(before.Config.ID)
	if !plan.SendAt.After(h.now.Add(24 * time.Hour)) {
		t.Fatal("fixture needs a future plan", plan)
	}
	h.command("/today")
	if !strings.Contains(h.wire.snapshot().Text, "Вопросы на сегодня: 0") {
		t.Fatal("future item shown as due", h.wire.snapshot().Text)
	}
	h.now = time.Date(2026, 9, 15, 19, 0, 0, 0, 0, time.UTC)
	sends := h.wire.sendCount()
	if err := h.service.Tick(testContext, h.now); err != nil {
		t.Fatal(err)
	}
	if err := h.service.Flush(testContext, h.api, h.now); err != nil {
		t.Fatal(err)
	}
	if h.wire.sendCount() != sends {
		t.Fatal("not-yet-due item triggered an automatic question")
	}
	h.now = plan.SendAt
	if err := h.service.Tick(testContext, h.now); err != nil {
		t.Fatal(err)
	}
	if err := h.service.Flush(testContext, h.api, h.now); err != nil {
		t.Fatal(err)
	}
	if h.wire.sendCount() != sends+1 || !strings.Contains(h.wire.snapshot().Text, "Вопросы на сегодня: 1") {
		t.Fatal("stored due slot did not produce exactly one question")
	}
	if !reflect.DeepEqual(before, h.state("Паста")) || !reflect.DeepEqual(plan, h.plan(before.Config.ID)) {
		t.Fatal("sending a question changed physical evidence or its stored plan")
	}
}

func TestDelayedHistoryKeepsPhysicalObservationTime(t *testing.T) {
	h := newHarness(t)
	h.add("Паста", false, "Нет", "Полная")
	h.now = h.now.Add(2 * 24 * time.Hour)
	h.command("/items")
	h.press("Паста")
	h.press("Нет")
	h.press("50%")
	observed := h.now
	if h.state("Паста").Revision != 0 {
		t.Fatal("partial observation committed before required history clarification")
	}
	h.now = h.now.Add(2 * time.Minute)
	h.press("Не пополняли")
	got := h.state("Паста")
	if !got.AsOf.Equal(observed) || !got.Anchor.At.Equal(observed) || got.Revision != 1 {
		t.Fatalf("history reply time replaced physical check time: %+v; checked at %s", got, observed)
	}
}
