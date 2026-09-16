package forecast

import (
	"testing"
	"time"

	"github.com/EugenSleptsov/hoarder/internal/item"
)

func TestSafetyDeadlineUsesLowerStockAndUpperRate(t *testing.T) {
	s := fixture(t, true)
	s.Stock = item.Interval{Low: 1, High: 3}
	s.Anchor.Stock = s.Stock
	s.Rate = item.Interval{Low: .1, High: .2}
	s.Config.LeadDays = 1
	d, err := (Baseline{}).Predict(s, s.AsOf)
	if err != nil {
		t.Fatal(err)
	}
	// The scarce/high-use case lasts five days; one day is needed to buy.
	// A midpoint-only or optimistic estimate would postpone this deadline.
	want := s.AsOf.Add(4 * 24 * time.Hour)
	if !d.NextCheckAt.Equal(want) || d.Reason != "possible_exhaustion_before_purchase" || d.Due {
		t.Fatalf("decision = %+v; want safety check at %s", d, want)
	}
}

func TestDecisionActionDistinguishesWaitCheckAndReplenishment(t *testing.T) {
	for _, tc := range []struct {
		name     string
		day      int
		maxGap   float64
		want     Action
		due      bool
		shortage bool
	}{
		{"wait", 0, 60, Wait, false, false},
		{"evidence_check", 5, 5, CheckStock, true, false},
		{"reserve_boundary", 30, 60, CheckReplenishment, true, false},
		{"empty", 60, 60, CheckReplenishment, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := fixture(t, true)
			s.Config.MaxCheckDays = tc.maxGap
			d, err := (Baseline{}).Predict(s, s.AsOf.Add(time.Duration(tc.day)*24*time.Hour))
			if err != nil || d.Action != tc.want || d.Due != tc.due || d.PossibleShortageBeforePurchase != tc.shortage {
				t.Fatalf("decision=%+v, error=%v; want action=%s due=%v shortage=%v", d, err, tc.want, tc.due, tc.shortage)
			}
		})
	}
}
