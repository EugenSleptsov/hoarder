package dialog

import (
	"errors"
	"reflect"
	"testing"
	"time"
)

func FuzzQuickWizardActionSequences(f *testing.F) {
	f.Add([]byte{0, 0, 0})
	f.Add([]byte{1, 4, 1, 2, 1, 3, 0, 2, 0})
	f.Fuzz(func(t *testing.T, path []byte) {
		s, err := NewQuick("aaaaaaaaaaaaaaaaaaaaaaaa", "Тест")
		if err != nil {
			t.Fatal(err)
		}
		now := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
		for i, b := range path {
			if i >= 128 || s.Step == Done {
				break
			}
			var choices []Button
			for _, row := range s.Buttons() {
				choices = append(choices, row...)
			}
			if len(choices) == 0 {
				t.Fatalf("reachable dead end: %s", s.Step)
			}
			a := choices[int(b)%len(choices)].Action
			next, err := s.Apply(s.Generation, a, now)
			if err != nil {
				t.Fatalf("offered action rejected: %s/%s: %v", s.Step, a, err)
			}
			replay, err := s.Apply(s.Generation, a, now)
			if err != nil || !reflect.DeepEqual(next, replay) {
				t.Fatal("nondeterministic transition")
			}
			if _, err = next.Apply(s.Generation, a, now); !errors.Is(err, ErrStale) {
				t.Fatal("old generation accepted")
			}
			if next.Generation != s.Generation+1 || next.Low < 0 || next.High < next.Low || next.DurationDays <= 0 {
				t.Fatal("invalid wizard result")
			}
			s = next
			// Sometimes expire the physical answer while navigating the form.
			if b&16 != 0 {
				now = now.Add(16 * time.Minute)
			}
		}
	})
}
