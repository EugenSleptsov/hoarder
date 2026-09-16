package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/EugenSleptsov/hoarder/internal/bot"
	"github.com/EugenSleptsov/hoarder/internal/sqlstore"
	"github.com/EugenSleptsov/hoarder/internal/telegram"
)

type auditRoundTrip func(*http.Request) (*http.Response, error)

func (f auditRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// A high persisted offset is not valid forever: Telegram documents a random
// next update ID after at least a week without updates. Model that API contract
// without sending a single request outside this process.
func TestAdversarialDaemonPollingResetsAcknowledgedCursor(t *testing.T) {
	for _, replay := range []bool{false, true} {
		name := "restart_after_quiet_week"
		if replay {
			name = "replay_then_empty_poll_then_lower_id"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			cfg := config{Owner: 123, Path: filepath.Join(t.TempDir(), "bot.db"), Token: "123:TEST", Schedule: bot.Config{Zone: "Europe/Berlin", Hour: 21, CatchUp: 30 * time.Minute}}
			db, err := sqlstore.Open(ctx, cfg.Path, 123)
			if err != nil {
				t.Fatal(err)
			}
			svc, err := bot.New(ctx, db, cfg.Schedule)
			if err != nil {
				t.Fatal(err)
			}
			old := telegram.Update{ID: 1000, Message: &telegram.Message{ID: 1, Date: 1, Chat: telegram.Chat{ID: 999, Type: "private"}, Text: "ignored foreign message"}}
			if _, err = svc.Handle(ctx, old, time.Now().Add(-14*24*time.Hour)); err != nil {
				t.Fatal(err)
			}
			if err = db.Close(); err != nil {
				t.Fatal(err)
			}
			offsets := []int64{0, 101}
			if replay {
				offsets = []int64{0, 1001, 0, 101}
			}
			calls := 0
			original := http.DefaultTransport
			defer func() { http.DefaultTransport = original }()
			http.DefaultTransport = auditRoundTrip(func(r *http.Request) (*http.Response, error) {
				if r.URL.Host != "api.telegram.org" {
					t.Errorf("unexpected host %s", r.URL.Host)
					cancel()
					return nil, errors.New("unexpected host")
				}
				var result any
				if strings.HasSuffix(r.URL.Path, "/getUpdates") {
					var req struct {
						Offset int64 `json:"offset"`
					}
					if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
						t.Error(err)
					}
					if calls >= len(offsets) || req.Offset != offsets[calls] {
						t.Errorf("poll %d used offset %d; expected sequence %v: risks dropping lower IDs or replaying forever", calls, req.Offset, offsets)
						cancel()
						return nil, context.Canceled
					}
					i := calls
					calls++
					switch {
					case replay && i == 0:
						result = []telegram.Update{old}
					case replay && i == 1:
						result = []telegram.Update{}
					case (!replay && i == 0) || (replay && i == 2):
						result = []telegram.Update{{ID: 100, Message: &telegram.Message{ID: 2, Date: 2, Chat: telegram.Chat{ID: 123, Type: "private"}, Text: "/add Паста"}}}
					default:
						cancel()
						return nil, context.Canceled
					}
				} else if strings.HasSuffix(r.URL.Path, "/sendMessage") {
					result = telegram.Message{ID: 55, Date: 2, Chat: telegram.Chat{ID: 123, Type: "private"}}
				} else {
					t.Errorf("unexpected method %s", r.URL.Path)
					cancel()
					return nil, context.Canceled
				}
				data, err := json.Marshal(map[string]any{"ok": true, "result": result})
				if err != nil {
					return nil, err
				}
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(data))), Request: r}, nil
			})
			err = run(ctx, cfg, "")
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("run: %v", err)
			}
			if calls != len(offsets) {
				t.Fatalf("only %d/%d polls reached", calls, len(offsets))
			}
			db, err = sqlstore.Open(context.Background(), cfg.Path, 123)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			if err = db.Transaction(context.Background(), func(tx *sqlstore.Tx) error {
				var receipt json.RawMessage
				return tx.Get(context.Background(), "update", "100", &receipt)
			}); err != nil {
				t.Fatalf("new low-ID update not committed: %v", err)
			}
		})
	}
}
