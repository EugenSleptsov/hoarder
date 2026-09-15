package bot

import (
	"reflect"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/EugenSleptsov/hoarder/internal/telegram"
)

func TestConcurrentCallbacksApplyOnlyOneObservation(t *testing.T) {
	h := newHarness(t)
	h.add("Паста", true, "Одна", "Полная")
	h.command("/items")
	h.press("Паста")
	h.press("Нет")
	updates := []telegram.Update{h.button("25%"), h.button("75%")}
	type outcome struct {
		notice string
		err    error
	}
	results := make(chan outcome, 2)
	var wg sync.WaitGroup
	for _, u := range updates {
		wg.Add(1)
		go func(u telegram.Update) {
			defer wg.Done()
			notice, e := h.service.Handle(testContext, u, h.now)
			results <- outcome{notice, e}
		}(u)
	}
	wg.Wait()
	close(results)
	accepted := 0
	for r := range results {
		if r.err != nil {
			t.Fatal(r.err)
		}
		if r.notice == "Ответ принят." {
			accepted++
		}
	}
	if accepted != 1 || h.state("Паста").Revision != 1 {
		t.Fatal("concurrent stock overwrite", accepted)
	}
	if e := h.service.Flush(testContext, h.api, h.now); e != nil {
		t.Fatal(e)
	}
}

func TestFailedEditDoesNotRollBackOrRepeatAcceptedStock(t *testing.T) {
	h := newHarness(t)
	h.add("Паста", true, "Одна", "Полная")
	h.command("/items")
	h.press("Паста")
	h.press("Нет")
	messageID := h.wire.snapshot().MessageID
	h.wire.setFail(true)
	u := h.press("50%")
	accepted := h.state("Паста")
	if accepted.Revision != 1 {
		t.Fatal("failed send lost the committed answer")
	}
	h.reopen()
	h.wire.setFail(false)
	h.now = h.now.Add(2 * time.Second)
	h.process(u)
	if !reflect.DeepEqual(h.state("Паста"), accepted) || h.wire.snapshot().MessageID != messageID {
		t.Fatal("retry reapplied stock or created a different message")
	}
}

func TestCallbackMessageScopeAndAccessibility(t *testing.T) {
	h := newHarness(t)
	h.add("Паста", true, "Одна", "Полная")
	h.command("/items")
	h.press("Паста")
	before := h.state("Паста")
	for _, mutate := range []func(*telegram.Callback){
		func(c *telegram.Callback) { c.Message = nil },
		func(c *telegram.Callback) { c.Message.Date = 0 },
		func(c *telegram.Callback) { c.Message.Chat.ID = 999 },
		func(c *telegram.Callback) { c.Message.Chat.Type = "group" },
		func(c *telegram.Callback) { c.Message.ID += 999 },
		func(c *telegram.Callback) { c.Data = "m1:0123456789abcdef01234567:0" },
	} {
		u := h.button("Не знаю / не смотрел")
		u.Callback.ID = "rejected-" + strconv.FormatInt(u.ID, 10)
		mutate(u.Callback)
		if notice := h.process(u); notice == "" || notice == "Ответ принят." {
			t.Fatal("invalid callback accepted")
		}
	}
	if !reflect.DeepEqual(h.state("Паста"), before) {
		t.Fatal("rejected button changed stock")
	}
}
