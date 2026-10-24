package bot

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/EugenSleptsov/hoarder/internal/app"
	"github.com/EugenSleptsov/hoarder/internal/forecast"
	"github.com/EugenSleptsov/hoarder/internal/item"
	"github.com/EugenSleptsov/hoarder/internal/sqlstore"
)

// The agent integration lets an external shopping agent read recommendations,
// fill a shop cart and report delivered orders. A recommendation is still not
// an order: only an actually delivered order becomes an exact addition at its
// delivery time. Every computation reads exactly one item.

const (
	ShopKnuspr       = "knuspr"
	maxCoverUnits    = 50
	maxDeliveryLines = 50
	maxChecks        = 10
	maxNotifications = 10
	maxNoticeRunes   = 3000
	deliverySkew     = 5 * time.Minute
)

var externalID = regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,64}$`)

// RequestError is an invalid agent request; it is safe to show to the agent.
type RequestError string

func (e RequestError) Error() string { return string(e) }

// ShopLink maps an item to one shop product. UnitsPerPack converts the shop's
// package to this item's own standard units.
type ShopLink struct {
	ItemID       string    `json:"item_id"`
	Shop         string    `json:"shop"`
	ProductID    string    `json:"product_id"`
	ProductName  string    `json:"product_name"`
	UnitsPerPack float64   `json:"units_per_pack"`
	Note         string    `json:"note,omitempty"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// Need is one item's own forecast evaluated at a requested future moment.
type Need struct {
	ItemID          string        `json:"item_id"`
	Name            string        `json:"name"`
	Paused          bool          `json:"paused"`
	ReserveUnits    float64       `json:"reserve_units"`
	LeadDays        float64       `json:"lead_days"`
	StockNow        item.Interval `json:"stock_now"`
	StockAtUntil    item.Interval `json:"stock_at_until"`
	RatePerDay      item.Interval `json:"rate_per_day"`
	LearningSamples int           `json:"learning_samples"`
	LastObservation time.Time     `json:"last_physical_observation"`
	RecommendedNow  bool          `json:"recommended_now"`
	NeededByUntil   bool          `json:"needed_by_until"`
	// MinUnitsToCover is the smallest whole number of units after which this
	// item would no longer be recommended at Until. It is not a stock target.
	// Nil means more than maxCoverUnits would be needed.
	MinUnitsToCover *int       `json:"min_units_to_cover"`
	Links           []ShopLink `json:"links"`
}

type DeliveryLine struct {
	ItemID string   `json:"item_id"`
	Units  *float64 `json:"units,omitempty"`
	Packs  *float64 `json:"packs,omitempty"`
}

type Delivery struct {
	Shop        string         `json:"shop"`
	OrderID     string         `json:"order_id"`
	DeliveredAt time.Time      `json:"delivered_at"`
	Lines       []DeliveryLine `json:"lines"`
}

type LineResult struct {
	ItemID string `json:"item_id"`
	// Status is recorded, already_recorded or rejected.
	Status         string         `json:"status"`
	Reason         string         `json:"reason,omitempty"`
	Units          float64        `json:"units,omitempty"`
	StockNow       *item.Interval `json:"stock_now,omitempty"`
	RecommendedNow bool           `json:"recommended_now"`
}

func maxTime(times ...time.Time) time.Time {
	var out time.Time
	for _, t := range times {
		if t.After(out) {
			out = t
		}
	}
	return out
}

func finitePositive(v float64, max float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0) && v > 0 && v <= max
}

func (s *Service) Timezone() string { return s.cfg.Zone }

// ShoppingNeeds never writes. Paused items are omitted unless requested,
// matching /shopping; archived items are always omitted.
func (s *Service) ShoppingNeeds(ctx context.Context, now, until time.Time, includePaused bool) ([]Need, error) {
	if until.Before(now) {
		return nil, RequestError("until must not be in the past")
	}
	needs := []Need{}
	err := s.db.Transaction(ctx, func(tx *sqlstore.Tx) error {
		links, err := s.shopLinks(ctx, tx)
		if err != nil {
			return err
		}
		states, err := tx.Items(ctx)
		if err != nil {
			return err
		}
		for _, state := range states {
			if state.Archived || state.Paused && !includePaused {
				continue
			}
			n, err := need(state, now, until)
			if err != nil {
				return err
			}
			var in intent
			err = tx.Get(ctx, "intent", state.Config.ID, &in)
			if err != nil && !errors.Is(err, sqlstore.ErrNotFound) {
				return err
			}
			n.RecommendedNow = err == nil && !state.Paused
			n.Links = links[state.Config.ID]
			if n.Links == nil {
				n.Links = []ShopLink{}
			}
			needs = append(needs, n)
		}
		return nil
	})
	return needs, err
}

// need is pure: one item's state and explicit times, nothing else.
func need(state item.State, now, until time.Time) (Need, error) {
	start := maxTime(now, state.AsOf)
	if state.ControlAt != nil {
		start = maxTime(start, *state.ControlAt)
	}
	until = maxTime(until, start)
	stockNow, err := state.Project(start)
	if err != nil {
		return Need{}, err
	}
	needed, decision, err := recommends(state, until)
	if err != nil {
		return Need{}, err
	}
	n := Need{ItemID: state.Config.ID, Name: state.Config.Name, Paused: state.Paused,
		ReserveUnits: state.Config.ReserveUnits, LeadDays: state.Config.LeadDays,
		StockNow: stockNow, StockAtUntil: decision.Stock, RatePerDay: state.Rate,
		LearningSamples: state.Samples, LastObservation: state.Anchor.At, NeededByUntil: needed}
	if !needed {
		zero := 0
		n.MinUnitsToCover = &zero
		return n, nil
	}
	for units := 1; units <= maxCoverUnits; units++ {
		probe := item.Event{ID: "probe", ItemID: state.Config.ID, Kind: item.Addition, At: start,
			Quantity: item.Interval{Low: float64(units), High: float64(units)}}
		next, err := state.Apply(probe)
		if err != nil {
			return Need{}, err
		}
		still, _, err := recommends(next, until)
		if err != nil {
			return Need{}, err
		}
		if !still {
			n.MinUnitsToCover = &units
			break
		}
	}
	return n, nil
}

func (s *Service) shopLinks(ctx context.Context, tx *sqlstore.Tx) (map[string][]ShopLink, error) {
	rows, err := tx.List(ctx, "shop_link")
	if err != nil {
		return nil, err
	}
	out := map[string][]ShopLink{}
	for _, raw := range rows {
		var l ShopLink
		if err = json.Unmarshal(raw, &l); err != nil {
			return nil, err
		}
		out[l.ItemID] = append(out[l.ItemID], l)
	}
	return out, nil
}

func validShop(shop string) error {
	if shop != ShopKnuspr {
		return RequestError("unsupported shop; only " + ShopKnuspr + " is configured")
	}
	return nil
}

// SetShopLink is integration configuration, not an observation or purchase.
func (s *Service) SetShopLink(ctx context.Context, l ShopLink, now time.Time) (ShopLink, error) {
	l.ProductName, l.Note = strings.TrimSpace(l.ProductName), strings.TrimSpace(l.Note)
	if err := validShop(l.Shop); err != nil {
		return l, err
	}
	if !externalID.MatchString(l.ProductID) {
		return l, RequestError("product_id must be 1-64 characters of letters, digits, '_', '.', ':' or '-'")
	}
	if l.ProductName == "" || utf8.RuneCountInString(l.ProductName) > 200 || utf8.RuneCountInString(l.Note) > 300 {
		return l, RequestError("product_name needs 1-200 characters and note at most 300")
	}
	if !finitePositive(l.UnitsPerPack, 100) {
		return l, RequestError("units_per_pack must be a number above 0 and at most 100")
	}
	l.UpdatedAt = now
	err := s.db.Transaction(ctx, func(tx *sqlstore.Tx) error {
		state, err := tx.LoadItem(ctx, l.ItemID)
		if errors.Is(err, sqlstore.ErrNotFound) {
			return RequestError("unknown item_id")
		}
		if err != nil {
			return err
		}
		if state.Archived {
			return RequestError("item is archived")
		}
		return tx.Put(ctx, "shop_link", l.ItemID+":"+l.Shop, l)
	})
	return l, err
}

func (s *Service) RemoveShopLink(ctx context.Context, itemID, shop string) (bool, error) {
	if err := validShop(shop); err != nil {
		return false, err
	}
	removed := false
	err := s.db.Transaction(ctx, func(tx *sqlstore.Tx) error {
		var l ShopLink
		err := tx.Get(ctx, "shop_link", itemID+":"+shop, &l)
		if errors.Is(err, sqlstore.ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		removed = true
		return tx.Delete(ctx, "shop_link", itemID+":"+shop)
	})
	return removed, err
}

// RecordDelivery adds each delivered line exactly once. The event ID is the
// order, so a repeated report is a no-op and a contradicting one is rejected.
// A line older than the item's latest evidence is rejected: a later snapshot
// already includes the delivered packages and adding them would double count.
func (s *Service) RecordDelivery(ctx context.Context, d Delivery, now time.Time) ([]LineResult, error) {
	if err := validShop(d.Shop); err != nil {
		return nil, err
	}
	if !externalID.MatchString(d.OrderID) {
		return nil, RequestError("order_id must be 1-64 characters of letters, digits, '_', '.', ':' or '-'")
	}
	if d.DeliveredAt.IsZero() || d.DeliveredAt.After(now.Add(deliverySkew)) {
		return nil, RequestError("delivered_at must be the actual past delivery time")
	}
	if len(d.Lines) == 0 || len(d.Lines) > maxDeliveryLines {
		return nil, RequestError("lines must contain 1-50 items")
	}
	seen := map[string]bool{}
	for _, line := range d.Lines {
		if seen[line.ItemID] {
			return nil, RequestError("each item_id may appear once per delivery; sum its quantities")
		}
		seen[line.ItemID] = true
		if (line.Units == nil) == (line.Packs == nil) {
			return nil, RequestError("each line needs exactly one of units or packs")
		}
		for _, v := range []*float64{line.Units, line.Packs} {
			if v != nil && !finitePositive(*v, 100) {
				return nil, RequestError("units and packs must be numbers above 0 and at most 100")
			}
		}
	}
	at := d.DeliveredAt.UTC()
	results := make([]LineResult, 0, len(d.Lines))
	err := s.db.Transaction(ctx, func(tx *sqlstore.Tx) error {
		links, err := s.shopLinks(ctx, tx)
		if err != nil {
			return err
		}
		for _, line := range d.Lines {
			r, err := s.deliverLine(ctx, tx, d, line, at, links, now)
			if err != nil {
				return err
			}
			results = append(results, r)
		}
		return nil
	})
	return results, err
}

func (s *Service) deliverLine(ctx context.Context, tx *sqlstore.Tx, d Delivery, line DeliveryLine, at time.Time, links map[string][]ShopLink, now time.Time) (LineResult, error) {
	r := LineResult{ItemID: line.ItemID, Status: "rejected"}
	state, err := tx.LoadItem(ctx, line.ItemID)
	if errors.Is(err, sqlstore.ErrNotFound) {
		r.Reason = "unknown item_id"
		return r, nil
	}
	if err != nil {
		return r, err
	}
	if state.Archived {
		r.Reason = "item is archived"
		return r, nil
	}
	if line.Units != nil {
		r.Units = *line.Units
	} else {
		for _, l := range links[line.ItemID] {
			if l.Shop == d.Shop {
				r.Units = *line.Packs * l.UnitsPerPack
			}
		}
		if r.Units == 0 {
			r.Reason = "no shop link for this item; pass units instead of packs"
			return r, nil
		}
	}
	event := item.Event{ID: "delivery:" + d.Shop + ":" + d.OrderID, ItemID: state.Config.ID, Kind: item.Addition,
		At: at, Quantity: item.Interval{Low: r.Units, High: r.Units}}
	digest, err := hash(event)
	if err != nil {
		return r, err
	}
	previous, err := tx.CommandReceipt(ctx, state.Config.ID, event.ID)
	if err != nil {
		return r, err
	}
	if previous != nil {
		if previous.PayloadHash != digest {
			r.Reason = "this order was already recorded for this item with a different quantity or time"
			return r, nil
		}
		r.Status = "already_recorded"
		return r, s.describe(ctx, tx, &r, state, now)
	}
	// Validate before storage; the reducer rejects out-of-order evidence.
	if _, err = state.Apply(event); err != nil {
		r.Reason = "the item has a newer observation or setting change than delivered_at; a newer observation likely includes this delivery, so ask the owner to check the item instead"
		return r, nil
	}
	next, err := tx.ApplyEvent(ctx, event, state.Revision)
	if err != nil {
		return r, err
	}
	plan, err := app.PlanItem(next, forecast.Baseline{}, s.daily, now.Add(time.Second))
	if err != nil {
		return r, err
	}
	if err = tx.SaveQuestion(ctx, plan); err != nil {
		return r, err
	}
	if _, err = s.updateIntent(ctx, tx, next, now); err != nil {
		return r, err
	}
	// An open check question was bound to the old revision.
	if err = s.invalidateQuestion(ctx, tx, state.Config.ID, now); err != nil {
		return r, err
	}
	r.Status = "recorded"
	return r, s.describe(ctx, tx, &r, next, now)
}

func (s *Service) describe(ctx context.Context, tx *sqlstore.Tx, r *LineResult, state item.State, now time.Time) error {
	stock, err := state.Project(maxTime(now, state.AsOf))
	if err != nil {
		return err
	}
	r.StockNow = &stock
	var in intent
	err = tx.Get(ctx, "intent", state.Config.ID, &in)
	if err != nil && !errors.Is(err, sqlstore.ErrNotFound) {
		return err
	}
	r.RecommendedNow = err == nil && !state.Paused
	return nil
}

type CheckResult struct {
	ItemID string `json:"item_id"`
	// Status is sent or rejected.
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
}

// RequestChecks asks the owner about selected items now, outside the daily
// slot. The answer is an ordinary observation; the request itself is not.
func (s *Service) RequestChecks(ctx context.Context, ids []string, now time.Time) ([]CheckResult, error) {
	if len(ids) == 0 || len(ids) > maxChecks {
		return nil, RequestError("item_ids must contain 1-10 items")
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			return nil, RequestError("duplicate item_id")
		}
		seen[id] = true
	}
	results := make([]CheckResult, 0, len(ids))
	err := s.db.Transaction(ctx, func(tx *sqlstore.Tx) error {
		for _, id := range ids {
			r := CheckResult{ItemID: id, Status: "rejected"}
			state, err := tx.LoadItem(ctx, id)
			switch {
			case errors.Is(err, sqlstore.ErrNotFound):
				r.Reason = "unknown item_id"
			case err != nil:
				return err
			case state.Archived:
				r.Reason = "item is archived"
			default:
				if err = s.beginCheck(ctx, tx, id, false, 0, now); err != nil {
					return err
				}
				r.Status = "sent"
			}
			results = append(results, r)
		}
		return nil
	})
	return results, err
}

// NotifyOwner sends a plain message to the owner chat. A daily cap keeps a
// looping agent from flooding the chat.
func (s *Service) NotifyOwner(ctx context.Context, text string, now time.Time) error {
	text = strings.TrimSpace(text)
	if text == "" || utf8.RuneCountInString(text) > maxNoticeRunes {
		return RequestError("text needs 1-3000 characters")
	}
	date, err := s.daily.DateKey(now)
	if err != nil {
		return err
	}
	return s.db.Transaction(ctx, func(tx *sqlstore.Tx) error {
		var sent int
		err := tx.Get(ctx, "agent_notice", date, &sent)
		if err != nil && !errors.Is(err, sqlstore.ErrNotFound) {
			return err
		}
		if sent >= maxNotifications {
			return RequestError("daily notification limit reached")
		}
		if err = tx.Put(ctx, "agent_notice", date, sent+1); err != nil {
			return err
		}
		return s.note(ctx, tx, text+"\n\n/shopping · /items", nil, 0, now)
	})
}
