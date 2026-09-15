package forecast

import (
	"reflect"
	"testing"
	"time"

	"github.com/EugenSleptsov/hoarder/internal/item"
)

func fixture(t *testing.T, reserve bool) item.State {
	t.Helper()
	units, stock := 0.0, 1.0
	if reserve {
		units, stock = 1, 2
	}
	s, err := item.New(item.Config{ID: "paste", Name: "Paste", WithReserve: reserve, ReserveUnits: units, LeadDays: 3, MaxCheckDays: 60},
		item.Interval{Low: stock, High: stock}, item.Interval{Low: 1.0 / 30, High: 1.0 / 30}, time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestReserveIsLeewayNotTheExhaustionTarget(t *testing.T) {
	s := fixture(t, true)
	d, err := (Baseline{}).Predict(s, s.AsOf)
	if err != nil {
		t.Fatal(err)
	}
	if !d.NextCheckAt.Equal(s.AsOf.Add(30*24*time.Hour)) || d.Due {
		t.Fatal(d)
	}
	late, err := (Baseline{}).Predict(s, s.AsOf.Add(37*24*time.Hour))
	if err != nil || !late.Due || late.Stock.Low <= 0 || s.Config.ReserveUnits != 1 {
		t.Fatal(late, err)
	}
}

func TestNoReserveAllowsTimeForPurchase(t *testing.T) {
	s := fixture(t, false)
	d, err := (Baseline{}).Predict(s, s.AsOf)
	if err != nil || !d.NextCheckAt.Equal(s.AsOf.Add(27*24*time.Hour)) {
		t.Fatal(d, err)
	}
}

func TestRepeatedEvaluationDoesNotMoveDeadline(t *testing.T) {
	s := fixture(t, true)
	first, _ := (Baseline{}).Predict(s, s.AsOf)
	for day := 1; day < 70; day++ {
		d, err := (Baseline{}).Predict(s, s.AsOf.Add(time.Duration(day)*24*time.Hour))
		if err != nil || !d.NextCheckAt.Equal(first.NextCheckAt) {
			t.Fatal(day, d, err)
		}
	}
}

func TestOtherItemsCannotAffectForecast(t *testing.T) {
	a := fixture(t, true)
	before, _ := (Baseline{}).Predict(a, a.AsOf)
	b := fixture(t, false)
	b.Config.ID = "paper"
	b.Rate = item.Interval{Low: 0.1, High: 0.4}
	if _, err := (Baseline{}).Predict(b, b.AsOf.Add(20*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	after, _ := (Baseline{}).Predict(a, a.AsOf)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("cross-item coupling")
	}
}

func TestUnknownCannotResetMaximumObservationGap(t *testing.T) {
	s := fixture(t, true)
	s.Rate = item.Interval{Low: 0.001, High: 0.002}
	s.Config.MaxCheckDays = 10
	first, _ := (Baseline{}).Predict(s, s.AsOf)
	n, err := s.Apply(item.Event{ID: "unknown", ItemID: s.Config.ID, Kind: item.Unknown, At: s.AsOf.Add(9 * 24 * time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	after, _ := (Baseline{}).Predict(n, n.AsOf)
	if !first.NextCheckAt.Equal(after.NextCheckAt) {
		t.Fatal("unknown postponed the evidence cap")
	}
}

func TestFasterPlausibleConsumptionBringsCheckForward(t *testing.T) {
	s := fixture(t, true)
	before, _ := (Baseline{}).Predict(s, s.AsOf)
	s.Rate.High = 0.2
	after, _ := (Baseline{}).Predict(s, s.AsOf)
	if !after.NextCheckAt.Before(before.NextCheckAt) || s.Config.ReserveUnits != 1 {
		t.Fatal(after)
	}
}

func TestExtremeRateCannotOverflowDeadline(t *testing.T) {
	s := fixture(t, true)
	s.Rate = item.Interval{Low: 0, High: 1e-300}
	d, err := (Baseline{}).Predict(s, s.AsOf)
	if err != nil || !d.NextCheckAt.Equal(s.AsOf.Add(60*24*time.Hour)) {
		t.Fatal(d, err)
	}
}
