package bot

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/EugenSleptsov/hoarder/internal/forecast"
	"github.com/EugenSleptsov/hoarder/internal/mcp"
)

// AgentInstructions is sent to the MCP client during initialisation.
const AgentInstructions = `hoarder tracks one household's non-perishable consumables. Every item has its own independent forecast.
Quantities are in each item's own standard package units and are heuristic [low, high] ranges, not calibrated probabilities.
A recommendation is never an order: do not place or pay for orders; the owner reviews the cart and checks out.
Record a delivery only after the order was actually delivered, once per order, with the order's delivery time. Repeating the identical call is harmless.
If an item has no learning samples or a wide stock range, prefer request_checks before buying it.
notify_owner writes straight into the owner's Telegram chat: keep messages short and send at most a few per day.`

// decode rejects unknown fields so a misspelt argument is not silently ignored.
func decode(raw json.RawMessage, out any) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(out); err != nil {
		return mcp.Reject("invalid arguments: %v", err)
	}
	return nil
}

func agentError(err error) error {
	var invalid RequestError
	if errors.As(err, &invalid) {
		return mcp.Reject("%s", string(invalid))
	}
	return err
}

func shopOrDefault(shop string) string {
	if shop == "" {
		return ShopKnuspr
	}
	return shop
}

// AgentTools exposes the integration over MCP. The clock is injected for tests.
func (s *Service) AgentTools(clock func() time.Time) []mcp.Tool {
	return []mcp.Tool{
		{
			Name: "shopping_needs",
			Description: "List active items with their own forecast evaluated now and at a future moment (for example the next delivery). " +
				"needed_by_until uses the same rule as the bot's shopping list. min_units_to_cover is the smallest whole number of the item's units " +
				"after which it would no longer be recommended at that moment. units_to_full_set is what to buy: enough units for the projected stock at that moment " +
				"to reach the item's full set plus its reserve, never less than the minimum. Neither is a stock target or an order. Links map items to shop products.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{
"until":{"type":"string","format":"date-time","description":"RFC 3339 moment to evaluate, e.g. the delivery after next"},
"horizon_days":{"type":"number","minimum":0,"maximum":60,"description":"Alternative to until: days from now"},
"include_paused":{"type":"boolean","description":"Also list paused items (default false, like /shopping)"}},
"additionalProperties":false}`),
			Call: func(ctx context.Context, raw json.RawMessage) (any, error) {
				var a struct {
					Until         *time.Time `json:"until"`
					HorizonDays   *float64   `json:"horizon_days"`
					IncludePaused bool       `json:"include_paused"`
				}
				if err := decode(raw, &a); err != nil {
					return nil, err
				}
				now := clock().UTC()
				var until time.Time
				switch {
				case (a.Until == nil) == (a.HorizonDays == nil):
					return nil, mcp.Reject("pass exactly one of until or horizon_days")
				case a.Until != nil:
					until = a.Until.UTC()
				case *a.HorizonDays < 0 || *a.HorizonDays > 60:
					return nil, mcp.Reject("horizon_days must be between 0 and 60")
				default:
					until = now.Add(time.Duration(*a.HorizonDays * float64(24*time.Hour)))
				}
				if until.After(now.Add(60 * 24 * time.Hour)) {
					return nil, mcp.Reject("until must be at most 60 days ahead")
				}
				needs, err := s.ShoppingNeeds(ctx, now, until, a.IncludePaused)
				if err != nil {
					return nil, agentError(err)
				}
				return map[string]any{"now": now, "until": until, "timezone": s.Timezone(), "model_version": forecast.Version,
					"units": "each item's own standard package equivalents", "items": needs}, nil
			},
		},
		{
			Name: "set_shop_link",
			Description: "Link an item to one shop product. units_per_pack converts one shop package into the item's units " +
				"(2 if the shop pack holds two of the item's packages). Replaces the previous link for the same item and shop.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{
"item_id":{"type":"string"},"shop":{"type":"string","enum":["knuspr"],"default":"knuspr"},
"product_id":{"type":"string","description":"The shop's product ID"},"product_name":{"type":"string"},
"units_per_pack":{"type":"number","exclusiveMinimum":0,"maximum":100},"note":{"type":"string","description":"Optional, e.g. preferred substitutes"}},
"required":["item_id","product_id","product_name","units_per_pack"],"additionalProperties":false}`),
			Call: func(ctx context.Context, raw json.RawMessage) (any, error) {
				var l ShopLink
				if err := decode(raw, &l); err != nil {
					return nil, err
				}
				if !l.UpdatedAt.IsZero() {
					return nil, mcp.Reject("updated_at is set by the server")
				}
				l.Shop = shopOrDefault(l.Shop)
				saved, err := s.SetShopLink(ctx, l, clock().UTC())
				if err != nil {
					return nil, agentError(err)
				}
				return map[string]any{"link": saved}, nil
			},
		},
		{
			Name:        "remove_shop_link",
			Description: "Remove an item's link to a shop product.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{
"item_id":{"type":"string"},"shop":{"type":"string","enum":["knuspr"],"default":"knuspr"}},
"required":["item_id"],"additionalProperties":false}`),
			Call: func(ctx context.Context, raw json.RawMessage) (any, error) {
				var a struct {
					ItemID string `json:"item_id"`
					Shop   string `json:"shop"`
				}
				if err := decode(raw, &a); err != nil {
					return nil, err
				}
				removed, err := s.RemoveShopLink(ctx, a.ItemID, shopOrDefault(a.Shop))
				if err != nil {
					return nil, agentError(err)
				}
				return map[string]any{"removed": removed}, nil
			},
		},
		{
			Name: "record_delivery",
			Description: "Record an actually delivered order: each line adds an exact quantity to one item at delivered_at. " +
				"Give units in the item's own units, or packs to convert through the item's shop link. Repeating the identical call is a no-op; " +
				"a line older than the item's newest evidence is rejected because that evidence already includes the delivery.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{
"shop":{"type":"string","enum":["knuspr"],"default":"knuspr"},
"order_id":{"type":"string","description":"The shop's order ID"},
"delivered_at":{"type":"string","format":"date-time","description":"RFC 3339 delivery time from the order"},
"lines":{"type":"array","minItems":1,"maxItems":50,"items":{"type":"object","properties":{
"item_id":{"type":"string"},"units":{"type":"number","exclusiveMinimum":0,"maximum":100},"packs":{"type":"number","exclusiveMinimum":0,"maximum":100}},
"required":["item_id"],"additionalProperties":false}}},
"required":["order_id","delivered_at","lines"],"additionalProperties":false}`),
			Call: func(ctx context.Context, raw json.RawMessage) (any, error) {
				var d Delivery
				if err := decode(raw, &d); err != nil {
					return nil, err
				}
				d.Shop = shopOrDefault(d.Shop)
				results, err := s.RecordDelivery(ctx, d, clock().UTC())
				if err != nil {
					return nil, agentError(err)
				}
				return map[string]any{"lines": results}, nil
			},
		},
		{
			Name: "request_checks",
			Description: "Ask the owner in Telegram, right now, to check the current stock of up to 10 items. " +
				"Use it before buying items whose forecast is uncertain. The answers arrive later through the bot.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{
"item_ids":{"type":"array","minItems":1,"maxItems":10,"items":{"type":"string"}}},
"required":["item_ids"],"additionalProperties":false}`),
			Call: func(ctx context.Context, raw json.RawMessage) (any, error) {
				var a struct {
					ItemIDs []string `json:"item_ids"`
				}
				if err := decode(raw, &a); err != nil {
					return nil, err
				}
				results, err := s.RequestChecks(ctx, a.ItemIDs, clock().UTC())
				if err != nil {
					return nil, agentError(err)
				}
				return map[string]any{"items": results}, nil
			},
		},
		{
			Name: "notify_owner",
			Description: "Send a short plain-text message to the owner's Telegram chat, e.g. the cart summary. " +
				"At most 3000 characters and 10 messages per local day.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{
"text":{"type":"string","minLength":1,"maxLength":3000}},
"required":["text"],"additionalProperties":false}`),
			Call: func(ctx context.Context, raw json.RawMessage) (any, error) {
				var a struct {
					Text string `json:"text"`
				}
				if err := decode(raw, &a); err != nil {
					return nil, err
				}
				if err := s.NotifyOwner(ctx, a.Text, clock().UTC()); err != nil {
					return nil, agentError(err)
				}
				return map[string]any{"queued": true}, nil
			},
		},
	}
}
