package dialog

import (
	"encoding/json"
	"testing"
	"time"
)

func TestQuickOnboardingThreeTapsAndDefaultDisclosure(t *testing.T) {
	s, err := NewQuick(testID, "Паста")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 16, 19, 0, 0, 0, time.UTC)
	for _, a := range []string{"yes", "q1"} {
		s = tap(t, s, a, now)
	}
	if s.Step != Review || s.DurationHint || s.Low != .875 || s.High != 1 || !s.WithReserve {
		t.Fatal(s)
	}
	// Reserve policy does not manufacture another physical package.
	s = tap(t, s, "save", now)
	if s.Step != Done || s.High != 1 {
		t.Fatal(s)
	}
}
func TestQuickReviewCanGoBackAndPersistEdits(t *testing.T) {
	s, _ := NewQuick(testID, "Мыло")
	now := time.Now()
	for _, a := range []string{"no", "q2", "duration", "d90", "back", "detail", "c0", "p25"} {
		s = tap(t, s, a, now)
	}
	raw, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	var resumed State
	if err = json.Unmarshal(raw, &resumed); err != nil {
		t.Fatal(err)
	}
	s = tap(t, resumed, "save", now)
	if s.Step != Done || s.WithReserve || s.High != .375 || s.DurationDays != 90 || !s.DurationHint {
		t.Fatal(s)
	}
}
func TestQuickConfirmationCannotRefreshOldObservation(t *testing.T) {
	s, _ := NewQuick(testID, "Паста")
	now := time.Now()
	s = tap(t, s, "yes", now)
	s = tap(t, s, "q2", now)
	checked := s.ObservedAt
	s = tap(t, s, "save", now.Add(16*time.Minute))
	if s.Step != Initial || !s.ObservedAt.Equal(checked) {
		t.Fatal(s)
	}
}
func TestQuickCancelAndStaleButtons(t *testing.T) {
	s, _ := NewQuick(testID, "Паста")
	now := time.Now()
	old := s.Generation
	s = tap(t, s, "yes", now)
	if _, err := s.Apply(old, "no", now); err == nil {
		t.Fatal("old reserve choice accepted")
	}
	s = tap(t, s, "cancel", now)
	if !s.Cancelled || s.Step != Done || !s.ObservedAt.IsZero() {
		t.Fatal(s)
	}
}
