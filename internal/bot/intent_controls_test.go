package bot

import (
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/EugenSleptsov/hoarder/internal/telegram"
)

func TestReserveChangesReconcileRecommendationsWithoutAddingStock(t *testing.T) {
	h := newHarness(t)
	h.add("Мыло", false, "Нет", "50%")
	before := h.state("Мыло")
	for _, choice := range []struct {
		label string
		count string
	}{{"Резерв: 1 упаковка", "1"}, {"Без резерва", "0"}} {
		manage(h, "Мыло")
		h.press("Настройки")
		h.press("Резерв")
		h.press(choice.label)
		h.press("Сохранить настройки")
		sameEvidence(t, before, h.state("Мыло"))
		h.command("/shopping")
		if !strings.Contains(h.wire.snapshot().Text, "Список покупок (рекомендации): "+choice.count) {
			t.Fatal("recommendations did not reflect explicit policy", h.wire.snapshot())
		}
	}
}

func TestIdenticalSettingsLeaveEvidenceRevisionAndPlanUnchanged(t *testing.T) {
	h := newHarness(t)
	h.add("Мыло", false, "Нет", "50%")
	before := h.state("Мыло")
	plan := h.plan(before.Config.ID)
	manage(h, "Мыло")
	h.press("Настройки")
	h.press("Время на покупку")
	h.press("3 дней")
	h.press("Сохранить настройки")
	if !reflect.DeepEqual(before, h.state("Мыло")) || !reflect.DeepEqual(plan, h.plan(before.Config.ID)) {
		t.Fatal("unchanged configuration created an event or moved the deadline")
	}
}

func TestCompetingManagementConfirmationsApplyOnlyOnce(t *testing.T) {
	h := newHarness(t)
	h.add("Мыло", false, "Нет", "50%")
	manage(h, "Мыло")
	first := h.button("Приостановить опросы")
	manage(h, "Мыло")
	second := h.button("Приостановить опросы")
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, u := range []telegram.Update{first, second} {
		wg.Add(1)
		go func(u telegram.Update) {
			defer wg.Done()
			_, err := h.service.Handle(testContext, u, h.now)
			errs <- err
		}(u)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	state := h.state("Мыло")
	if !state.Paused || state.Revision != 1 {
		t.Fatal("competing controls applied twice", state)
	}
}
