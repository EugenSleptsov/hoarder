package dialog

import (
	"strings"
	"testing"
	"time"
)

func labels(s State) []string {
	var out []string
	for _, row := range s.Buttons() {
		for _, b := range row {
			out = append(out, b.Label)
		}
	}
	return out
}

func offers(s State, label string) bool {
	for _, l := range labels(s) {
		if l == label {
			return true
		}
	}
	return false
}

func TestClosedCountsPageBeyondThreeUpToTheLimit(t *testing.T) {
	now := time.Date(2026, 9, 15, 21, 0, 0, 0, time.UTC)
	small := start(t, false, true)
	small.Scale = 2
	if got := strings.Join(labels(small), ","); !strings.HasPrefix(got, "Нет,Одна,Две,Три,Больше 3") {
		t.Fatal("small items lost their worded counts", got)
	}
	big := start(t, false, true)
	big.Scale = 8 // A set of four plus a reserve of four.
	if !offers(big, "11") || offers(big, "12") || !offers(big, "Больше 11") || offers(big, "Меньше 0") {
		t.Fatal("first page does not cover the set plus reserve", labels(big))
	}
	if _, err := big.Apply(big.Generation, "c12", now); err == nil {
		t.Fatal("a count not on the page was accepted")
	}
	big = tap(t, big, "cmore", now)
	if big.Step != Closed || !offers(big, "12") || !offers(big, "23") || !offers(big, "Меньше 12") || !big.ClosedAt.IsZero() {
		t.Fatal("paging recorded a count or skipped numbers", big, labels(big))
	}
	big = tap(t, big, "c13", now)
	big = tap(t, big, "p50", now)
	if big.Low != 13.375 || big.High != 13.625 || big.ClosedPage != 0 {
		t.Fatal(big)
	}
	last := start(t, false, true)
	for last.ClosedPage = 0; offers(last, "Больше 99") || !offers(last, "99"); last.ClosedPage++ {
		if last.ClosedPage > 20 {
			t.Fatal("pages never reach the limit")
		}
	}
	if offers(last, "100") || strings.Contains(strings.Join(labels(last), ","), "Больше") {
		t.Fatal("counts beyond the limit offered", labels(last))
	}
}

func TestQuickAddingChoosesAFullSetAndReserve(t *testing.T) {
	now := time.Date(2026, 9, 16, 19, 0, 0, 0, time.UTC)
	s, err := NewQuick(testID, "Влажная бумага")
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range []string{"many", "f4", "r4"} {
		s = tap(t, s, a, now)
	}
	if s.Step != Initial || s.SetUnits != 4 || s.ReserveUnits != 4 || !s.WithReserve || s.Scale != 8 {
		t.Fatal(s)
	}
	if !offers(s, "Полный набор: 4 уп.") || !offers(s, "Набор + резерв: 8 уп.") || offers(s, "Одна полная упаковка") {
		t.Fatal("presets ignore the set", labels(s))
	}
	s = tap(t, s, "q2", now)
	if s.Step != Review || s.Low != 7.875 || s.High != 8 || !strings.Contains(s.Text(), "полный набор 4 уп. + резерв 4 уп.") {
		t.Fatal(s, s.Text())
	}
	s = tap(t, s, "stock", now)
	s = tap(t, s, "qhalf", now)
	if s.Low != 1.5 || s.High != 2.5 {
		t.Fatal("half a set", s)
	}
	// Back walks through the set steps; a plain choice drops the set again.
	s = tap(t, s, "stock", now)
	s = tap(t, s, "back", now)
	if s.Step != ReserveSize {
		t.Fatal(s.Step)
	}
	s = tap(t, s, "r0", now)
	if s.WithReserve || s.Scale != 4 || offers(s, "Набор + резерв: 4 уп.") {
		t.Fatal("no reserve still offers a reserve preset", labels(s))
	}
	for _, a := range []string{"back", "back", "back", "yes"} {
		s = tap(t, s, a, now)
	}
	if s.Step != Initial || s.SetUnits != 0 || s.ReserveUnits != 0 || s.Scale != 0 || !s.WithReserve || !offers(s, "Одна полная упаковка") {
		t.Fatal("plain reserve kept the old set", s)
	}
	// Leaving the count screen forgets its page.
	for _, a := range []string{"detail", "cmore", "back", "detail"} {
		s = tap(t, s, a, now)
	}
	if !offers(s, "Три") || s.ClosedPage != 0 {
		t.Fatal("count screen reopened on an old page", labels(s))
	}
}
