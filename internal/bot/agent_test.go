package bot

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/EugenSleptsov/hoarder/internal/item"
	"github.com/EugenSleptsov/hoarder/internal/mcp"
	"github.com/EugenSleptsov/hoarder/internal/sqlstore"
)

func units(v float64) *float64 { return &v }

func (h *harness) needs(until time.Time, paused bool) map[string]Need {
	h.t.Helper()
	list, err := h.service.ShoppingNeeds(testContext, h.now, until, paused)
	if err != nil {
		h.t.Fatal(err)
	}
	out := map[string]Need{}
	for _, n := range list {
		out[n.Name] = n
	}
	return out
}

func (h *harness) deliver(d Delivery) []LineResult {
	h.t.Helper()
	results, err := h.service.RecordDelivery(testContext, d, h.now)
	if err != nil {
		h.t.Fatal(err)
	}
	if err = h.service.Flush(testContext, h.api, h.now); err != nil {
		h.t.Fatal(err)
	}
	return results
}

func TestShoppingNeedsEvaluateOnlyTheItemAtTheRequestedMoment(t *testing.T) {
	h := newHarness(t)
	h.add("Паста", false, "Одна", "Полная")
	soon := h.needs(h.now.Add(10*24*time.Hour), false)["Паста"]
	later := h.needs(h.now.Add(40*24*time.Hour), false)["Паста"]
	if soon.NeededByUntil || *soon.MinUnitsToCover != 0 || soon.RecommendedNow {
		t.Fatal("full stock needed within ten days", soon)
	}
	if !later.NeededByUntil || later.MinUnitsToCover == nil || *later.MinUnitsToCover < 1 {
		t.Fatal("projected shortage not reported", later)
	}
	if later.StockNow != h.state("Паста").Stock || later.StockAtUntil.High >= later.StockNow.High || len(later.Links) != 0 {
		t.Fatal(later)
	}
	h.add("Мыло", true, "Нет", "25%")
	h.add("Бумага", false, "Нет", "Пусто")
	again := h.needs(h.now.Add(40*24*time.Hour), false)
	if !reflect.DeepEqual(again["Паста"], later) {
		t.Fatal("another item changed this item's need", again["Паста"], later)
	}
	if !again["Мыло"].RecommendedNow || !again["Мыло"].NeededByUntil {
		t.Fatal("low stock not recommended", again["Мыло"])
	}
	manage(h, "Мыло")
	h.press("Приостановить опросы")
	if _, listed := h.needs(h.now, false)["Мыло"]; listed {
		t.Fatal("paused item listed by default")
	}
	if paused := h.needs(h.now, true)["Мыло"]; !paused.Paused || paused.RecommendedNow {
		t.Fatal("paused item hidden from include_paused or still recommended", paused)
	}
	if _, err := h.service.ShoppingNeeds(testContext, h.now, h.now.Add(-time.Hour), false); err == nil {
		t.Fatal("past horizon accepted")
	}
}

func TestMinimumCoverIsExactlyTheSmallestSufficientAddition(t *testing.T) {
	h := newHarness(t)
	h.add("Паста", true, "Нет", "25%")
	until := h.now.Add(20 * 24 * time.Hour)
	n := h.needs(until, false)["Паста"]
	min := *n.MinUnitsToCover
	if min < 1 {
		t.Fatal(n)
	}
	// Deliveries at the evaluation moment reproduce the probe exactly.
	if min > 1 {
		h.deliver(Delivery{Shop: ShopKnuspr, OrderID: "short", DeliveredAt: h.now, Lines: []DeliveryLine{{ItemID: n.ItemID, Units: units(float64(min - 1))}}})
		if !h.needs(until, false)["Паста"].NeededByUntil {
			t.Fatal("one unit fewer than the minimum was sufficient")
		}
		h.deliver(Delivery{Shop: ShopKnuspr, OrderID: "rest", DeliveredAt: h.now, Lines: []DeliveryLine{{ItemID: n.ItemID, Units: units(1)}}})
	} else {
		h.deliver(Delivery{Shop: ShopKnuspr, OrderID: "all", DeliveredAt: h.now, Lines: []DeliveryLine{{ItemID: n.ItemID, Units: units(1)}}})
	}
	if covered := h.needs(until, false)["Паста"]; covered.NeededByUntil || *covered.MinUnitsToCover != 0 {
		t.Fatal("the reported minimum did not cover the horizon", covered)
	}
}

func TestDeliveryAddsExactlyOnceAndClosesTheRecommendation(t *testing.T) {
	h := newHarness(t)
	h.add("Паста", true, "Нет", "25%")
	h.add("Бумага", true, "Одна", "Полная")
	other, otherPlan := h.state("Бумага"), h.plan(h.state("Бумага").Config.ID)
	before := h.state("Паста")
	h.now = h.now.Add(24 * time.Hour)
	delivered := h.now.Add(-2 * time.Hour)
	order := Delivery{Shop: ShopKnuspr, OrderID: "K-1001", DeliveredAt: delivered, Lines: []DeliveryLine{{ItemID: before.Config.ID, Units: units(2)}}}
	r := h.deliver(order)[0]
	after := h.state("Паста")
	projected, _ := before.Project(delivered)
	if r.Status != "recorded" || r.Units != 2 || r.RecommendedNow || after.Added != 2 || after.Samples != 0 ||
		after.Stock != (item.Interval{Low: projected.Low + 2, High: projected.High + 2}) || !after.AsOf.Equal(delivered) {
		t.Fatal("delivery was not an exact addition at its time", r, after)
	}
	h.command("/shopping")
	if !strings.Contains(h.wire.snapshot().Text, "(рекомендации): 0") {
		t.Fatal("recommendation remained open", h.wire.snapshot().Text)
	}
	if again := h.deliver(order)[0]; again.Status != "already_recorded" || !reflect.DeepEqual(h.state("Паста"), after) {
		t.Fatal("repeated delivery changed stock", again)
	}
	changed := order
	changed.Lines = []DeliveryLine{{ItemID: before.Config.ID, Units: units(3)}}
	if c := h.deliver(changed)[0]; c.Status != "rejected" || !reflect.DeepEqual(h.state("Паста"), after) {
		t.Fatal("contradicting report of the same order accepted", c)
	}
	if !reflect.DeepEqual(other, h.state("Бумага")) || !reflect.DeepEqual(otherPlan, h.plan(other.Config.ID)) {
		t.Fatal("delivery touched another item")
	}
	if e := h.db.Transaction(testContext, func(tx *sqlstore.Tx) error {
		replay, err := tx.Replay(testContext, after.Config.ID)
		if err == nil && !reflect.DeepEqual(replay, after) {
			t.Fatal("replay differs from projection")
		}
		return err
	}); e != nil {
		t.Fatal(e)
	}
}

func TestDeliveryPacksUseTheShopLinkAndNewerEvidenceWins(t *testing.T) {
	h := newHarness(t)
	h.add("Бумага", true, "Одна", "50%")
	id := h.state("Бумага").Config.ID
	h.now = h.now.Add(time.Hour)
	if r := h.deliver(Delivery{Shop: ShopKnuspr, OrderID: "P1", DeliveredAt: h.now, Lines: []DeliveryLine{{ItemID: id, Packs: units(1)}}})[0]; r.Status != "rejected" || !strings.Contains(r.Reason, "link") {
		t.Fatal("packs converted without a link", r)
	}
	if _, err := h.service.SetShopLink(testContext, ShopLink{ItemID: id, Shop: ShopKnuspr, ProductID: "12345", ProductName: "Toilettenpapier 16 Rollen", UnitsPerPack: 2}, h.now); err != nil {
		t.Fatal(err)
	}
	before := h.state("Бумага")
	if r := h.deliver(Delivery{Shop: ShopKnuspr, OrderID: "P1", DeliveredAt: h.now, Lines: []DeliveryLine{{ItemID: id, Packs: units(1.5)}}})[0]; r.Status != "recorded" || r.Units != 3 {
		t.Fatal(r)
	}
	projected, _ := before.Project(h.now)
	if got := h.state("Бумага"); got.Stock != (item.Interval{Low: projected.Low + 3, High: projected.High + 3}) {
		t.Fatal("pack conversion", got.Stock, projected)
	}
	links := h.needs(h.now, false)["Бумага"].Links
	if len(links) != 1 || links[0].ProductID != "12345" || links[0].UnitsPerPack != 2 {
		t.Fatal(links)
	}
	// The owner counts stock after the delivery; reporting the delivery later must not double count.
	h.now = h.now.Add(2 * time.Hour)
	h.command("/items")
	h.press("Бумага")
	h.press("Две")
	h.press("Полная")
	counted := h.state("Бумага")
	late := Delivery{Shop: ShopKnuspr, OrderID: "P2", DeliveredAt: h.now.Add(-time.Hour), Lines: []DeliveryLine{{ItemID: id, Units: units(2)}}}
	if r := h.deliver(late)[0]; r.Status != "rejected" || !strings.Contains(r.Reason, "newer observation") || !reflect.DeepEqual(h.state("Бумага"), counted) {
		t.Fatal("delivery older than a snapshot was added", r)
	}
	if removed, err := h.service.RemoveShopLink(testContext, id, ShopKnuspr); err != nil || !removed {
		t.Fatal(removed, err)
	}
	if len(h.needs(h.now, false)["Бумага"].Links) != 0 {
		t.Fatal("link not removed")
	}
}

func TestDeliveryValidationRejectsWholeRequestsAndUnknownLines(t *testing.T) {
	h := newHarness(t)
	h.add("Паста", true, "Нет", "25%")
	h.add("Мыло", false, "Нет", "50%")
	id := h.state("Паста").Config.ID
	manage(h, "Мыло")
	h.press("Убрать в архив")
	h.press("Подтверждаю архивирование")
	archived := h.state("Мыло")
	line := []DeliveryLine{{ItemID: id, Units: units(1)}}
	for name, d := range map[string]Delivery{
		"future":    {Shop: ShopKnuspr, OrderID: "A", DeliveredAt: h.now.Add(time.Hour), Lines: line},
		"no time":   {Shop: ShopKnuspr, OrderID: "A", Lines: line},
		"order id":  {Shop: ShopKnuspr, OrderID: "a b", DeliveredAt: h.now, Lines: line},
		"shop":      {Shop: "other", OrderID: "A", DeliveredAt: h.now, Lines: line},
		"empty":     {Shop: ShopKnuspr, OrderID: "A", DeliveredAt: h.now},
		"duplicate": {Shop: ShopKnuspr, OrderID: "A", DeliveredAt: h.now, Lines: append(line, line...)},
		"both":      {Shop: ShopKnuspr, OrderID: "A", DeliveredAt: h.now, Lines: []DeliveryLine{{ItemID: id, Units: units(1), Packs: units(1)}}},
		"neither":   {Shop: ShopKnuspr, OrderID: "A", DeliveredAt: h.now, Lines: []DeliveryLine{{ItemID: id}}},
		"zero":      {Shop: ShopKnuspr, OrderID: "A", DeliveredAt: h.now, Lines: []DeliveryLine{{ItemID: id, Units: units(0)}}},
		"huge":      {Shop: ShopKnuspr, OrderID: "A", DeliveredAt: h.now, Lines: []DeliveryLine{{ItemID: id, Units: units(101)}}},
	} {
		var invalid RequestError
		if _, err := h.service.RecordDelivery(testContext, d, h.now); !errors.As(err, &invalid) {
			t.Fatal(name, err)
		}
	}
	if h.state("Паста").Revision != 0 {
		t.Fatal("invalid request changed stock")
	}
	results := h.deliver(Delivery{Shop: ShopKnuspr, OrderID: "B", DeliveredAt: h.now, Lines: []DeliveryLine{
		{ItemID: "missing", Units: units(1)}, {ItemID: archived.Config.ID, Units: units(1)}, {ItemID: id, Units: units(1)}}})
	if results[0].Status != "rejected" || results[1].Status != "rejected" || results[2].Status != "recorded" || !reflect.DeepEqual(h.state("Мыло"), archived) {
		t.Fatal(results)
	}
}

func TestRequestChecksAskNowAndNotificationsAreCapped(t *testing.T) {
	h := newHarness(t)
	h.add("Паста", true, "Нет", "25%")
	id := h.state("Паста").Config.ID
	sends := h.wire.sendCount()
	results, err := h.service.RequestChecks(testContext, []string{id, "missing"}, h.now)
	if err != nil || results[0].Status != "sent" || results[1].Status != "rejected" {
		t.Fatal(results, err)
	}
	if err = h.service.Flush(testContext, h.api, h.now); err != nil {
		t.Fatal(err)
	}
	if h.wire.sendCount() != sends+1 || !strings.Contains(h.wire.snapshot().Text, "закрытых стандартных упаковок") {
		t.Fatal("check question not sent", h.wire.snapshot())
	}
	h.press("Одна")
	h.press("50%")
	if h.state("Паста").Revision != 1 {
		t.Fatal("requested check answer not recorded")
	}
	for _, ids := range [][]string{nil, {id, id}, make([]string, 11)} {
		if _, err = h.service.RequestChecks(testContext, ids, h.now); err == nil {
			t.Fatal("invalid check request accepted", ids)
		}
	}
	for i := 0; i < maxNotifications; i++ {
		if err = h.service.NotifyOwner(testContext, "Корзина Knuspr готова: 3 товара", h.now); err != nil {
			t.Fatal(i, err)
		}
	}
	if err = h.service.Flush(testContext, h.api, h.now); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(h.wire.snapshot().Text, "Корзина Knuspr готова") {
		t.Fatal(h.wire.snapshot())
	}
	var invalid RequestError
	if err = h.service.NotifyOwner(testContext, "ещё", h.now); !errors.As(err, &invalid) {
		t.Fatal("daily cap not enforced", err)
	}
	if err = h.service.NotifyOwner(testContext, " ", h.now.Add(24*time.Hour)); !errors.As(err, &invalid) {
		t.Fatal("blank message accepted", err)
	}
	if err = h.service.NotifyOwner(testContext, "завтра", h.now.Add(24*time.Hour)); err != nil {
		t.Fatal("cap is not per local day", err)
	}
}

func TestAgentToolsOverMCP(t *testing.T) {
	h := newHarness(t)
	h.add("Паста", true, "Нет", "25%")
	id := h.state("Паста").Config.ID
	token := strings.Repeat("t", 32)
	server, err := mcp.New(mcp.Info{Name: "hoarder", Version: "test", Instructions: AgentInstructions}, token, h.service.AgentTools(func() time.Time { return h.now }))
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(server)
	t.Cleanup(srv.Close)
	call := func(body string) map[string]any {
		t.Helper()
		req, _ := http.NewRequest(http.MethodPost, srv.URL, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		resp, err := srv.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		var out map[string]any
		if err = json.Unmarshal(raw, &out); err != nil {
			t.Fatal(err, string(raw))
		}
		if out["error"] != nil {
			t.Fatal(out)
		}
		return out["result"].(map[string]any)
	}
	tool := func(name, args string) map[string]any {
		t.Helper()
		return call(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"` + name + `","arguments":` + args + `}}`)
	}
	listed := call(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)["tools"].([]any)
	names := []string{}
	for _, l := range listed {
		names = append(names, l.(map[string]any)["name"].(string))
	}
	if strings.Join(names, ",") != "shopping_needs,set_shop_link,remove_shop_link,record_delivery,request_checks,notify_owner" {
		t.Fatal(names)
	}
	needs := tool("shopping_needs", `{"horizon_days":7}`)
	items := needs["structuredContent"].(map[string]any)["items"].([]any)
	if needs["isError"] != false || len(items) != 1 || items[0].(map[string]any)["needed_by_until"] != true {
		t.Fatal(needs)
	}
	if first := items[0].(map[string]any); first["full_set_units"] != 1.0 || first["units_to_full_set"].(float64) < first["min_units_to_cover"].(float64) {
		t.Fatal("cart size missing from the tool result", first)
	}
	for args, want := range map[string]string{
		`{}`: "exactly one",
		`{"horizon_days":1,"until":"2026-09-16T00:00:00Z"}`: "exactly one",
		`{"horizon_days":61}`:                               "between 0 and 60",
		`{"horizon":1}`:                                     "unknown field",
	} {
		r := tool("shopping_needs", args)
		if r["isError"] != true || !strings.Contains(r["content"].([]any)[0].(map[string]any)["text"].(string), want) {
			t.Fatal(args, r)
		}
	}
	if r := tool("set_shop_link", `{"item_id":"`+id+`","product_id":"777","product_name":"Zahnpasta","units_per_pack":1}`); r["isError"] != false {
		t.Fatal(r)
	}
	if r := tool("set_shop_link", `{"item_id":"`+id+`","product_id":"777","product_name":"Zahnpasta","units_per_pack":1,"updated_at":"2026-01-01T00:00:00Z"}`); r["isError"] != true {
		t.Fatal("client-set timestamp accepted", r)
	}
	h.now = h.now.Add(time.Hour)
	delivery := tool("record_delivery", `{"order_id":"K-7","delivered_at":"`+h.now.Format(time.RFC3339)+`","lines":[{"item_id":"`+id+`","packs":2}]}`)
	line := delivery["structuredContent"].(map[string]any)["lines"].([]any)[0].(map[string]any)
	if line["status"] != "recorded" || line["units"] != 2.0 || h.state("Паста").Added != 2 {
		t.Fatal(delivery)
	}
	if r := tool("record_delivery", `{"order_id":"K-8","delivered_at":"`+h.now.Add(time.Hour).Format(time.RFC3339)+`","lines":[{"item_id":"`+id+`","units":1}]}`); r["isError"] != true {
		t.Fatal("future delivery accepted", r)
	}
	if r := tool("notify_owner", `{"text":"Корзина готова"}`); r["isError"] != false {
		t.Fatal(r)
	}
	if r := tool("request_checks", `{"item_ids":["`+id+`"]}`); r["isError"] != false {
		t.Fatal(r)
	}
	if r := tool("remove_shop_link", `{"item_id":"`+id+`"}`); r["structuredContent"].(map[string]any)["removed"] != true {
		t.Fatal(r)
	}
}
