// Package forecast predicts one item at a time, without cross-item state.
package forecast

import (
	"math"
	"time"

	"github.com/EugenSleptsov/hoarder/internal/item"
)

const Version = "interval-baseline-v1"

type Action string

const (
	Wait               Action = "wait"
	CheckStock         Action = "check_stock"
	CheckReplenishment Action = "check_replenishment"
)

type Decision struct {
	ItemID                         string        `json:"item_id"`
	ModelVersion                   string        `json:"model_version"`
	Stock                          item.Interval `json:"stock"`
	NextCheckAt                    time.Time     `json:"next_check_at"`
	Due                            bool          `json:"due"`
	Action                         Action        `json:"action"`
	PossibleShortageBeforePurchase bool          `json:"possible_shortage_before_purchase"`
	Reason                         string        `json:"reason"`
}

type Predictor interface {
	Predict(item.State, time.Time) (Decision, error)
}

// Baseline uses plausible rate bounds, not fitted probability distributions.
// It is intentionally non-seasonal. Its parameters require empirical evaluation.
type Baseline struct{}

func (Baseline) Predict(s item.State, now time.Time) (Decision, error) {
	stock, err := s.Project(now)
	if err != nil {
		return Decision{}, err
	}
	// The cap is anchored to actual physical evidence, not each scheduler tick
	// or an explicit unknown answer. Otherwise the deadline could slide forever.
	due := s.Anchor.At.Add(days(s.Config.MaxCheckDays))
	reason := "maximum_observation_gap"
	targetDays := math.Max(0, (s.Stock.Mid()-s.Config.ReserveUnits)/s.Rate.Mid())
	if !s.Config.WithReserve {
		targetDays = math.Max(0, s.Stock.Mid()/s.Rate.Mid()-s.Config.LeadDays)
	}
	safetyDays := math.Max(0, s.Stock.Low/s.Rate.High-s.Config.LeadDays)
	for _, candidate := range []struct {
		after  float64
		reason string
	}{
		{targetDays, "predicted_replenishment_boundary"},
		{safetyDays, "possible_exhaustion_before_purchase"},
	} {
		// A candidate beyond this horizon cannot precede the bounded cap.
		// This also avoids overflow converting extreme rates to time.Duration.
		if candidate.after <= 365 {
			at := s.AsOf.Add(days(candidate.after))
			if at.Before(due) {
				due, reason = at, candidate.reason
			}
		}
	}
	d := Decision{ItemID: s.Config.ID, ModelVersion: Version, Stock: stock, NextCheckAt: due,
		Due: !due.After(now), Action: Wait, Reason: reason,
		PossibleShortageBeforePurchase: stock.Low <= s.Rate.High*s.Config.LeadDays}
	if d.Due {
		d.Action = CheckStock
		if stock.Mid() <= s.Config.ReserveUnits || d.PossibleShortageBeforePurchase {
			d.Action = CheckReplenishment
		}
	}
	return d, nil
}

func days(n float64) time.Duration { return time.Duration(n * float64(24*time.Hour)) }
