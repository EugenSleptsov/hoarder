package dialog

import (
	"testing"
	"time"
)

func TestEveryStockPresetHasCorrectQuantityIndependentOfPolicy(t *testing.T) {
	now := time.Date(2026, 9, 16, 19, 0, 0, 0, time.UTC)
	for _, policy := range []string{"yes", "no"} {
		for _, tc := range []struct {
			action    string
			low, high float64
		}{{"q0", 0, 0}, {"qhalf", .375, .625}, {"q1", .875, 1}, {"q2", 1.875, 2}} {
			t.Run(policy+"/"+tc.action, func(t *testing.T) {
				s, err := NewQuick(testID, "Паста")
				if err != nil {
					t.Fatal(err)
				}
				s = tap(t, s, policy, now)
				s = tap(t, s, tc.action, now)
				if s.Step != Review || s.Low != tc.low || s.High != tc.high || !s.ObservedAt.Equal(now) {
					t.Fatalf("preset %s/%s: %+v; want [%g,%g]", policy, tc.action, s, tc.low, tc.high)
				}
				s = tap(t, s, "save", now.Add(time.Minute))
				if s.Step != Done || s.Low != tc.low || s.High != tc.high || !s.ObservedAt.Equal(now) {
					t.Fatal("confirmation changed stock or its observation time", s)
				}
			})
		}
	}
}
