package telegram

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAdversarialPollingLargeValidUpdatesDoesNotPoisonBatch(t *testing.T) {
	text := strings.Repeat("界", 4096)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Limit int `json:"limit"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		count := request.Limit
		if count == 0 {
			count = 100
		}
		updates := make([]Update, count)
		for i := range updates {
			updates[i] = Update{ID: int64(i + 1), Message: &Message{ID: int64(i + 1), Date: 1, Chat: Chat{ID: 999, Type: "private"}, Text: text}}
		}
		if err := json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": updates}); err != nil {
			t.Error(err)
		}
	}))
	defer server.Close()
	client, err := newClient("123:TEST", server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	updates, err := client.Updates(context.Background(), 0)
	if err != nil {
		t.Fatalf("legal long messages poison polling: %v", err)
	}
	if len(updates) == 0 || updates[0].Message.Text != text {
		t.Fatal("update data was truncated")
	}
}
