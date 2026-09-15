package schedule

import (
	"testing"
	"time"
)

func instant(t *testing.T, s string) time.Time {
	t.Helper()
	v, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestBerlinDSTPolicy(t *testing.T) {
	d, err := New("Europe/Berlin", 2, 30)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		month time.Month
		day   int
		want  string
	}{
		{time.March, 29, "2026-03-29T01:00:00Z"},
		{time.October, 25, "2026-10-25T00:30:00Z"},
	} {
		got, err := d.Slot(2026, tc.month, tc.day)
		if err != nil || !got.Equal(instant(t, tc.want)) {
			t.Fatal(tc, got, err)
		}
	}
	// Once the first 02:30 has passed, the repeated 02:30 is not another session.
	next, err := d.Next(instant(t, "2026-10-25T00:31:00Z"))
	if err != nil || !next.Equal(instant(t, "2026-10-26T01:30:00Z")) {
		t.Fatal(next, err)
	}
}

func TestBeforeDeadlineRoundsEarlier(t *testing.T) {
	d, _ := New("Europe/Berlin", 21, 0)
	p, err := d.BeforeDeadline(instant(t, "2026-09-15T10:00:00Z"), instant(t, "2026-09-20T10:00:00Z"))
	if err != nil || p.DeadlineUnmet || !p.At.Equal(instant(t, "2026-09-19T19:00:00Z")) {
		t.Fatal(p, err)
	}
}

func TestUnmetDeadlineIsNotHiddenByRounding(t *testing.T) {
	d, _ := New("Europe/Berlin", 21, 0)
	p, err := d.BeforeDeadline(instant(t, "2026-09-15T10:00:00Z"), instant(t, "2026-09-15T12:00:00Z"))
	if err != nil || !p.DeadlineUnmet || !p.At.Equal(instant(t, "2026-09-15T19:00:00Z")) {
		t.Fatal(p, err)
	}
}

func TestNextIsInclusiveAndDateKeyUsesLocalDate(t *testing.T) {
	d, _ := New("Europe/Berlin", 21, 0)
	now := instant(t, "2026-09-15T19:00:00Z")
	next, err := d.Next(now)
	if err != nil || !next.Equal(now) {
		t.Fatal(next, err)
	}
	key, err := d.DateKey(instant(t, "2026-09-15T23:00:00Z"))
	if err != nil || key != "2026-09-16" {
		t.Fatal(key, err)
	}
}

func TestInvalidConfigurationAndSkippedDate(t *testing.T) {
	if _, err := New("Europe/Berlin", 24, 0); err == nil {
		t.Fatal("invalid hour")
	}
	if _, err := New("bad/zone", 21, 0); err == nil {
		t.Fatal("invalid zone")
	}
	d, _ := New("Pacific/Apia", 21, 0)
	if _, err := d.Slot(2011, time.December, 30); err == nil {
		t.Fatal("skipped date accepted")
	}
	if _, err := d.Slot(2026, time.February, 30); err == nil {
		t.Fatal("invalid date normalised")
	}
	if _, err := (Daily{}).Next(time.Now()); err == nil {
		t.Fatal("zero schedule accepted")
	}
}
