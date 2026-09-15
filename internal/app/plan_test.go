package app

import (
	"reflect"
	"testing"
	"time"

	"github.com/EugenSleptsov/hoarder/internal/forecast"
	"github.com/EugenSleptsov/hoarder/internal/item"
	"github.com/EugenSleptsov/hoarder/internal/schedule"
)

func TestIndependentPlansShareOnlyTheDailyWindow(t *testing.T) {
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	daily, err := schedule.New("Europe/Berlin", 21, 0)
	if err != nil {
		t.Fatal(err)
	}
	a, err := item.New(item.Config{ID: "a", Name: "A", WithReserve: true, ReserveUnits: 1, LeadDays: 3, MaxCheckDays: 60},
		item.Interval{Low: 2, High: 2}, item.Interval{Low: 0.03, High: 0.04}, now)
	if err != nil {
		t.Fatal(err)
	}
	first, err := PlanItem(a, forecast.Baseline{}, daily, now)
	if err != nil {
		t.Fatal(err)
	}
	b := a
	b.Config.ID = "b"
	b.Rate = item.Interval{Low: 1, High: 2}
	if _, err = PlanItem(b, forecast.Baseline{}, daily, now); err != nil {
		t.Fatal(err)
	}
	again, err := PlanItem(a, forecast.Baseline{}, daily, now)
	if err != nil || !reflect.DeepEqual(first, again) {
		t.Fatal("cross-item influence", err)
	}
	loc, _ := time.LoadLocation("Europe/Berlin")
	if first.SendAt.In(loc).Hour() != 21 || first.SendAt.After(first.RequestedDeadline) {
		t.Fatal(first)
	}
}

func TestPlannerRejectsMissingDependencies(t *testing.T) {
	if _, err := PlanItem(item.State{}, nil, schedule.Daily{}, time.Time{}); err == nil {
		t.Fatal("nil dependency accepted")
	}
}
