package bot

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/EugenSleptsov/hoarder/internal/item"
	"github.com/EugenSleptsov/hoarder/internal/sqlstore"
	"github.com/EugenSleptsov/hoarder/internal/telegram"
)

func TestCallbackOnboardingSurvivesRestart(t *testing.T) {
	h := newHarness(t)
	h.command("/start")
	h.press("Добавить предмет")
	h.command("Паста")
	h.press("С резервом")
	h.press("Около месяца")
	h.press("Одна")
	h.reopen()
	h.press("50%")
	state := h.state("Паста")
	if state.Stock != (item.Interval{Low: 1.375, High: 1.625}) || state.Config.ReserveUnits != 1 || state.Revision != 0 {
		t.Fatal(state)
	}
	if h.wire.ackCount() != 5 || h.wire.editCount() < 4 {
		t.Fatal("callbacks not acknowledged/edited", h.wire.ackCount(), h.wire.editCount())
	}
	if e := h.db.Transaction(testContext, func(tx *sqlstore.Tx) error {
		replay, e := tx.Replay(testContext, state.Config.ID)
		if e == nil && !reflect.DeepEqual(replay, state) {
			t.Fatal("replay mismatch")
		}
		return e
	}); e != nil {
		t.Fatal(e)
	}
}
func TestDuplicateStaleAndUnauthorisedButtons(t *testing.T) {
	h := newHarness(t)
	h.add("Паста", true, "Одна", "Полная")
	h.command("/items")
	h.press("Паста")
	h.press("Нет")
	u := h.press("50%")
	state := h.state("Паста")
	h.process(u)
	h.sequence++
	u.ID = h.sequence
	h.process(u)
	h.sequence++
	u.ID = h.sequence
	u.Callback.ID = strconv.FormatInt(h.sequence, 10)
	notice := h.process(u)
	if notice == "Ответ принят." {
		t.Fatal("stale button accepted")
	}
	h.command("/items")
	h.press("Паста")
	forged := h.button("Не знаю / не смотрел")
	forged.Callback.From.ID = 999
	if notice = h.process(forged); !strings.Contains(notice, "Нет доступа") {
		t.Fatal(notice)
	}
	if !reflect.DeepEqual(h.state("Паста"), state) {
		t.Fatal("repeated or unauthorised mutation")
	}
	offset, e := h.service.Offset(testContext)
	if e != nil || offset != h.sequence+1 {
		t.Fatal(offset, e)
	}
}
func TestUnknownKeepsDepletingAndOtherItemPlanIsUnchanged(t *testing.T) {
	h := newHarness(t)
	h.add("Паста", true, "Одна", "Полная")
	h.add("Бумага", true, "Одна", "Полная")
	before := h.state("Паста")
	other := h.state("Бумага")
	plan := h.plan(other.Config.ID)
	h.now = h.now.Add(4 * 24 * time.Hour)
	h.command("/items")
	h.press("Паста")
	h.press("Не знаю / не смотрел")
	after := h.state("Паста")
	if !after.Anchor.At.Equal(before.Anchor.At) || after.Rate != before.Rate || after.Stock.High >= before.Stock.High || after.Config.ReserveUnits != 1 {
		t.Fatal(after)
	}
	if !reflect.DeepEqual(plan, h.plan(other.Config.ID)) || !reflect.DeepEqual(other, h.state("Бумага")) {
		t.Fatal("cross-item coupling")
	}
}
func TestBoughtMeansSnapshotNotInventedAddition(t *testing.T) {
	h := newHarness(t)
	h.add("Паста", true, "Нет", "25%")
	h.now = h.now.Add(24 * time.Hour)
	h.command("/shopping")
	h.press("Уже купили: Паста")
	h.press("Одна")
	h.press("50%")
	state := h.state("Паста")
	if state.Stock != (item.Interval{Low: 1.375, High: 1.625}) || state.Added != 0 || state.Samples != 0 || state.Config.ReserveUnits != 1 || state.Revision != 1 {
		t.Fatal(state)
	}
	h.command("/shopping")
	if !strings.Contains(h.wire.snapshot().Text, ": 0") {
		t.Fatal("purchase intent not resolved")
	}
}
func TestNoUseIsExplicitAndDoesNotSetFutureRateToZero(t *testing.T) {
	h := newHarness(t)
	h.add("Мыло", false, "Нет", "50%")
	before := h.state("Мыло")
	h.now = h.now.Add(3 * 24 * time.Hour)
	h.command("/items")
	h.press("Мыло")
	h.press("Не использовали")
	if h.state("Мыло").Revision != 0 {
		t.Fatal("unconfirmed no-use applied")
	}
	h.press("Подтверждаю")
	after := h.state("Мыло")
	if after.Stock != before.Stock || after.Rate != before.Rate || !after.AsOf.Equal(h.now) {
		t.Fatal(after)
	}
}
func TestDailyDigestIncludesAllItemsWithoutMovingTheirPlans(t *testing.T) {
	h := newHarness(t)
	for i := 0; i < 10; i++ {
		h.add(fmt.Sprintf("Предмет %02d", i), false, "Нет", "Пусто")
	}
	h.now = time.Date(2026, 9, 15, 19, 0, 0, 0, time.UTC)
	before := h.wire.sendCount()
	for i := 0; i < 2; i++ {
		if e := h.service.Tick(testContext, h.now); e != nil {
			t.Fatal(e)
		}
		if e := h.service.Flush(testContext, h.api, h.now); e != nil {
			t.Fatal(e)
		}
	}
	if h.wire.sendCount() != before+1 {
		t.Fatal("duplicate digest", h.wire.sendCount(), before)
	}
	var daily session
	if e := h.db.Transaction(testContext, func(tx *sqlstore.Tx) error { return tx.Get(testContext, "session", "2026-09-15", &daily) }); e != nil {
		t.Fatal(e)
	}
	if len(daily.ItemIDs) != 10 {
		t.Fatal("global cap", daily)
	}
	h.press("Дальше")
	itemButtons := 0
	for _, row := range h.wire.snapshot().Keyboard.Rows {
		for _, button := range row {
			if strings.HasPrefix(button.Text, "Предмет ") {
				itemButtons++
			}
		}
	}
	if itemButtons != 2 {
		t.Fatal("pagination lost items", itemButtons)
	}
	h.reopen()
	if e := h.service.Tick(testContext, h.now); e != nil {
		t.Fatal(e)
	}
	if e := h.service.Flush(testContext, h.api, h.now); e != nil {
		t.Fatal(e)
	}
	if h.wire.sendCount() != before+1 {
		t.Fatal("restart duplicated digest")
	}
}
func TestNoNightRetryAndNextDayCatchUp(t *testing.T) {
	h := newHarness(t)
	h.add("Паста", true, "Нет", "Пусто")
	h.now = time.Date(2026, 9, 15, 19, 0, 0, 0, time.UTC)
	before := h.wire.sendCount()
	if e := h.service.Tick(testContext, h.now); e != nil {
		t.Fatal(e)
	}
	h.wire.setFail(true)
	if e := h.service.Flush(testContext, h.api, h.now); e != nil {
		t.Fatal(e)
	}
	h.wire.setFail(false)
	h.now = h.now.Add(31 * time.Minute)
	h.reopen()
	if e := h.service.Flush(testContext, h.api, h.now); e != nil {
		t.Fatal(e)
	}
	if h.wire.sendCount() != before {
		t.Fatal("late proactive delivery")
	}
	h.now = time.Date(2026, 9, 16, 19, 0, 0, 0, time.UTC)
	if e := h.service.Tick(testContext, h.now); e != nil {
		t.Fatal(e)
	}
	if e := h.service.Flush(testContext, h.api, h.now); e != nil {
		t.Fatal(e)
	}
	if h.wire.sendCount() != before+1 {
		t.Fatal("overdue item lost")
	}
}
func TestEmptyDayIsSilentAndExpiredButtonsAreRejected(t *testing.T) {
	h := newHarness(t)
	h.now = time.Date(2026, 9, 15, 19, 0, 0, 0, time.UTC)
	if e := h.service.Tick(testContext, h.now); e != nil {
		t.Fatal(e)
	}
	if e := h.service.Flush(testContext, h.api, h.now); e != nil {
		t.Fatal(e)
	}
	if h.wire.sendCount() != 0 {
		t.Fatal("empty digest")
	}
	h.command("/add Мыло")
	u := h.button("Без резерва")
	h.now = h.now.Add(8 * 24 * time.Hour)
	if notice := h.process(u); !strings.Contains(notice, "устарела") {
		t.Fatal(notice)
	}
}
func TestCallbackRecoversAcceptedSendBeforeMessageIDWasStored(t *testing.T) {
	h := newHarness(t)
	h.sequence++
	_, e := h.service.Handle(testContext, telegram.Update{ID: h.sequence, Message: &telegram.Message{Chat: telegram.Chat{ID: 123, Type: "private"}, Text: "/add Паста"}}, h.now)
	if e != nil {
		t.Fatal(e)
	}
	var v screen
	if e = h.db.Transaction(testContext, func(tx *sqlstore.Tx) error {
		rows, e := tx.List(testContext, "screen")
		if e != nil {
			return e
		}
		return json.Unmarshal(rows[0], &v)
	}); e != nil {
		t.Fatal(e)
	}
	h.sequence++
	u := telegram.Update{ID: h.sequence, Callback: &telegram.Callback{ID: "recovered", From: telegram.User{ID: 123}, Message: &telegram.Message{ID: 777, Date: h.now.Unix(), Chat: telegram.Chat{ID: 123, Type: "private"}}, Data: v.Dialog.Data("yes")}}
	h.process(u)
	if h.wire.sendCount() != 0 || h.wire.editCount() != 1 || h.wire.snapshot().MessageID != 777 {
		t.Fatal("message recovery failed")
	}
}
func TestStoredScheduleCannotSilentlyChange(t *testing.T) {
	h := newHarness(t)
	changed := testSchedule
	changed.Hour = 20
	if _, e := New(testContext, h.db, changed); e == nil {
		t.Fatal("silently changed persisted item deadlines")
	}
}
