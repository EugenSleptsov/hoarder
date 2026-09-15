package item

import (
	"math"
	"reflect"
	"testing"
	"time"
)

func fixture(t *testing.T) State {
	t.Helper()
	s, err := New(Config{ID: "paste", Name: "Paste", WithReserve: true, ReserveUnits: 1, LeadDays: 3, MaxCheckDays: 60},
		Interval{2, 2}, Interval{0.03, 0.04}, time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestAdditionIsNotResetAndIsIdempotent(t *testing.T) {
	s := fixture(t)
	e := Event{ID: "purchase-1", ItemID: "paste", Kind: Addition, At: s.AsOf.Add(10 * 24 * time.Hour), Quantity: Interval{1, 1}}
	n, err := s.Apply(e)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(n.Stock.Low-2.6) > 1e-9 || math.Abs(n.Stock.High-2.7) > 1e-9 {
		t.Fatal(n.Stock)
	}
	again, err := n.Apply(e)
	if err != nil || !reflect.DeepEqual(again, n) {
		t.Fatal("duplicate changed state", err)
	}
	if len(s.Receipts) != 0 || s.Revision != 0 {
		t.Fatal("input mutated")
	}
	e.Quantity = Interval{2, 2}
	if _, err = n.Apply(e); err == nil {
		t.Fatal("conflicting duplicate accepted")
	}
}

func TestUnknownDoesNotStopConsumption(t *testing.T) {
	s := fixture(t)
	at := s.AsOf.Add(10 * 24 * time.Hour)
	n, err := s.Apply(Event{ID: "unknown", ItemID: "paste", Kind: Unknown, At: at})
	if err != nil {
		t.Fatal(err)
	}
	want, _ := s.Project(at)
	if n.Stock != want || n.Rate != s.Rate || n.Anchor != s.Anchor || n.Samples != 0 {
		t.Fatal(n)
	}
	later, _ := n.Project(at.Add(24 * time.Hour))
	if later.High >= n.Stock.High {
		t.Fatal("silence froze stock")
	}
}

func TestUnreportedPurchaseReanchorsWithoutLearning(t *testing.T) {
	s := fixture(t)
	n, err := s.Apply(Event{ID: "snapshot", ItemID: "paste", Kind: Snapshot, At: s.AsOf.Add(30 * 24 * time.Hour), Quantity: Interval{2, 2}})
	if err != nil {
		t.Fatal(err)
	}
	if n.Rate != s.Rate || n.Samples != 0 || n.Stock != (Interval{2, 2}) {
		t.Fatal(n)
	}
}

func TestCompleteObservationLearnsAndReserveNeverChanges(t *testing.T) {
	s := fixture(t)
	n, err := s.Apply(Event{ID: "snapshot", ItemID: "paste", Kind: Snapshot, At: s.AsOf.Add(10 * 24 * time.Hour), Quantity: Interval{1.4, 1.6}, HistoryComplete: true})
	if err != nil {
		t.Fatal(err)
	}
	if n.Samples != 1 || n.Rate == s.Rate || n.Config != s.Config {
		t.Fatal(n)
	}
}

func TestEmptyStockIsCensoredNotExactDepletionTime(t *testing.T) {
	s := fixture(t)
	n, err := s.Apply(Event{ID: "empty", ItemID: "paste", Kind: Snapshot, At: s.AsOf.Add(90 * 24 * time.Hour), Quantity: Interval{0, 0}, HistoryComplete: true})
	if err != nil {
		t.Fatal(err)
	}
	if n.Rate != s.Rate || n.Samples != 0 {
		t.Fatal("used reply time as exact exhaustion time")
	}
}

func TestKnownAdditionIntervalDoesNotAssumeContinuousAvailability(t *testing.T) {
	s := fixture(t)
	n, err := s.Apply(Event{ID: "add", ItemID: "paste", Kind: Addition, At: s.AsOf.Add(70 * 24 * time.Hour), Quantity: Interval{1, 1}})
	if err != nil {
		t.Fatal(err)
	}
	n, err = n.Apply(Event{ID: "snap", ItemID: "paste", Kind: Snapshot, At: n.AsOf.Add(24 * time.Hour), Quantity: Interval{0.8, 1}, HistoryComplete: true})
	if err != nil || n.Samples != 0 {
		t.Fatal(n, err)
	}
}

func TestConfirmedNoUseRequiresWholeInterval(t *testing.T) {
	s := fixture(t)
	e := Event{ID: "no-use", ItemID: "paste", Kind: NoUse, At: s.AsOf.Add(7 * 24 * time.Hour), From: s.AsOf, HistoryComplete: true}
	n, err := s.Apply(e)
	if err != nil || n.Stock != s.Stock || n.Rate != s.Rate {
		t.Fatal(n, err)
	}
	e.From = e.From.Add(time.Hour)
	if _, err = s.Apply(e); err == nil {
		t.Fatal("partial interval accepted")
	}
}

func TestInvalidInputDoesNotMutateState(t *testing.T) {
	s := fixture(t)
	for _, e := range []Event{
		{ID: "x", ItemID: "other", Kind: Unknown, At: s.AsOf},
		{ID: "x", ItemID: "paste", Kind: Unknown, At: s.AsOf.Add(-time.Hour)},
		{ID: "x", ItemID: "paste", Kind: Snapshot, At: s.AsOf, Quantity: Interval{math.NaN(), 1}},
		{ID: "x", ItemID: "paste", Kind: Addition, At: s.AsOf, Quantity: Interval{0, 1}},
	} {
		n, err := s.Apply(e)
		if err == nil || !reflect.DeepEqual(n, s) {
			t.Fatal("invalid event accepted or mutated input", e, err)
		}
	}
}

func FuzzIntervalValidation(f *testing.F) {
	f.Add(0.0, 1.0)
	f.Add(-1.0, 0.0)
	f.Fuzz(func(t *testing.T, lo, hi float64) {
		v := Interval{lo, hi}
		if v.Validate() == nil && (!finite(lo) || !finite(hi) || lo < 0 || hi < lo) {
			t.Fatal(v)
		}
	})
}
