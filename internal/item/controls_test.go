package item

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

func controlFixture(t *testing.T) State {
	t.Helper()
	s, err := New(Config{ID: "paste", Name: "Paste", WithReserve: true, ReserveUnits: 1, LeadDays: 3, MaxCheckDays: 90}, Interval{1.4, 1.6}, Interval{.01, .03}, time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestControlsDoNotBecomePhysicalEvidence(t *testing.T) {
	s := controlFixture(t)
	initial := s
	at := s.AsOf.Add(10 * 24 * time.Hour)
	for i, kind := range []Kind{Pause, Resume, Archive, Restore, Resume} {
		e := Event{ID: string(kind) + string(rune('a'+i)), ItemID: s.Config.ID, Kind: kind, At: at.Add(time.Duration(i) * time.Hour)}
		n, err := s.Apply(e)
		if err != nil {
			t.Fatal(err)
		}
		if n.Stock != initial.Stock || n.Anchor != initial.Anchor || n.Rate != initial.Rate || n.Samples != initial.Samples || n.AsOf != initial.AsOf || n.LastContact != initial.LastContact || n.Config != initial.Config {
			t.Fatal("control changed physical evidence")
		}
		projected, _ := n.Project(at.Add(24 * time.Hour))
		want, _ := initial.Project(at.Add(24 * time.Hour))
		if projected != want {
			t.Fatal("pause stopped consumption")
		}
		again, err := n.Apply(e)
		if err != nil || !reflect.DeepEqual(again, n) {
			t.Fatal("duplicate control was not idempotent", err)
		}
		s = n
	}
}

func TestReconfigureDoesNotCreateReserveStock(t *testing.T) {
	s := controlFixture(t)
	cfg := s.Config
	cfg.ReserveUnits = 2
	cfg.LeadDays = 7
	e := Event{ID: "policy", ItemID: cfg.ID, Kind: Reconfigure, At: s.AsOf.Add(time.Hour), Configuration: &cfg}
	n, err := s.Apply(e)
	if err != nil || n.Config != cfg || n.Stock != s.Stock || n.Anchor != s.Anchor || n.Rate != s.Rate || n.Samples != 0 {
		t.Fatal(n, err)
	}
	cfg.ReserveUnits = 99
	if n.Config.ReserveUnits != 2 {
		t.Fatal("configuration retained mutable input pointer")
	}
	if _, err = n.Apply(e); err == nil {
		t.Fatal("changed duplicate payload accepted")
	}
	if _, err = n.Apply(Event{ID: "old", ItemID: cfg.ID, Kind: Unknown, At: s.AsOf}); err == nil {
		t.Fatal("out-of-order answer crossed a configuration change")
	}
}

func TestInvalidControls(t *testing.T) {
	s := controlFixture(t)
	for _, e := range []Event{
		{Kind: Resume}, {Kind: Restore}, {Kind: Reconfigure},
		{Kind: Pause, Quantity: Interval{1, 1}},
		{Kind: Pause, Configuration: &s.Config},
		{Kind: Unknown, Configuration: &s.Config},
	} {
		e.ID, e.ItemID, e.At = "bad", s.Config.ID, s.AsOf
		if _, err := s.Apply(e); err == nil {
			t.Fatal("accepted invalid control", e)
		}
	}
}

func TestLegacyEventEncodingRemainsCompatible(t *testing.T) {
	b, err := json.Marshal(Event{ID: "old", ItemID: "paste", Kind: Unknown, At: time.Unix(1, 0).UTC()})
	if err != nil || strings.Contains(string(b), "configuration") {
		t.Fatal("legacy receipt hash would change", string(b), err)
	}
}
