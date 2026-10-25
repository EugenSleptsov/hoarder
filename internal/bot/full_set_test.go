package bot

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/EugenSleptsov/hoarder/internal/item"
)

// addSet adds an item with a full set of four and a reserve of four.
func (h *harness) addSet(name, stock string) {
	h.t.Helper()
	h.command("/add " + name)
	h.press("Много упаковок: набор и резерв")
	h.press("4")
	h.press("4")
	h.press(stock)
	h.press("Добавить")
}

func TestFullSetSizesTheShoppingAmountWithoutResettingStock(t *testing.T) {
	h := newHarness(t)
	h.add("Паста", true, "Одна", "Полная")
	until := h.now.Add(10 * 24 * time.Hour)
	paste := h.needs(until, false)["Паста"]
	h.addSet("Влажная бумага", "Примерно половина набора")
	wipes := h.state("Влажная бумага")
	if wipes.Config.FullSet() != 4 || wipes.Config.ReserveUnits != 4 || !wipes.Config.WithReserve || wipes.Stock != (item.Interval{Low: 1.5, High: 2.5}) {
		t.Fatal(wipes)
	}
	if !reflect.DeepEqual(h.needs(until, false)["Паста"], paste) {
		t.Fatal("another item's set changed this item's need")
	}
	n := h.needs(until, false)["Влажная бумага"]
	fill := *n.UnitsToFullSet
	if !n.NeededByUntil || n.FullSetUnits != 4 || fill <= *n.MinUnitsToCover || n.StockAtUntil.Mid()+float64(fill) < 8 || n.StockAtUntil.Mid()+float64(fill-1) >= 8 {
		t.Fatal("the cart is not sized up to the set plus reserve", n)
	}

	lead := *h.needs(h.now.Add(3*24*time.Hour), false)["Влажная бумага"].UnitsToFullSet
	h.command("/shopping")
	if line := fmt.Sprintf("Влажная бумага: ≈%d уп., до 8 (набор 4 + резерв 4)", lead); !strings.Contains(h.wire.snapshot().Text, line) {
		t.Fatal("shopping list does not show the amount", h.wire.snapshot().Text, line)
	}

	// One unit fewer leaves the projected stock short of the set plus reserve.
	h.deliver(Delivery{Shop: ShopKnuspr, OrderID: "short", DeliveredAt: h.now, Lines: []DeliveryLine{{ItemID: n.ItemID, Units: units(float64(fill - 1))}}})
	short := h.needs(until, false)["Влажная бумага"]
	if short.StockAtUntil.Mid() >= 8 || *short.UnitsToFullSet != 1 && short.NeededByUntil {
		t.Fatal(short)
	}
	h.deliver(Delivery{Shop: ShopKnuspr, OrderID: "rest", DeliveredAt: h.now, Lines: []DeliveryLine{{ItemID: n.ItemID, Units: units(1)}}})
	after := h.state("Влажная бумага")
	if after.Stock.Low != wipes.Stock.Low+float64(fill) || after.Stock.High != wipes.Stock.High+float64(fill) {
		t.Fatal("delivery reset stock instead of adding exactly", after.Stock, wipes.Stock, fill)
	}
	if done := h.needs(until, false)["Влажная бумага"]; done.NeededByUntil || *done.UnitsToFullSet != 0 || done.StockAtUntil.Mid() < 8 {
		t.Fatal(done)
	}
}

func TestStockChecksCountManyClosedPackages(t *testing.T) {
	h := newHarness(t)
	h.addSet("Влажная бумага", "Сейчас нет")
	h.add("Паста", true, "Одна", "Полная")
	h.command("/items")
	h.press("Влажная бумага")
	h.press("Больше 11")
	h.press("13")
	h.press("50%")
	if got := h.state("Влажная бумага").Stock; got != (item.Interval{Low: 13.375, High: 13.625}) {
		t.Fatal(got)
	}
	h.command("/items")
	h.press("Паста")
	h.press("Больше 3")
	h.press("5")
	h.press("Полная")
	if got := h.state("Паста").Stock; got != (item.Interval{Low: 5.875, High: 6}) {
		t.Fatal(got)
	}
}

func TestSetAndReserveSettingsKeepEvidenceAndTheSetKeepsTheDate(t *testing.T) {
	h := newHarness(t)
	h.add("Мыло", true, "Одна", "Полная")
	h.add("Бумага", false, "Одна", "Полная")
	before, other := h.state("Мыло"), h.state("Бумага")
	plan, otherPlan := h.plan(before.Config.ID), h.plan(other.Config.ID)
	manage(h, "Мыло")
	h.press("Настройки")
	h.press("Полный набор")
	h.press("Набор: 4 уп.")
	h.press("Сохранить настройки")
	set := h.state("Мыло")
	if set.Config.FullSet() != 4 || set.Revision != before.Revision+1 {
		t.Fatal(set)
	}
	if got := h.plan(set.Config.ID); !got.SendAt.Equal(plan.SendAt) || got.LocalDate != plan.LocalDate {
		t.Fatal("a full set moved the question date", got, plan)
	}
	h.press("Настройки")
	h.press("Полный набор")
	h.press("Набор: 4 уп.")
	if notice := h.process(h.button("Сохранить настройки")); notice != "Настройки не изменились." || h.state("Мыло").Revision != set.Revision {
		t.Fatal("same set was stored again", notice)
	}
	h.press("Настройки")
	h.press("Резерв")
	h.press("Резерв: 4 уп.")
	h.press("Сохранить настройки")
	if !strings.Contains(h.wire.snapshot().Text, "Полный набор: 4 уп.\nРезерв: 4 уп.") {
		t.Fatal(h.wire.snapshot().Text)
	}
	h.press("Настройки")
	h.press("Полный набор")
	h.press("Набор: 1 упаковка")
	h.press("Сохранить настройки")
	h.reopen()
	after := h.state("Мыло")
	if after.Config.FullUnits != 0 || after.Config.ReserveUnits != 4 || !after.Config.WithReserve {
		t.Fatal(after.Config)
	}
	sameEvidence(t, before, after)
	if !reflect.DeepEqual(other, h.state("Бумага")) || !reflect.DeepEqual(otherPlan, h.plan(other.Config.ID)) {
		t.Fatal("other item affected")
	}
}
