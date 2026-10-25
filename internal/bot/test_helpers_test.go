package bot

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/EugenSleptsov/hoarder/internal/app"
	"github.com/EugenSleptsov/hoarder/internal/item"
	"github.com/EugenSleptsov/hoarder/internal/sqlstore"
	"github.com/EugenSleptsov/hoarder/internal/telegram"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// wireKey names a message the way Telegram does: IDs count per chat.
type wireKey struct{ chat, id int64 }

// ownerChat is the harness household's owner.
const ownerChat = 123

type wire struct {
	messages map[wireKey]telegram.Text
	clears   int
	mu       sync.Mutex
	last     map[int64]telegram.Text
	sends    int
	sent     map[int64]int
	edits    int
	acks     int
	fail     bool
	next     map[int64]int64
}

func (w *wire) serve(rw http.ResponseWriter, r *http.Request) {
	w.mu.Lock()
	defer w.mu.Unlock()
	rw.Header().Set("Content-Type", "application/json")
	method := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
	if method == "answerCallbackQuery" {
		w.acks++
		json.NewEncoder(rw).Encode(map[string]any{"ok": true, "result": true})
		return
	}
	if w.fail {
		rw.WriteHeader(500)
		json.NewEncoder(rw).Encode(map[string]any{"ok": false, "error_code": 500})
		return
	}
	var text telegram.Text
	if e := json.NewDecoder(r.Body).Decode(&text); e != nil {
		rw.WriteHeader(400)
		return
	}
	if w.messages == nil {
		w.messages, w.last, w.sent, w.next = map[wireKey]telegram.Text{}, map[int64]telegram.Text{}, map[int64]int{}, map[int64]int64{}
	}
	if method == "sendMessage" {
		w.sends++
		w.sent[text.ChatID]++
		if w.next[text.ChatID] == 0 {
			w.next[text.ChatID] = 100
		}
		w.next[text.ChatID]++
		text.MessageID = w.next[text.ChatID]
	} else if method == "editMessageReplyMarkup" {
		w.clears++
		old := w.messages[wireKey{text.ChatID, text.MessageID}]
		text.Text = old.Text
	} else if method == "editMessageText" {
		w.edits++
	} else {
		rw.WriteHeader(404)
		return
	}
	w.messages[wireKey{text.ChatID, text.MessageID}] = text
	if text.MessageID >= w.last[text.ChatID].MessageID {
		w.last[text.ChatID] = text
	}
	msg := telegram.Message{ID: text.MessageID, Date: 1, Chat: telegram.Chat{ID: text.ChatID, Type: "private"}, Text: text.Text}
	json.NewEncoder(rw).Encode(map[string]any{"ok": true, "result": msg})
}
func (w *wire) snapshot() telegram.Text { return w.snapshotIn(ownerChat) }
func (w *wire) snapshotIn(chat int64) telegram.Text {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.last[chat]
}

type harness struct {
	t        *testing.T
	db       *sqlstore.DB
	service  *Service
	api      *telegram.Client
	wire     *wire
	path     string
	now      time.Time
	sequence int64
}

var testContext = context.Background()
var testSchedule = Config{Zone: "Europe/Berlin", Hour: 21, CatchUp: 30 * time.Minute}

func newHarness(t *testing.T) *harness {
	t.Helper()
	h := &harness{t: t, path: filepath.Join(t.TempDir(), "home.db"), now: time.Date(2026, 9, 15, 18, 0, 0, 0, time.UTC), wire: &wire{}}
	srv := httptest.NewServer(http.HandlerFunc(h.wire.serve))
	t.Cleanup(srv.Close)
	endpoint, e := url.Parse(srv.URL)
	if e != nil {
		t.Fatal(e)
	}
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		copy := r.Clone(r.Context())
		u := *r.URL
		u.Scheme = endpoint.Scheme
		u.Host = endpoint.Host
		copy.URL = &u
		return srv.Client().Transport.RoundTrip(copy)
	})
	h.api, e = telegram.NewWithClient("123:TEST", &http.Client{Transport: transport, Timeout: time.Second})
	if e != nil {
		t.Fatal(e)
	}
	h.reopen()
	t.Cleanup(func() { h.db.Close() })
	return h
}
func (h *harness) reopen() {
	h.t.Helper()
	if h.db != nil {
		h.db.Close()
	}
	var e error
	h.db, e = sqlstore.Open(testContext, h.path, 123)
	if e != nil {
		h.t.Fatal(e)
	}
	h.service, e = New(testContext, h.db, testSchedule)
	if e != nil {
		h.t.Fatal(e)
	}
	h.service.SetUsername("hoarder_test_bot")
}
func (h *harness) process(u telegram.Update) string {
	h.t.Helper()
	notice, e := h.service.Handle(testContext, u, h.now)
	if u.Callback != nil {
		if err := h.api.AnswerNotice(testContext, u.Callback.ID, notice); err != nil {
			h.t.Fatal(err)
		}
	}
	if e != nil {
		h.t.Fatal(e)
	}
	if e = h.service.Flush(testContext, h.api, h.now); e != nil {
		h.t.Fatal(e)
	}
	return notice
}
func (h *harness) command(text string) { h.commandIn(ownerChat, text) }
func (h *harness) commandIn(chat int64, text string) {
	h.sequence++
	h.process(telegram.Update{ID: h.sequence, Message: &telegram.Message{ID: h.sequence, Date: h.now.Unix(), Chat: telegram.Chat{ID: chat, Type: "private"}, Text: text}})
}
func (h *harness) button(label string) telegram.Update {
	h.t.Helper()
	return h.buttonIn(ownerChat, label)
}
func (h *harness) buttonIn(chat int64, label string) telegram.Update {
	h.t.Helper()
	msg := h.wire.snapshotIn(chat)
	data := ""
	if msg.Keyboard != nil {
		for _, row := range msg.Keyboard.Rows {
			for _, b := range row {
				if b.Text == label {
					data = b.Data
				}
			}
		}
	}
	if data == "" {
		h.t.Fatalf("button %q missing from %+v", label, msg)
	}
	if len(data) > 64 {
		h.t.Fatal("oversized callback")
	}
	h.sequence++
	return telegram.Update{ID: h.sequence, Callback: &telegram.Callback{ID: strconv.FormatInt(h.sequence, 10), From: telegram.User{ID: chat}, Message: &telegram.Message{ID: msg.MessageID, Date: h.now.Unix(), Chat: telegram.Chat{ID: chat, Type: "private"}, Text: msg.Text}, Data: data}}
}
func (h *harness) press(label string) telegram.Update {
	h.t.Helper()
	return h.pressIn(ownerChat, label)
}
func (h *harness) pressIn(chat int64, label string) telegram.Update {
	h.t.Helper()
	u := h.buttonIn(chat, label)
	h.process(u)
	return u
}
func (h *harness) add(name string, reserve bool, closed, level string) {
	h.t.Helper()
	h.command("/add " + name)
	if reserve {
		h.press("С резервом")
	} else {
		h.press("Без резерва")
	}
	h.press("Указать остаток")
	h.press(closed)
	h.press(level)
	h.press("Добавить")
}
func (h *harness) state(name string) item.State {
	h.t.Helper()
	var found item.State
	e := h.db.Transaction(testContext, func(tx *sqlstore.Tx) error {
		items, e := tx.Items(testContext)
		for _, s := range items {
			if s.Config.Name == name {
				found = s
			}
		}
		return e
	})
	if e != nil || found.Config.ID == "" {
		h.t.Fatal("missing item", name, e)
	}
	return found
}
func (h *harness) plan(id string) app.QuestionPlan {
	h.t.Helper()
	var p app.QuestionPlan
	if e := h.db.Transaction(testContext, func(tx *sqlstore.Tx) error { return tx.Get(testContext, "plan", id, &p) }); e != nil {
		h.t.Fatal(e)
	}
	return p
}

func (w *wire) sendCount() int { w.mu.Lock(); defer w.mu.Unlock(); return w.sends }
func (w *wire) editCount() int { w.mu.Lock(); defer w.mu.Unlock(); return w.edits }
func (w *wire) ackCount() int  { w.mu.Lock(); defer w.mu.Unlock(); return w.acks }
func (w *wire) setFail(v bool) { w.mu.Lock(); defer w.mu.Unlock(); w.fail = v }

func (w *wire) message(id int64) telegram.Text { return w.messageIn(ownerChat, id) }
func (w *wire) messageIn(chat, id int64) telegram.Text {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.messages[wireKey{chat, id}]
}
func (w *wire) sendsTo(chat int64) int { w.mu.Lock(); defer w.mu.Unlock(); return w.sent[chat] }
