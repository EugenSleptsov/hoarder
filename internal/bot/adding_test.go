package bot

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/EugenSleptsov/hoarder/internal/dialog"
	"github.com/EugenSleptsov/hoarder/internal/item"
	"github.com/EugenSleptsov/hoarder/internal/sqlstore"
)

func (h *harness) itemCount() int {
	h.t.Helper()
	n := 0
	if err := h.db.Transaction(testContext, func(tx *sqlstore.Tx) error { items, err := tx.Items(testContext); n = len(items); return err }); err != nil {
		h.t.Fatal(err)
	}
	return n
}

func TestPresetNameQuickAddAndExplicitConfirmation(t *testing.T) {
	h := newHarness(t)
	h.command("/add")
	id := h.wire.snapshot().MessageID
	h.press("Зубная паста")
	h.press("С резервом")
	h.press("Одна полная упаковка")
	if h.itemCount() != 0 {
		t.Fatal("created before confirmation")
	}
	h.press("Добавить")
	got := h.state("Зубная паста")
	if got.Stock != (item.Interval{Low: .875, High: 1}) || got.Config.ReserveUnits != 1 || got.Samples != 0 {
		t.Fatal(got)
	}
	if h.wire.snapshot().MessageID != id {
		t.Fatal("unnecessary messages during adding")
	}
	assertNoKeyboard(t, h.wire.snapshot())
}

func TestTextNameReusesPromptAndInvalidatesCancelButton(t *testing.T) {
	h := newHarness(t)
	h.command("/add")
	oldCancel := h.button("Отмена")
	id := h.wire.snapshot().MessageID
	h.command("Зубная нить")
	if h.wire.snapshot().MessageID != id {
		t.Fatal("name prompt was left behind")
	}
	h.process(oldCancel)
	h.press("Без резерва")
	h.press("Сейчас нет")
	h.press("Добавить")
	if h.itemCount() != 1 {
		t.Fatal("stale cancel aborted the new dialog")
	}
}

func TestBatchSkipsDuplicatesAndContinuesAfterEachItem(t *testing.T) {
	h := newHarness(t)
	h.add("Паста", true, "Одна", "Полная")
	before := h.state("Паста")
	plan := h.plan(before.Config.ID)
	h.command("/add Паста\nБумага\nбумага\n\nМыло")
	if !strings.Contains(h.wire.snapshot().Text, "1 из 2") {
		t.Fatal(h.wire.snapshot().Text)
	}
	h.press("С резервом")
	h.press("Полная + одна запасная")
	firstMessage := h.wire.snapshot().MessageID
	h.press("Добавить")
	assertNoKeyboard(t, h.wire.message(firstMessage))
	if !strings.Contains(h.wire.snapshot().Text, "Мыло") {
		t.Fatal("did not advance queue")
	}
	h.press("Без резерва")
	h.press("Осталось примерно 50%")
	h.press("Добавить")
	if h.itemCount() != 3 || !reflect.DeepEqual(before, h.state("Паста")) || !reflect.DeepEqual(plan, h.plan(before.Config.ID)) {
		t.Fatal("batch duplicated or modified existing item")
	}
}

func TestAddingResumeAfterRestartPreservesChoicesAndCleansOldScreen(t *testing.T) {
	h := newHarness(t)
	h.command("/add Паста")
	h.press("С резервом")
	h.press("Одна полная упаковка")
	old := h.button("Добавить")
	oldID := old.Callback.Message.ID
	h.reopen()
	h.command("/add")
	assertNoKeyboard(t, h.wire.message(oldID))
	h.process(old)
	if h.itemCount() != 0 {
		t.Fatal("superseded confirmation accepted")
	}
	h.press("Уточнить срок расхода")
	h.press("Около 3 месяцев")
	h.press("Добавить")
	if h.itemCount() != 1 || h.state("Паста").Rate.High != 1.5/90 {
		t.Fatal("resume lost choices")
	}
}

func TestBatchCancelCurrentVersusCancelWholeQueue(t *testing.T) {
	h := newHarness(t)
	h.command("/add Паста\nБумага\nМыло")
	h.press("Отмена")
	if !strings.Contains(h.wire.snapshot().Text, "Бумага") || h.itemCount() != 0 {
		t.Fatal("single cancel aborted whole queue")
	}
	h.press("Без резерва")
	h.press("Сейчас нет")
	h.press("Добавить")
	old := h.button("С резервом")
	h.command("/cancel")
	assertNoKeyboard(t, h.wire.snapshot())
	h.process(old)
	if h.itemCount() != 1 || h.state("Бумага").Stock.High != 0 {
		t.Fatal("cancel removed confirmed items")
	}
	h.command("/add Мыло")
	h.press("Без резерва")
	h.press("Сейчас нет")
	h.press("Добавить")
	if h.itemCount() != 2 {
		t.Fatal("cancel failed to release adding flow")
	}
}

func TestQuickSaveAfterLongPauseRequiresFreshStock(t *testing.T) {
	h := newHarness(t)
	h.command("/add Паста")
	h.press("С резервом")
	h.press("Полная + одна запасная")
	h.now = h.now.Add(16 * time.Minute)
	h.press("Добавить")
	if h.itemCount() != 0 {
		t.Fatal("saved stale snapshot as current")
	}
	h.press("Сейчас нет")
	h.press("Добавить")
	if h.state("Паста").Stock.High != 0 {
		t.Fatal("fresh check not used")
	}
}

func TestLegacyWizardStillFinishes(t *testing.T) {
	h := newHarness(t)
	if err := h.db.Transaction(testContext, func(tx *sqlstore.Tx) error {
		d, err := dialog.New("aaaaaaaaaaaaaaaaaaaaaaaa", "legacy", "Legacy", true, false, 0, time.Time{}, time.Time{})
		if err != nil {
			return err
		}
		return h.service.saveNewDialog(testContext, tx, d, 0, h.now)
	}); err != nil {
		t.Fatal(err)
	}
	if err := h.service.Flush(testContext, h.api, h.now); err != nil {
		t.Fatal(err)
	}
	h.press("Без резерва")
	h.press("Около месяца")
	h.press("Нет")
	h.press("50%")
	if h.itemCount() != 1 {
		t.Fatal("legacy wizard unreadable")
	}
	assertNoKeyboard(t, h.wire.snapshot())
}

func TestNamesValidationIsAtomic(t *testing.T) {
	for _, bad := range []string{"", strings.Repeat("а", 81), "Паста\nМыло\x00", strings.Repeat("x", 10001), "Паста\n/cancel"} {
		if _, err := parseNames(bad); err == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
	names, err := parseNames("  Паста \r\nпаста\n Мыло ")
	if err != nil || !reflect.DeepEqual(names, []string{"Паста", "Мыло"}) {
		t.Fatal(names, err)
	}
	many := []string{}
	for i := 0; i < 21; i++ {
		many = append(many, strings.Repeat("а", i+1))
	}
	if _, err := parseNames(strings.Join(many, "\n")); err == nil {
		t.Fatal("unbounded batch")
	}
	h := newHarness(t)
	h.command("/add Паста\nМыло\x00")
	if h.itemCount() != 0 {
		t.Fatal("partially applied malformed batch")
	}
}

func TestConfirmRechecksDuplicateName(t *testing.T) {
	h := newHarness(t)
	h.command("/add Паста")
	h.press("С резервом")
	h.press("Сейчас нет")
	if err := h.db.Transaction(testContext, func(tx *sqlstore.Tx) error {
		d, _ := dialog.NewQuick("bbbbbbbbbbbbbbbbbbbbbbbb", "Паста")
		d.Step = dialog.Done
		d.ObservedAt = h.now
		d.High = 1
		d.Low = .875
		v := screen{ID: d.ID, Dialog: &d}
		return h.service.finish(testContext, tx, &v, h.now)
	}); err != nil {
		t.Fatal(err)
	}
	h.press("Добавить")
	if h.itemCount() != 1 || !strings.Contains(h.wire.snapshot().Text, "Дубликат не создан") {
		t.Fatal("duplicate name created")
	}
}
