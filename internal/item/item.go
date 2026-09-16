// Package item defines an independent household consumable and its observations.
package item

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
)

// Interval is a heuristic plausibility range, not a calibrated confidence interval.
type Interval struct {
	Low  float64 `json:"low"`
	High float64 `json:"high"`
}

func (v Interval) Validate() error {
	if !finite(v.Low) || !finite(v.High) || v.Low < 0 || v.High < v.Low {
		return errors.New("invalid nonnegative interval")
	}
	return nil
}

func (v Interval) Mid() float64 { return v.Low + (v.High-v.Low)/2 }

func finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }

type Config struct {
	ID           string  `json:"id"`
	Name         string  `json:"name"`
	WithReserve  bool    `json:"with_reserve"`
	ReserveUnits float64 `json:"reserve_units"`
	LeadDays     float64 `json:"lead_days"`
	MaxCheckDays float64 `json:"max_check_days"`
}

func (c Config) Validate() error {
	if strings.TrimSpace(c.ID) == "" || strings.TrimSpace(c.Name) == "" {
		return errors.New("item ID and name are required")
	}
	if !finite(c.ReserveUnits) || c.ReserveUnits < 0 || c.WithReserve != (c.ReserveUnits > 0) {
		return errors.New("reserve flag and fixed reserve units disagree")
	}
	if !finite(c.LeadDays) || c.LeadDays < 0 || c.LeadDays > 365 {
		return errors.New("lead days must be between 0 and 365")
	}
	if !finite(c.MaxCheckDays) || c.MaxCheckDays < 1 || c.MaxCheckDays > 365 {
		return errors.New("maximum check gap must be between 1 and 365 days")
	}
	return nil
}

type Anchor struct {
	Stock Interval  `json:"stock"`
	At    time.Time `json:"at"`
}

type State struct {
	// Controls are not observations and do not freeze physical consumption.
	Paused      bool              `json:"paused,omitempty"`
	Archived    bool              `json:"archived,omitempty"`
	ControlAt   *time.Time        `json:"control_at,omitempty"`
	Config      Config            `json:"config"`
	Stock       Interval          `json:"stock"`
	Rate        Interval          `json:"rate"`
	AsOf        time.Time         `json:"as_of"`
	LastContact time.Time         `json:"last_contact"`
	Anchor      Anchor            `json:"anchor"`
	Added       float64           `json:"added_since_anchor"`
	Samples     int               `json:"samples"`
	Revision    uint64            `json:"revision"`
	LastNote    string            `json:"last_note"`
	Receipts    map[string]string `json:"receipts"`
}

func New(c Config, stock, rate Interval, at time.Time) (State, error) {
	s := State{Config: c, Stock: stock, Rate: rate, AsOf: at, LastContact: at,
		Anchor: Anchor{stock, at}, Receipts: make(map[string]string)}
	return s, s.Validate()
}

func (s State) Validate() error {
	if s.Archived && !s.Paused || s.ControlAt != nil && s.ControlAt.IsZero() {
		return errors.New("invalid lifecycle state")
	}
	if err := s.Config.Validate(); err != nil {
		return err
	}
	if err := s.Stock.Validate(); err != nil {
		return fmt.Errorf("stock: %w", err)
	}
	if err := s.Rate.Validate(); err != nil {
		return fmt.Errorf("rate: %w", err)
	}
	if err := s.Anchor.Stock.Validate(); err != nil {
		return fmt.Errorf("anchor: %w", err)
	}
	if s.Rate.High <= 0 || s.AsOf.IsZero() || s.Anchor.At.IsZero() || s.LastContact.IsZero() ||
		s.Anchor.At.After(s.AsOf) || s.LastContact.After(s.AsOf) ||
		!finite(s.Added) || s.Added < 0 || s.Samples < 0 {
		return errors.New("invalid item state")
	}
	return nil
}

// Project never changes the observation anchor or mistakes silence for zero use.
func (s State) Project(at time.Time) (Interval, error) {
	if err := s.Validate(); err != nil {
		return Interval{}, err
	}
	if at.Before(s.AsOf) {
		return Interval{}, errors.New("cannot project backwards")
	}
	days := at.Sub(s.AsOf).Hours() / 24
	return Interval{math.Max(0, s.Stock.Low-s.Rate.High*days),
		math.Max(0, s.Stock.High-s.Rate.Low*days)}, nil
}

type Kind string

const (
	Snapshot Kind = "snapshot"
	Addition Kind = "addition"
	Unknown  Kind = "unknown"
	NoUse    Kind = "no_use"
)

type Event struct {
	Configuration   *Config   `json:"configuration,omitempty"`
	ID              string    `json:"id"`
	ItemID          string    `json:"item_id"`
	Kind            Kind      `json:"kind"`
	At              time.Time `json:"at"`
	Quantity        Interval  `json:"quantity"`
	HistoryComplete bool      `json:"history_complete"`
	From            time.Time `json:"from,omitempty"`
}

// Apply is copy-on-write. Durable adapters must also enforce command uniqueness
// transactionally; receipts make replay and duplicate handling explicit here.
func (s State) Apply(e Event) (State, error) {
	if err := s.Validate(); err != nil {
		return s, err
	}
	if e.ID == "" || e.ItemID != s.Config.ID || e.At.IsZero() {
		return s, errors.New("event identity, item and time are required")
	}
	if err := e.Quantity.Validate(); err != nil {
		return s, err
	}
	payload, err := json.Marshal(e)
	if err != nil {
		return s, err
	}
	digest := sha256.Sum256(payload)
	hash := hex.EncodeToString(digest[:])
	if previous, exists := s.Receipts[e.ID]; exists {
		if previous != hash {
			return s, errors.New("event ID reused with different payload")
		}
		return s, nil
	}
	if e.Configuration != nil && e.Kind != Reconfigure {
		return s, errors.New("configuration payload requires a configuration event")
	}
	if e.At.Before(s.AsOf) || s.ControlAt != nil && e.At.Before(*s.ControlAt) {
		return s, errors.New("out-of-order event requires replay")
	}
	n := s
	projected, err := s.Project(e.At)
	if err != nil {
		return s, err
	}
	n.Stock, n.AsOf, n.LastContact = projected, e.At, e.At
	n.LastNote = ""
	switch e.Kind {
	case Pause, Resume, Archive, Restore, Reconfigure:
		n, err = s.applyControl(e)
		if err != nil {
			return s, err
		}
	case Snapshot:
		// Training uses only uncensored, confirmed no-addition intervals.
		// Even a known addition may conceal a period with no stock available.
		days := e.At.Sub(s.Anchor.At).Hours() / 24
		if e.HistoryComplete && s.Added == 0 && days >= 1 && e.Quantity.Low > 0 &&
			e.Quantity.Low <= s.Anchor.Stock.High {
			lo := math.Max(0, s.Anchor.Stock.Low-e.Quantity.High) / days
			hi := math.Max(0, s.Anchor.Stock.High-e.Quantity.Low) / days
			const alpha = 0.35
			n.Rate = Interval{(1-alpha)*s.Rate.Low + alpha*lo,
				math.Max(1e-9, (1-alpha)*s.Rate.High+alpha*hi)}
			n.Samples++
			n.LastNote = "rate_updated_from_complete_interval"
		} else {
			n.LastNote = "stock_reanchored_without_rate_learning"
		}
		n.Stock, n.Anchor, n.Added = e.Quantity, Anchor{e.Quantity, e.At}, 0
	case Addition:
		if e.Quantity.Low != e.Quantity.High || e.Quantity.Low <= 0 {
			return s, errors.New("addition must have a known positive quantity")
		}
		n.Stock.Low += e.Quantity.Low
		n.Stock.High += e.Quantity.High
		n.Added += e.Quantity.Low
		n.LastNote = "quantity_added_not_reset"
	case Unknown:
		if e.Quantity != (Interval{}) {
			return s, errors.New("unknown is not a stock observation")
		}
		n.LastNote = "no_stock_evidence"
	case NoUse:
		if !e.From.Equal(s.AsOf) || !e.HistoryComplete || e.Quantity != (Interval{}) {
			return s, errors.New("no-use needs the exact interval and confirmed complete history")
		}
		n.Stock, n.Anchor, n.Added = s.Stock, Anchor{s.Stock, e.At}, 0
		n.LastNote = "confirmed_no_use_rate_unchanged"
	default:
		return s, errors.New("unsupported event kind")
	}
	n.Revision++
	n.Receipts = make(map[string]string, len(s.Receipts)+1)
	for id, receipt := range s.Receipts {
		n.Receipts[id] = receipt
	}
	n.Receipts[e.ID] = hash
	if err := n.Validate(); err != nil {
		return s, err
	}
	return n, nil
}
