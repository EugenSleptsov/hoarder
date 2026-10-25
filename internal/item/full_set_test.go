package item

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"
)

func TestFullSetIsAWholeExplicitPolicyAndOnePackageStaysImplicit(t *testing.T) {
	base := fixture(t).Config
	if base.FullSet() != 1 {
		t.Fatal("an item without a stored set must mean one package", base)
	}
	// One package is stored as zero, so older event bytes and receipts stay
	// identical and choosing the current value is recognised as no change.
	one := base.WithFullSet(1)
	if one != base {
		t.Fatal("one package stored explicitly", one)
	}
	raw, err := json.Marshal(one)
	if err != nil || strings.Contains(string(raw), "full_units") {
		t.Fatal("default set changed configuration bytes", string(raw), err)
	}
	set := base.WithFullSet(4)
	if set.FullSet() != 4 || set.Validate() != nil {
		t.Fatal(set)
	}
	for _, units := range []float64{1, 2.5, -2, 100, math.NaN(), math.Inf(1)} {
		bad := base
		bad.FullUnits = units
		if bad.Validate() == nil {
			t.Fatal("invalid full set accepted", units)
		}
	}
	for _, units := range []float64{4, 99} {
		ok := base
		ok.ReserveUnits = units
		if ok.Validate() != nil {
			t.Fatal("explicit reserve rejected", units)
		}
	}
	tooMany := base
	tooMany.ReserveUnits = 100
	if tooMany.Validate() == nil {
		t.Fatal("unbounded reserve accepted")
	}
}

func TestReconfiguringTheSetIsNotStockEvidence(t *testing.T) {
	s := fixture(t)
	set := s.Config.WithFullSet(4)
	set.ReserveUnits = 4
	e := Event{ID: "set", ItemID: s.Config.ID, Kind: Reconfigure, At: s.AsOf.Add(48 * time.Hour), Configuration: &set}
	n, err := s.Apply(e)
	if err != nil {
		t.Fatal(err)
	}
	if n.Stock != s.Stock || !n.AsOf.Equal(s.AsOf) || n.Anchor != s.Anchor || n.Rate != s.Rate || n.Added != 0 || n.Config.FullSet() != 4 {
		t.Fatal("a set size changed stock evidence", n)
	}
}
