package item

import (
	"math"
	"reflect"
	"testing"
	"time"
)

func TestSnapshotRateUsesBothIntervalEndpoints(t *testing.T) {
	s := fixture(t)
	n, err := s.Apply(Event{ID: "learn", ItemID: "paste", Kind: Snapshot,
		At: s.AsOf.Add(10 * 24 * time.Hour), Quantity: Interval{1.4, 1.6}, HistoryComplete: true})
	if err != nil {
		t.Fatal(err)
	}
	// Two packages -> [1.4, 1.6] over ten days means [0.04, 0.06]
	// packages/day. Baseline v1 weights that interval by 0.35. These
	// hand-computed expectations do not call the predictor or reducer again.
	if math.Abs(n.Rate.Low-0.0335) > 1e-12 || math.Abs(n.Rate.High-0.047) > 1e-12 {
		t.Fatalf("learned rate = %+v; want [0.0335, 0.047]", n.Rate)
	}
	if n.Samples != 1 || n.Config != s.Config || n.Anchor.Stock != (Interval{1.4, 1.6}) {
		t.Fatalf("learning changed the wrong evidence/configuration: %+v", n)
	}
}

func TestNoUseRejectsUnconfirmedHistoryWithoutMutation(t *testing.T) {
	s := fixture(t)
	n, err := s.Apply(Event{ID: "no-use", ItemID: "paste", Kind: NoUse,
		At: s.AsOf.Add(24 * time.Hour), From: s.AsOf, HistoryComplete: false})
	if err == nil || !reflect.DeepEqual(n, s) {
		t.Fatalf("unconfirmed no-use accepted or mutated state: error=%v, state=%+v", err, n)
	}
}
