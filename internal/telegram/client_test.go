package telegram

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

const testToken = "123:test_token_not_a_real_secret"

func testClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	c, err := newClient(testToken, server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestSendAndCallbackEncoding(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/bot"+testToken+"/sendMessage" {
			t.Error("bad request route")
		}
		var text Text
		if err := json.NewDecoder(r.Body).Decode(&text); err != nil {
			t.Error(err)
		}
		if text.ChatID != 42 || text.Keyboard.Rows[0][0].Data != "q1:0:yes" {
			t.Error(text)
		}
		io.WriteString(w, `{"ok":true,"result":{"message_id":7,"chat":{"id":42},"text":"test"}}`)
	})
	m, err := c.Send(context.Background(), Text{ChatID: 42, Text: "test", Keyboard: &Keyboard{Rows: [][]Button{{{Text: "Да", Data: "q1:0:yes"}}}}})
	if err != nil || m.ID != 7 {
		t.Fatal(m, err)
	}
}

func TestPollingOffsetIsExplicit(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		var p struct {
			Offset  int64 `json:"offset"`
			Timeout int   `json:"timeout"`
		}
		if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
			t.Error(err)
		}
		if p.Offset != 101 || p.Timeout != 30 {
			t.Error(p)
		}
		io.WriteString(w, `{"ok":true,"result":[{"update_id":101,"callback_query":{"id":"x","from":{"id":8},"data":"q1"}}]}`)
	})
	u, err := c.Updates(context.Background(), 101)
	if err != nil || len(u) != 1 || u[0].Callback.From.ID != 8 {
		t.Fatal(u, err)
	}
	if _, err = c.Updates(context.Background(), -1); err == nil {
		t.Fatal("negative offset accepted")
	}
}

func TestRateLimitAndNotModified(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "editMessageText") {
			w.WriteHeader(400)
			io.WriteString(w, `{"ok":false,"error_code":400,"description":"Bad Request: message is not modified"}`)
		} else {
			w.WriteHeader(429)
			io.WriteString(w, `{"ok":false,"error_code":429,"parameters":{"retry_after":9}}`)
		}
	})
	_, err := c.Send(context.Background(), Text{ChatID: 42, Text: "test"})
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.RetryAfterSeconds != 9 {
		t.Fatal(err)
	}
	if err = c.Edit(context.Background(), Text{ChatID: 42, MessageID: 7, Text: "same"}); err != nil {
		t.Fatal(err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestNetworkErrorsNeverExposeToken(t *testing.T) {
	c, err := newClient(testToken, "https://api.telegram.org", &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return nil, errors.New("failed request " + r.URL.String())
	})})
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Send(context.Background(), Text{ChatID: 42, Text: "test"})
	if !errors.Is(err, ErrTransport) || strings.Contains(err.Error(), testToken) {
		t.Fatal(err)
	}
}

func TestRedirectIsNotFollowed(t *testing.T) {
	calls := 0
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		http.Redirect(w, r, "/unexpected", http.StatusTemporaryRedirect)
	})
	_, err := c.Send(context.Background(), Text{ChatID: 42, Text: "test"})
	if err == nil || calls != 1 {
		t.Fatal(calls, err)
	}
}

func TestInvalidPayloadAndOversizedResponse(t *testing.T) {
	if _, err := New("token/with?query"); err == nil {
		t.Fatal("invalid token accepted")
	}
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, strings.Repeat("x", (1<<20)+1)) })
	if _, err := c.Send(context.Background(), Text{ChatID: 42, Text: "test", Keyboard: &Keyboard{Rows: [][]Button{{{Text: "x", Data: strings.Repeat("я", 33)}}}}}); err == nil {
		t.Fatal("byte limit ignored")
	}
	if _, err := c.Send(context.Background(), Text{ChatID: 42, Text: "test"}); !errors.Is(err, ErrProtocol) {
		t.Fatal(err)
	}
}
