package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const testToken = "0123456789abcdef0123456789abcdef"

func testServer(t *testing.T) *httptest.Server {
	t.Helper()
	tools := []Tool{
		{Name: "echo", Description: "echo", InputSchema: json.RawMessage(`{"type":"object"}`),
			Call: func(_ context.Context, raw json.RawMessage) (any, error) {
				var a map[string]any
				if err := json.Unmarshal(raw, &a); err != nil {
					return nil, err
				}
				return map[string]any{"got": a}, nil
			}},
		{Name: "reject", InputSchema: json.RawMessage(`{}`),
			Call: func(context.Context, json.RawMessage) (any, error) { return nil, Reject("bad %s", "input") }},
		{Name: "secret", InputSchema: json.RawMessage(`{}`),
			Call: func(context.Context, json.RawMessage) (any, error) {
				return nil, errors.New("db path /private/home.db")
			}},
		{Name: "array", InputSchema: json.RawMessage(`{}`),
			Call: func(context.Context, json.RawMessage) (any, error) { return []int{1}, nil }},
		{Name: "panic", InputSchema: json.RawMessage(`{}`),
			Call: func(context.Context, json.RawMessage) (any, error) { panic("boom") }},
	}
	s, err := New(Info{Name: "hoarder", Version: "test", Instructions: "be careful"}, testToken, tools)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(s)
	t.Cleanup(srv.Close)
	return srv
}

func post(t *testing.T, srv *httptest.Server, body string, headers map[string]string) (int, map[string]any) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, srv.URL, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+testToken)
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		if v == "" {
			req.Header.Del(k)
		} else {
			req.Header.Set(k, v)
		}
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]any
	if resp.Header.Get("Content-Type") == "application/json" {
		if err = json.Unmarshal(raw, &out); err != nil {
			t.Fatal(err, string(raw))
		}
	}
	return resp.StatusCode, out
}

func TestRejectsMissingOrWrongTokenAndBrowserOrigins(t *testing.T) {
	srv := testServer(t)
	ping := `{"jsonrpc":"2.0","id":1,"method":"ping"}`
	for name, headers := range map[string]map[string]string{
		"missing": {"Authorization": ""},
		"wrong":   {"Authorization": "Bearer " + strings.Repeat("x", 32)},
		"prefix":  {"Authorization": "Bearer " + testToken[:31]},
		"scheme":  {"Authorization": "Basic " + testToken},
	} {
		if code, _ := post(t, srv, ping, headers); code != http.StatusUnauthorized {
			t.Fatal(name, code)
		}
	}
	if code, _ := post(t, srv, ping, map[string]string{"Origin": "https://evil.example"}); code != http.StatusForbidden {
		t.Fatal("browser origin accepted", code)
	}
	if code, out := post(t, srv, ping, nil); code != http.StatusOK || out["result"] == nil {
		t.Fatal(code, out)
	}
}

func TestNewRefusesWeakTokenAndBrokenTools(t *testing.T) {
	info := Info{Name: "hoarder", Version: "test"}
	if _, err := New(info, "short", nil); err == nil {
		t.Fatal("short token accepted")
	}
	tool := Tool{Name: "a", InputSchema: json.RawMessage(`{}`), Call: func(context.Context, json.RawMessage) (any, error) { return nil, nil }}
	if _, err := New(info, testToken, []Tool{tool, tool}); err == nil {
		t.Fatal("duplicate tool accepted")
	}
	tool.InputSchema = json.RawMessage(`{`)
	if _, err := New(info, testToken, []Tool{tool}); err == nil {
		t.Fatal("invalid schema accepted")
	}
}

func TestTransportRules(t *testing.T) {
	srv := testServer(t)
	req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed || resp.Header.Get("Allow") != http.MethodPost {
		t.Fatal("GET stream offered", resp.StatusCode)
	}
	ping := `{"jsonrpc":"2.0","id":1,"method":"ping"}`
	if code, _ := post(t, srv, ping, map[string]string{"Content-Type": "text/plain"}); code != http.StatusUnsupportedMediaType {
		t.Fatal(code)
	}
	if code, _ := post(t, srv, ping, map[string]string{"MCP-Protocol-Version": "1999-01-01"}); code != http.StatusBadRequest {
		t.Fatal(code)
	}
	if code, _ := post(t, srv, ping, map[string]string{"MCP-Protocol-Version": "2025-06-18"}); code != http.StatusOK {
		t.Fatal(code)
	}
	if code, _ := post(t, srv, `{"jsonrpc":"2.0","method":"notifications/initialized"}`, nil); code != http.StatusAccepted {
		t.Fatal("notification answered", code)
	}
	if code, _ := post(t, srv, `{"jsonrpc":"2.0","id":1,"result":{}}`, nil); code != http.StatusAccepted {
		t.Fatal("client response answered", code)
	}
	if code, _ := post(t, srv, `{"jsonrpc":"2.0","id":1,"method":"ping","params":"`+strings.Repeat("x", maxBody)+`"}`, nil); code != http.StatusRequestEntityTooLarge {
		t.Fatal("oversized body", code)
	}
	for body, want := range map[string]float64{
		`[{"jsonrpc":"2.0","id":1,"method":"ping"}]`: -32600,
		`{"jsonrpc":`: -32700,
		`{"jsonrpc":"1.0","id":1,"method":"ping"}`:                                   -32600,
		`{"jsonrpc":"2.0","id":{},"method":"ping"}`:                                  -32600,
		`{"jsonrpc":"2.0","id":1,"method":"nope"}`:                                   -32601,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"missing"}}`: -32602,
	} {
		code, out := post(t, srv, body, nil)
		e, _ := out["error"].(map[string]any)
		if code != http.StatusOK || e == nil || e["code"] != want {
			t.Fatal(body, code, out)
		}
	}
}

func TestInitializeNegotiatesVersionAndListsTools(t *testing.T) {
	srv := testServer(t)
	_, out := post(t, srv, `{"jsonrpc":"2.0","id":"a","method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"codex","version":"1"}}}`, nil)
	result := out["result"].(map[string]any)
	if out["id"] != "a" || result["protocolVersion"] != "2025-03-26" || result["instructions"] != "be careful" {
		t.Fatal(out)
	}
	if result["capabilities"].(map[string]any)["tools"] == nil {
		t.Fatal("tools capability missing", result)
	}
	_, out = post(t, srv, `{"jsonrpc":"2.0","id":2,"method":"initialize","params":{"protocolVersion":"2099-01-01"}}`, nil)
	if out["result"].(map[string]any)["protocolVersion"] != Versions[0] {
		t.Fatal("unknown version not answered with the newest supported one", out)
	}
	_, out = post(t, srv, `{"jsonrpc":"2.0","id":3,"method":"tools/list"}`, nil)
	tools := out["result"].(map[string]any)["tools"].([]any)
	first := tools[0].(map[string]any)
	if len(tools) != 5 || first["name"] != "echo" || first["inputSchema"].(map[string]any)["type"] != "object" {
		t.Fatal(tools)
	}
}

func TestToolResultsAndFailuresStayInsideTheResult(t *testing.T) {
	srv := testServer(t)
	call := func(name, args string) map[string]any {
		t.Helper()
		_, out := post(t, srv, `{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"`+name+`","arguments":`+args+`}}`, nil)
		if out["error"] != nil {
			t.Fatal(out)
		}
		return out["result"].(map[string]any)
	}
	ok := call("echo", `{"x":1}`)
	text := ok["content"].([]any)[0].(map[string]any)["text"].(string)
	if ok["isError"] != false || ok["structuredContent"].(map[string]any)["got"].(map[string]any)["x"] != 1.0 || !strings.Contains(text, `"x":1`) {
		t.Fatal(ok)
	}
	if empty := call("echo", `null`); empty["isError"] != false {
		t.Fatal("missing arguments were not treated as an empty object", empty)
	}
	failures := map[string]string{"reject": "bad input", "secret": "internal error", "array": "internal error", "panic": "internal error"}
	for name, want := range failures {
		r := call(name, `{}`)
		text := r["content"].([]any)[0].(map[string]any)["text"].(string)
		if r["isError"] != true || !strings.Contains(text, want) || strings.Contains(text, "/private") || r["structuredContent"] != nil {
			t.Fatal(name, r)
		}
	}
}
