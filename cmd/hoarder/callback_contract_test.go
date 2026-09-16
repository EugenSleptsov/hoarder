package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/EugenSleptsov/hoarder/internal/bot"
	"github.com/EugenSleptsov/hoarder/internal/item"
	"github.com/EugenSleptsov/hoarder/internal/sqlstore"
	"github.com/EugenSleptsov/hoarder/internal/telegram"
)

func TestDaemonAcknowledgesCallbacksAfterDurableDecision(t *testing.T) {
	t.Run("accepted_rejected_and_replayed", func(t *testing.T) { exerciseDaemonCallbacks(t, false, false) })
	t.Run("ack_failure_does_not_undo_answer", func(t *testing.T) { exerciseDaemonCallbacks(t, false, true) })
}

func TestDaemonFailedCommitAcknowledgesFailureNotSuccess(t *testing.T) {
	exerciseDaemonCallbacks(t, true, false)
}

// Drive the actual run loop. Unlike the service harness, this fixture NEVER
// sends an acknowledgement itself: only production code can reach that route.
func exerciseDaemonCallbacks(t *testing.T, failCommit, failAck bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cfg := config{Owner: 123, Path: filepath.Join(t.TempDir(), "bot.db"), Token: "123:TEST",
		Schedule: bot.Config{Zone: "UTC", Hour: (time.Now().UTC().Hour() + 12) % 24, CatchUp: time.Minute}}
	probe, err := sqlstore.Open(ctx, cfg.Path, cfg.Owner)
	if err != nil {
		t.Fatal(err)
	}
	defer probe.Close()
	var last telegram.Text
	var current, saved telegram.Update
	acks := map[string]int{}
	poll := 0
	terminalMarkup := ""
	original := http.DefaultTransport
	defer func() { http.DefaultTransport = original }()
	buttonUpdate := func(id int64, callback, label string, sender int64) telegram.Update {
		data := ""
		if last.Keyboard != nil {
			for _, row := range last.Keyboard.Rows {
				for _, b := range row {
					if b.Text == label {
						data = b.Data
					}
				}
			}
		}
		if data == "" {
			t.Errorf("real daemon did not deliver button %q: %+v", label, last)
			cancel()
		}
		return telegram.Update{ID: id, Callback: &telegram.Callback{ID: callback, From: telegram.User{ID: sender},
			Message: &telegram.Message{ID: last.MessageID, Date: 1, Chat: telegram.Chat{ID: 123, Type: "private"}}, Data: data}}
	}
	http.DefaultTransport = auditRoundTrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "api.telegram.org" {
			t.Errorf("unexpected network destination %s", r.URL.Host)
			cancel()
			return nil, context.Canceled
		}
		var result any
		switch {
		case strings.HasSuffix(r.URL.Path, "/getUpdates"):
			switch poll {
			case 0:
				current = telegram.Update{ID: 1, Message: &telegram.Message{ID: 1, Date: 1, Chat: telegram.Chat{ID: 123, Type: "private"}, Text: "/add Паста"}}
			case 1:
				current = buttonUpdate(2, "foreign", "С резервом", 999)
			case 2:
				current = buttonUpdate(3, "reserve", "С резервом", 123)
			case 3:
				current = buttonUpdate(4, "stock", "Одна полная упаковка", 123)
			case 4:
				if failCommit {
					// Fail the real SQLite item INSERT, not a stubbed service result.
					db, e := sql.Open("sqlite3", cfg.Path)
					if e != nil {
						return nil, e
					}
					_, e = db.ExecContext(ctx, "CREATE TRIGGER reject_item BEFORE INSERT ON items BEGIN SELECT RAISE(ABORT,'injected item write failure'); END")
					db.Close()
					if e != nil {
						return nil, e
					}
				}
				current = buttonUpdate(5, "save", "Добавить", 123)
				saved = current
			case 5:
				current = saved
				current.ID = 6 // Same callback delivered under another update ID.
			default:
				cancel()
				return nil, context.Canceled
			}
			poll++
			result = []telegram.Update{current}
		case strings.HasSuffix(r.URL.Path, "/answerCallbackQuery"):
			var ack struct {
				ID        string `json:"callback_query_id"`
				Text      string `json:"text"`
				CacheTime int    `json:"cache_time"`
			}
			if e := json.NewDecoder(r.Body).Decode(&ack); e != nil {
				return nil, e
			}
			if current.Callback == nil || ack.ID != current.Callback.ID || ack.CacheTime != 0 {
				t.Errorf("ack does not match incoming callback: %+v", ack)
			}
			acks[ack.ID]++
			writeFailed := failCommit && current.ID == 5
			e := probe.Transaction(ctx, func(tx *sqlstore.Tx) error {
				var receipt json.RawMessage
				e := tx.Get(ctx, "update", strconv.FormatInt(current.ID, 10), &receipt)
				if writeFailed {
					if !errors.Is(e, sqlstore.ErrNotFound) {
						t.Errorf("failed write nevertheless acquired a success receipt: %v", e)
					}
					return nil
				}
				return e
			})
			if e != nil {
				t.Errorf("callback acknowledged before durable decision: %v", e)
			}
			if writeFailed && !strings.Contains(ack.Text, "не сохранён") {
				t.Errorf("failed write reported success: %q", ack.Text)
			} else if ack.ID == "foreign" && !strings.Contains(ack.Text, "Нет доступа") {
				t.Errorf("rejected callback reported success: %q", ack.Text)
			} else if !writeFailed && ack.ID != "foreign" && !strings.Contains(ack.Text, "принят") {
				t.Errorf("committed callback missing confirmation: %q", ack.Text)
			}
			if failAck && ack.ID == "save" && acks[ack.ID] == 1 {
				return &http.Response{StatusCode: 500, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"ok":false,"error_code":500}`)), Request: r}, nil
			}
			result = true
		case strings.HasSuffix(r.URL.Path, "/sendMessage"), strings.HasSuffix(r.URL.Path, "/editMessageText"):
			raw, e := io.ReadAll(r.Body)
			if e != nil {
				return nil, e
			}
			if e = json.Unmarshal(raw, &last); e != nil {
				return nil, e
			}
			if last.MessageID == 0 {
				last.MessageID = 500
			}
			if strings.Contains(last.Text, "Предмет добавлен") {
				var envelope struct {
					Markup map[string]json.RawMessage `json:"reply_markup"`
				}
				if e = json.Unmarshal(raw, &envelope); e != nil {
					return nil, e
				}
				terminalMarkup = string(envelope.Markup["inline_keyboard"])
			}
			result = telegram.Message{ID: last.MessageID, Date: 1, Chat: telegram.Chat{ID: 123, Type: "private"}}
		default:
			t.Errorf("unexpected Bot API method %s", r.URL.Path)
			cancel()
			return nil, context.Canceled
		}
		raw, e := json.Marshal(map[string]any{"ok": true, "result": result})
		if e != nil {
			return nil, e
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(raw))), Request: r}, nil
	})
	err = run(ctx, cfg, "")
	if failCommit {
		if err == nil || !strings.Contains(err.Error(), "injected item write failure") {
			t.Fatalf("did not reach injected commit failure: %v", err)
		}
	} else if !errors.Is(err, context.Canceled) {
		t.Fatalf("daemon stopped unexpectedly: %v", err)
	}
	wantSave := 2
	if failCommit {
		wantSave = 1
	}
	if acks["foreign"] != 1 || acks["reserve"] != 1 || acks["stock"] != 1 || acks["save"] != wantSave {
		t.Fatalf("production daemon omitted callback acknowledgements: %+v", acks)
	}
	if !failCommit && terminalMarkup != "[]" {
		t.Fatalf("terminal wire keyboard = %q; want explicit empty JSON array", terminalMarkup)
	}
	err = probe.Transaction(context.Background(), func(tx *sqlstore.Tx) error {
		items, e := tx.Items(context.Background())
		if e != nil {
			return e
		}
		if failCommit {
			if len(items) != 0 {
				t.Error("failed insert committed an item")
			}
		} else if len(items) != 1 || items[0].Stock != (item.Interval{Low: .875, High: 1}) || items[0].Revision != 0 {
			t.Errorf("wrong/duplicate persisted item after daemon callbacks: %+v", items)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
