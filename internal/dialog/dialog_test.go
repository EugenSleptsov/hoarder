package dialog

import (
	"errors"
	"testing"
	"time"
)

const testID = "0123456789abcdef01234567"

func start(t *testing.T, creating, reserve bool) State {
	t.Helper()
	s, e := New(testID, "paste", "Паста", creating, reserve, 4, time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC))
	if e != nil {
		t.Fatal(e)
	}
	return s
}
func tap(t *testing.T, s State, a string, now time.Time) State {
	t.Helper()
	n, e := s.Apply(s.Generation, a, now)
	if e != nil {
		t.Fatal(e)
	}
	return n
}
func TestOnboardingAndPayloads(t *testing.T) {
	now := time.Date(2026, 9, 15, 21, 0, 0, 0, time.UTC)
	s := start(t, true, false)
	for _, a := range []string{"yes", "d30", "c1", "p50"} {
		for _, row := range s.Buttons() {
			for _, b := range row {
				data := s.Data(b.Action)
				id, g, action, e := Parse(data)
				if e != nil || id != s.ID || g != s.Generation || action != b.Action || len(data) > 64 {
					t.Fatal(data, e)
				}
			}
		}
		s = tap(t, s, a, now)
	}
	if s.Step != Done || !s.WithReserve || s.Low != 1.375 || s.High != 1.625 || s.HistoryComplete {
		t.Fatalf("%+v", s)
	}
}
func TestSnapshotHistoryAndObservationTime(t *testing.T) {
	now := time.Date(2026, 9, 15, 21, 0, 0, 0, time.UTC)
	s := start(t, false, true)
	s = tap(t, s, "c0", now)
	s = tap(t, s, "p50", now)
	s = tap(t, s, "none", now.Add(time.Minute))
	if s.Step != Done || !s.HistoryComplete || !s.ObservedAt.Equal(now) {
		t.Fatalf("%+v", s)
	}
}
func TestStaleForbiddenAndMalformed(t *testing.T) {
	s := start(t, true, false)
	now := time.Now()
	n := tap(t, s, "yes", now)
	if _, e := n.Apply(s.Generation, "yes", now); !errors.Is(e, ErrStale) {
		t.Fatal(e)
	}
	if _, e := s.Apply(0, "p100", now); e == nil {
		t.Fatal("accepted hidden action")
	}
	for _, v := range []string{"", "h1:bad:0:yes", "h1:" + testID + ":00:yes", "h1:" + testID + ":z:too-long-action"} {
		if _, _, _, e := Parse(v); e == nil {
			t.Fatal(v)
		}
	}
}
func TestUnknownIsNotNoUse(t *testing.T) {
	now := time.Now()
	s := tap(t, start(t, false, false), "skip", now)
	if !s.Unknown || s.Unused || s.HistoryComplete || s.Step != Done {
		t.Fatal(s)
	}
}
func TestNoUseNeedsExplicitConfirmation(t *testing.T) {
	now := time.Now()
	s := tap(t, start(t, false, false), "unused", now)
	if s.Step != NoUse || s.Unused {
		t.Fatal(s)
	}
	s = tap(t, s, "confirm", now)
	if !s.Unused || !s.HistoryComplete || s.Step != Done {
		t.Fatal(s)
	}
}
func TestLateHistoryRequiresNewStockCheck(t *testing.T) {
	now := time.Now()
	s := tap(t, start(t, false, true), "c1", now)
	s = tap(t, s, "p25", now)
	s = tap(t, s, "none", now.Add(16*time.Minute))
	if s.Step != Closed || !s.ObservedAt.IsZero() {
		t.Fatal(s)
	}
}
func TestEmptyDoesNotInventDepletionTimeOrAskHistory(t *testing.T) {
	now := time.Now()
	s := tap(t, start(t, false, false), "c0", now)
	s = tap(t, s, "p0", now)
	if s.Step != Done || s.Low != 0 || s.High != 0 || s.HistoryComplete {
		t.Fatal(s)
	}
}
func TestCancelNeverBecomesObservation(t *testing.T) {
	s := tap(t, start(t, true, false), "cancel", time.Now())
	if !s.Cancelled || s.Step != Done || !s.ObservedAt.IsZero() {
		t.Fatal(s)
	}
}
func FuzzParse(f *testing.F) {
	f.Add("h1:" + testID + ":0:yes")
	f.Add("")
	f.Fuzz(func(t *testing.T, s string) {
		id, g, a, e := Parse(s)
		if e == nil {
			state := State{ID: id, Generation: g}
			if state.Data(a) != s {
				t.Fatal("noncanonical")
			}
		}
	})
}

func TestPolicyDoesNotImplyPhysicalStock(t *testing.T) {
	now := time.Date(2026, 9, 15, 21, 0, 0, 0, time.UTC)
	s := start(t, true, false)
	for _, a := range []string{"no", "d30", "c1", "p25"} {
		s = tap(t, s, a, now)
	}
	if s.WithReserve || s.Low != 1.125 || s.High != 1.375 {
		t.Fatal(s)
	}
}
func TestStalePartialCountRequiresRecheck(t *testing.T) {
	now := time.Date(2026, 9, 15, 21, 0, 0, 0, time.UTC)
	s := tap(t, start(t, false, true), "c1", now)
	s = tap(t, s, "p25", now.Add(16*time.Minute))
	if s.Step != Closed || !s.ObservedAt.IsZero() {
		t.Fatal(s)
	}
}
