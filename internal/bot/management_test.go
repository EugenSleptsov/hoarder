package bot

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/EugenSleptsov/hoarder/internal/item"
	"github.com/EugenSleptsov/hoarder/internal/sqlstore"
)

func manage(h *harness, name string) {
	h.t.Helper()
	h.command("/manage")
	label := name
	if h.state(name).Paused {
		label += " (пауза)"
	}
	h.press(label)
}

func sameEvidence(t *testing.T, a, b item.State) {
	t.Helper()
	if a.Stock != b.Stock || a.Rate != b.Rate || a.Anchor != b.Anchor || a.AsOf != b.AsOf || a.LastContact != b.LastContact || a.Added != b.Added || a.Samples != b.Samples {
		t.Fatal("control changed physical evidence")
	}
}

func TestPauseResumePreservesOverduePlanAndOtherItem(t *testing.T) {
	h := newHarness(t)
	h.add("Паста", true, "Нет", "25%")
	h.add("Бумага", true, "Одна", "Полная")
	before, other := h.state("Паста"), h.state("Бумага")
	plan, otherPlan := h.plan(before.Config.ID), h.plan(other.Config.ID)
	h.now = h.now.Add(10 * 24 * time.Hour)
	manage(h, "Паста")
	u := h.press("Приостановить опросы")
	h.process(u)
	paused := h.state("Паста")
	if !paused.Paused || paused.Revision != 1 {
		t.Fatal(paused)
	}
	sameEvidence(t, before, paused)
	p := h.plan(before.Config.ID)
	p.ExpectedRevision = plan.ExpectedRevision
	if !reflect.DeepEqual(p, plan) {
		t.Fatal("pause moved date")
	}
	h.command("/shopping")
	if strings.Contains(h.wire.snapshot().Text, ": 1") {
		t.Fatal("paused shopping prompt")
	}
	h.now = h.now.Add(10 * 24 * time.Hour)
	h.reopen()
	manage(h, "Паста")
	h.press("Возобновить опросы")
	after := h.state("Паста")
	if after.Paused || after.Revision != 2 {
		t.Fatal(after)
	}
	sameEvidence(t, before, after)
	p = h.plan(before.Config.ID)
	p.ExpectedRevision = plan.ExpectedRevision
	if !reflect.DeepEqual(p, plan) || !p.SendAt.Before(h.now) {
		t.Fatal("resume lost overdue date")
	}
	if !reflect.DeepEqual(other, h.state("Бумага")) || !reflect.DeepEqual(otherPlan, h.plan(other.Config.ID)) {
		t.Fatal("cross-item mutation")
	}
	a, _ := after.Project(h.now)
	b, _ := before.Project(h.now)
	if a != b {
		t.Fatal("pause stopped consumption")
	}
}

func TestArchiveConfirmationCancelRestoreAndOldQuestion(t *testing.T) {
	h := newHarness(t)
	h.add("Паста", true, "Одна", "Полная")
	h.command("/items")
	h.press("Паста")
	old := h.button("Не знаю / не смотрел")
	manage(h, "Паста")
	h.press("Убрать в архив")
	if h.state("Паста").Archived {
		t.Fatal("archived before confirmation")
	}
	h.press("Отмена")
	if h.state("Паста").Revision != 0 {
		t.Fatal("cancel changed item")
	}
	h.press("Убрать в архив")
	h.reopen()
	h.press("Подтверждаю архивирование")
	archived := h.state("Паста")
	if !archived.Archived || !archived.Paused {
		t.Fatal(archived)
	}
	if notice := h.process(old); notice == "Ответ принят." {
		t.Fatal("old question accepted")
	}
	h.command("/items")
	if !strings.Contains(h.wire.snapshot().Text, "Предметы: 0") {
		t.Fatal("archive visible in registry")
	}
	h.command("/archive")
	h.press("Паста")
	h.press("Восстановить на паузе")
	restored := h.state("Паста")
	if restored.Archived || !restored.Paused || restored.Revision != 2 {
		t.Fatal(restored)
	}
	sameEvidence(t, archived, restored)
	h.press("Проверить остаток")
	h.press("Нет")
	h.press("50%")
	if !h.state("Паста").Paused {
		t.Fatal("manual check resumed notifications")
	}
}

func TestSettingsAreConfirmedReplayableAndDoNotBuyStock(t *testing.T) {
	h := newHarness(t)
	h.add("Мыло", false, "Нет", "50%")
	h.add("Бумага", true, "Одна", "Полная")
	before, other := h.state("Мыло"), h.state("Бумага")
	otherPlan := h.plan(other.Config.ID)
	manage(h, "Мыло")
	h.press("Настройки")
	h.press("Резерв")
	h.press("Резерв: 1 упаковка")
	if h.state("Мыло").Config.WithReserve {
		t.Fatal("unconfirmed policy")
	}
	h.press("Сохранить настройки")
	for _, pair := range [][2]string{{"Время на покупку", "7 дней"}, {"Интервал проверки", "30 дней"}} {
		h.press("Настройки")
		h.press(pair[0])
		h.press(pair[1])
		h.press("Сохранить настройки")
	}
	h.reopen()
	after := h.state("Мыло")
	if !after.Config.WithReserve || after.Config.ReserveUnits != 1 || after.Config.LeadDays != 7 || after.Config.MaxCheckDays != 30 || after.Revision != 3 {
		t.Fatal(after)
	}
	sameEvidence(t, before, after)
	if !reflect.DeepEqual(other, h.state("Бумага")) || !reflect.DeepEqual(otherPlan, h.plan(other.Config.ID)) {
		t.Fatal("other item affected")
	}
	if err := h.db.Transaction(testContext, func(tx *sqlstore.Tx) error {
		replayed, err := tx.Replay(testContext, after.Config.ID)
		if err == nil && !reflect.DeepEqual(replayed, after) {
			t.Fatal("control replay mismatch")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

func TestStaleSettingsCannotOverwriteNewAnswer(t *testing.T) {
	h := newHarness(t)
	h.add("Мыло", false, "Нет", "50%")
	manage(h, "Мыло")
	h.press("Настройки")
	h.press("Резерв")
	h.press("Резерв: 1 упаковка")
	old := h.button("Сохранить настройки")
	h.command("/items")
	h.press("Мыло")
	h.press("Нет")
	h.press("25%")
	before := h.state("Мыло")
	if notice := h.process(old); !strings.Contains(notice, "изменён") {
		t.Fatal(notice)
	}
	if !reflect.DeepEqual(before, h.state("Мыло")) {
		t.Fatal("stale settings replaced observation")
	}
}

func TestPendingDigestIsCancelledAfterLastItemPaused(t *testing.T) {
	h := newHarness(t)
	h.add("Паста", true, "Нет", "Пусто")
	manage(h, "Паста")
	h.now = time.Date(2026, 9, 15, 19, 0, 0, 0, time.UTC)
	before := h.wire.sendCount()
	if err := h.service.Tick(testContext, h.now); err != nil {
		t.Fatal(err)
	}
	h.press("Приостановить опросы")
	if err := h.service.Flush(testContext, h.api, h.now); err != nil {
		t.Fatal(err)
	}
	if h.wire.sendCount() != before {
		t.Fatal("sent cancelled daily digest")
	}
}

func TestPendingDigestFiltersOneItemWithoutDeferringAnother(t *testing.T) {
	h := newHarness(t)
	h.add("Паста", true, "Нет", "Пусто")
	h.add("Бумага", true, "Нет", "Пусто")
	other := h.state("Бумага")
	otherPlan := h.plan(other.Config.ID)
	manage(h, "Паста")
	h.now = time.Date(2026, 9, 15, 19, 0, 0, 0, time.UTC)
	before := h.wire.sendCount()
	if err := h.service.Tick(testContext, h.now); err != nil {
		t.Fatal(err)
	}
	h.press("Приостановить опросы")
	if err := h.service.Flush(testContext, h.api, h.now); err != nil {
		t.Fatal(err)
	}
	if h.wire.sendCount() != before+1 {
		t.Fatal("other due question not delivered")
	}
	text := h.wire.snapshot()
	if !strings.Contains(text.Text, "Вопросы на сегодня: 1") {
		t.Fatal(text)
	}
	for _, row := range text.Keyboard.Rows {
		for _, b := range row {
			if b.Text == "Паста" {
				t.Fatal("paused item delivered")
			}
		}
	}
	if !reflect.DeepEqual(otherPlan, h.plan(other.Config.ID)) {
		t.Fatal("other item rescheduled")
	}
}

func TestControlAndPlanRollBackTogether(t *testing.T) {
	h := newHarness(t)
	h.add("Паста", true, "Одна", "Полная")
	before := h.state("Паста")
	plan := h.plan(before.Config.ID)
	boom := errors.New("injected failure")
	err := h.db.Transaction(testContext, func(tx *sqlstore.Tx) error {
		a := itemAction("pause", "control", before)
		a.Control = item.Pause
		if _, err := h.service.applyControl(testContext, tx, before, a, "rollback", 0, h.now); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) || !reflect.DeepEqual(before, h.state("Паста")) || !reflect.DeepEqual(plan, h.plan(before.Config.ID)) {
		t.Fatal("partial control commit", err)
	}
}

func TestManagementPermissionsStillApply(t *testing.T) {
	h := newHarness(t)
	h.add("Мыло", false, "Нет", "50%")
	manage(h, "Мыло")
	u := h.button("Приостановить опросы")
	u.Callback.From.ID = 999
	if notice := h.process(u); !strings.Contains(notice, "Нет доступа") {
		t.Fatal(notice)
	}
	if h.state("Мыло").Paused {
		t.Fatal("unauthorised pause")
	}
}
