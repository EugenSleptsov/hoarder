// Package bot coordinates durable Telegram conversations and independent plans.
package bot

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/EugenSleptsov/hoarder/internal/dialog"
	"github.com/EugenSleptsov/hoarder/internal/item"
	"github.com/EugenSleptsov/hoarder/internal/schedule"
	"github.com/EugenSleptsov/hoarder/internal/sqlstore"
	"github.com/EugenSleptsov/hoarder/internal/telegram"
)

type Config struct {
	Zone         string
	Hour, Minute int
	CatchUp      time.Duration
}

type Service struct {
	db       *sqlstore.DB
	daily    schedule.Daily
	cfg      Config
	username string     // The bot's @username for invitation links; may be empty.
	delivery sync.Mutex // One sender in this process; deploy one process per database.
}

type action struct {
	Bound            bool
	ExpectedRevision uint64
	Control          item.Kind
	Field            string
	Configuration    *item.Config
	Label            string
	Kind             string
	ItemID           string
	View             string
	Page             int
	IDs              []string
	Member           int64 `json:",omitempty"`
}

type screen struct {
	MenuView  string
	MenuIDs   []string
	ID        string
	Dialog    *dialog.State
	Text      string
	Actions   []action
	MessageID int64
	// ChatID is a family member's chat; zero, as on every older screen, is
	// the owner's.
	ChatID    int64 `json:",omitempty"`
	ExpiresAt time.Time
	Used      bool
}

// place is where a screen goes: a chat (zero for the owner's) and the message
// it replaces, zero for a new message.
type place struct{ Chat, Message int64 }

func (p place) fresh() place { return place{Chat: p.Chat} }
func (v screen) at() place   { return place{v.ChatID, v.MessageID} }
func (s *Service) to(chat int64) int64 {
	if chat == 0 {
		return s.db.Owner()
	}
	return chat
}

type receipt struct{ Hash, Notice string }

type intent struct {
	ItemID   string
	OpenedAt time.Time
}

func New(ctx context.Context, db *sqlstore.DB, cfg Config) (*Service, error) {
	if db == nil || cfg.CatchUp < time.Minute || cfg.CatchUp > 4*time.Hour {
		return nil, errors.New("database and catch-up window between one minute and four hours required")
	}
	daily, e := schedule.New(cfg.Zone, cfg.Hour, cfg.Minute)
	if e != nil {
		return nil, e
	}
	s := &Service{db: db, daily: daily, cfg: cfg}
	e = db.Transaction(ctx, func(tx *sqlstore.Tx) error {
		var previous Config
		e := tx.Get(ctx, "runtime", "settings", &previous)
		if errors.Is(e, sqlstore.ErrNotFound) {
			return tx.Put(ctx, "runtime", "settings", cfg)
		}
		if e != nil {
			return e
		}
		if previous != cfg {
			return errors.New("stored schedule differs; explicit rescheduling migration required")
		}
		return nil
	})
	return s, e
}

func opaqueID() (string, error) {
	var b [12]byte
	_, e := rand.Read(b[:])
	return hex.EncodeToString(b[:]), e
}
func hash(v any) (string, error) {
	b, e := json.Marshal(v)
	if e != nil {
		return "", e
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

func (s *Service) callback(ctx context.Context, tx *sqlstore.Tx, cb telegram.Callback, now time.Time) (string, error) {
	if cb.ID == "" || cb.Message == nil || cb.Message.ID <= 0 || cb.Message.Date == 0 {
		return "Нет доступа к этому вопросу.", nil
	}
	chat, allowed, e := s.sender(ctx, tx, cb.Message.Chat, cb.From.ID)
	if e != nil {
		return "", e
	}
	if !allowed {
		return "Нет доступа к этому вопросу.", nil
	}
	digest, e := hash(cb)
	if e != nil {
		return "", e
	}
	var prior receipt
	e = tx.Get(ctx, "callback", cb.ID, &prior)
	if e == nil {
		if prior.Hash != digest {
			return "Некорректный повтор ответа.", nil
		}
		return prior.Notice, nil
	}
	if !errors.Is(e, sqlstore.ErrNotFound) {
		return "", e
	}
	notice, e := s.routeCallback(ctx, tx, cb, chat, now)
	if e != nil {
		return "", e
	}
	return notice, tx.Insert(ctx, "callback", cb.ID, receipt{digest, notice})
}

func (s *Service) routeCallback(ctx context.Context, tx *sqlstore.Tx, cb telegram.Callback, chat int64, now time.Time) (string, error) {
	parts := strings.Split(cb.Data, ":")
	if len(cb.Data) > 64 || len(parts) < 3 {
		return "Некорректная кнопка.", nil
	}
	// Reject malformed protocols before inspecting ownership/expiry; those
	// checks may adopt a legacy binding or queue a real keyboard removal.
	switch parts[0] {
	case "m1":
		if len(parts) != 3 {
			return "Некорректная кнопка.", nil
		}
		id, err := hex.DecodeString(parts[1])
		n, numberErr := strconv.Atoi(parts[2])
		if err != nil || len(id) != 12 || hex.EncodeToString(id) != parts[1] || numberErr != nil || n < 0 || strconv.Itoa(n) != parts[2] {
			return "Некорректная кнопка.", nil
		}
	case "h1":
		if _, _, _, err := dialog.Parse(cb.Data); err != nil {
			return "Некорректная кнопка.", nil
		}
	default:
		return "Некорректная кнопка.", nil
	}
	var v screen
	e := tx.Get(ctx, "screen", parts[1], &v)
	if errors.Is(e, sqlstore.ErrNotFound) {
		return "Кнопка устарела. Откройте /items.", nil
	}
	if e != nil {
		return "", e
	}
	// Screen IDs are secret, but a screen still answers only in its own chat.
	if v.ChatID != chat {
		return "Кнопка устарела. Откройте /items.", nil
	}
	if v.MessageID != 0 && v.MessageID != cb.Message.ID {
		return "Кнопка устарела. Откройте /items.", nil
	}
	// An authenticated opaque callback can recover the send/commit crash window.
	v.MessageID = cb.Message.ID
	owned, err := s.ownsMessage(ctx, tx, v)
	if err != nil {
		return "", err
	}
	if !owned {
		return "В этом сообщении уже другой экран.", nil
	}
	if v.Used || !now.Before(v.ExpiresAt) {
		return "Кнопка устарела. Откройте /items.", s.retireScreen(ctx, tx, v, now)
	}
	if parts[0] == "m1" && len(parts) == 3 {
		n, err := strconv.Atoi(parts[2])
		if err != nil || n < 0 || n >= len(v.Actions) || strconv.Itoa(n) != parts[2] {
			return "Некорректная кнопка.", nil
		}
		v.Used = true
		if e = tx.Put(ctx, "screen", v.ID, v); e != nil {
			return "", e
		}
		a := v.Actions[n]
		at := v.at()
		switch a.Kind {
		case "manage", "settings", "setting", "config_preview", "archive_preview", "control", "rename":
			notice, err := s.managementCallback(ctx, tx, a, v.ID, at, now)
			if err == nil && notice != "" {
				err = s.retireScreen(ctx, tx, v, now)
			}
			return notice, err
		case "check", "bought":
			if a.Bound {
				current, err := tx.LoadItem(ctx, a.ItemID)
				if err != nil {
					return "", err
				}
				if current.Archived || current.Revision != a.ExpectedRevision {
					// Someone may have answered from another chat: show the
					// same list with current items rather than dead buttons.
					if v.MenuView != "" {
						return "Предмет уже изменён. Список обновлён.", s.menu(ctx, tx, v.MenuView, 0, v.MenuIDs, at, now)
					}
					return "Предмет изменён. Откройте /items.", s.retireScreen(ctx, tx, v, now)
				}
			}
			return "", s.beginCheck(ctx, tx, a.ItemID, a.Kind == "bought", at, now)
		case "page":
			return "", s.menu(ctx, tx, a.View, a.Page, a.IDs, at, now)
		case "view":
			return "", s.menu(ctx, tx, a.View, 0, nil, at, now)
		case "add":
			return "", s.beginAdding(ctx, tx, at, now)
		case "add_name":
			return "", s.beginNames(ctx, tx, a.Field, at, now)
		case "hide_menu":
			return "", s.suggestionScreen(ctx, tx, at, now)
		case "hide_name":
			return "Подсказка скрыта.", s.hideSuggestion(ctx, tx, a.Field, at, now)
		case "show_names":
			return "Подсказки возвращены.", s.showSuggestions(ctx, tx, a.View == "add", at, now)
		case "cancel_add":
			return "Отменено.", s.cancelAdding(ctx, tx, at, now)
		case "family", "invite", "revoke", "quiet", "remove_preview", "remove":
			return s.familyCallback(ctx, tx, a, at, now)
		default:
			return "Некорректная кнопка.", nil
		}
	}
	id, g, a, e := dialog.Parse(cb.Data)
	if e != nil || id != v.ID || v.Dialog == nil {
		return "Некорректная кнопка.", nil
	}
	d := *v.Dialog
	if d.Step == dialog.Done {
		// Replay the already committed receipt, never the domain transition.
		return "Этот ответ уже сохранён.", s.queue(ctx, tx, v.ID, now, false, "")
	}
	var active string
	if e = tx.Get(ctx, "active", d.ItemID, &active); errors.Is(e, sqlstore.ErrNotFound) {
		return "Этот вопрос уже закрыт.", s.retireScreen(ctx, tx, v, now)
	} else if e != nil {
		return "", e
	}
	if active != v.ID {
		return "Есть более новый вопрос. Откройте /items.", s.retireScreen(ctx, tx, v, now)
	}
	if !d.Creating {
		current, err := tx.LoadItem(ctx, d.ItemID)
		if err != nil {
			return "", err
		}
		if current.Archived || current.Revision != d.ItemRevision {
			return "Остаток уже обновлён. Откройте /items.", s.retireScreen(ctx, tx, v, now)
		}
	}
	next, e := d.Apply(g, a, now)
	if e != nil {
		return "Эта кнопка уже не действует.", s.queue(ctx, tx, v.ID, now, false, "")
	}
	v.Dialog = &next
	if next.Step == dialog.Done {
		if e = s.finish(ctx, tx, &v, now); e != nil {
			return "", e
		}
		if e = tx.Delete(ctx, "active", d.ItemID); e != nil {
			return "", e
		}
	}
	if e = tx.Put(ctx, "screen", v.ID, v); e != nil {
		return "", e
	}
	if e = s.queue(ctx, tx, v.ID, now, false, ""); e != nil {
		return "", e
	}
	if next.Step == dialog.Done {
		if e = s.advanceAdding(ctx, tx, v, now); e != nil {
			return "", e
		}
	}
	return "Ответ принят.", nil
}
