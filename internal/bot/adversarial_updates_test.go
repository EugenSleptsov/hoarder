package bot

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/EugenSleptsov/hoarder/internal/telegram"
)

func TestAdversarialForeignMessagesAndUnsupportedUpdatesAreDurablyIgnored(t *testing.T) {
	h := newHarness(t)
	h.add("Паста", true, "Одна", "Полная")
	before := h.state("Паста")
	plan := h.plan(before.Config.ID)
	sends := h.wire.sendCount()
	for _, message := range []*telegram.Message{
		{ID: 777, Date: 1, Chat: telegram.Chat{ID: 999, Type: "private"}, Text: "/add Intruder"},
		{ID: 778, Date: 1, Chat: telegram.Chat{ID: -123, Type: "group"}, Text: "/items"},
		nil,
	} {
		h.sequence++
		u := telegram.Update{ID: h.sequence, Message: message}
		for repeat := 0; repeat < 2; repeat++ {
			if _, err := h.service.Handle(testContext, u, h.now); err != nil {
				t.Fatalf("untrusted/unsupported input poisoned update loop: %v", err)
			}
		}
	}
	offset, err := h.service.Offset(testContext)
	if err != nil || offset != h.sequence+1 {
		t.Fatalf("ignored inputs did not advance cursor: %d %v", offset, err)
	}
	if !reflect.DeepEqual(before, h.state("Паста")) || !reflect.DeepEqual(plan, h.plan(before.Config.ID)) {
		t.Fatal("unauthorised input changed item")
	}
	if err = h.service.Flush(testContext, h.api, h.now); err != nil {
		t.Fatal(err)
	}
	if h.wire.sendCount() != sends {
		t.Fatal("replied to unauthorised input")
	}
	h.command("/items")
	h.press("Паста")
}

func TestAdversarialMalformedProtocolCannotRetireKnownScreen(t *testing.T) {
	h := newHarness(t)
	h.command("/add Паста")
	u := h.button("С резервом")
	id := strings.Split(u.Callback.Data, ":")[1]
	h.now = h.now.Add(8 * 24 * time.Hour)
	// Even after expiry an unknown callback protocol is not authority to edit.
	u.Callback.Data = "garbage:" + id + ":0"
	before := h.wire.snapshot()
	h.process(u)
	if !reflect.DeepEqual(before, h.wire.snapshot()) {
		t.Fatal("unknown callback protocol erased a known message")
	}
}
