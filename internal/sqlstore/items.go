package sqlstore

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"

	"github.com/EugenSleptsov/hoarder/internal/app"
	"github.com/EugenSleptsov/hoarder/internal/forecast"
	"github.com/EugenSleptsov/hoarder/internal/item"
)

var _ app.Store = (*DB)(nil)
var _ app.Tx = (*Tx)(nil)

func (s *DB) WithinTransaction(ctx context.Context, fn func(app.Tx) error) error {
	return s.Transaction(ctx, func(t *Tx) error { return fn(t) })
}
func (t *Tx) CreateItem(ctx context.Context, s item.State) error {
	if e := s.Validate(); e != nil {
		return e
	}
	if s.Revision != 0 {
		return errors.New("initial revision must be zero")
	}
	b, e := json.Marshal(s)
	if e != nil {
		return e
	}
	r, e := t.tx.ExecContext(ctx, "INSERT INTO items(id,revision,initial,state,model) VALUES(?,?,?,?,?) ON CONFLICT(id) DO NOTHING", s.Config.ID, 0, b, b, forecast.Version)
	if e != nil {
		return e
	}
	n, e := r.RowsAffected()
	if e != nil {
		return e
	}
	if n != 1 {
		return ErrConflict
	}
	return nil
}
func (t *Tx) LoadItem(ctx context.Context, id string) (item.State, error) {
	var b []byte
	var s item.State
	err := t.tx.QueryRowContext(ctx, "SELECT state FROM items WHERE id=?", id).Scan(&b)
	if errors.Is(err, sql.ErrNoRows) {
		return s, ErrNotFound
	}
	if err != nil {
		return s, err
	}
	if err = json.Unmarshal(b, &s); err != nil {
		return s, err
	}
	return s, s.Validate()
}
func (t *Tx) Items(ctx context.Context) ([]item.State, error) {
	rows, e := t.tx.QueryContext(ctx, "SELECT state FROM items ORDER BY id")
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	result := []item.State{}
	for rows.Next() {
		var b []byte
		var s item.State
		if e = rows.Scan(&b); e != nil {
			return nil, e
		}
		if e = json.Unmarshal(b, &s); e != nil {
			return nil, e
		}
		if e = s.Validate(); e != nil {
			return nil, e
		}
		result = append(result, s)
	}
	return result, rows.Err()
}
func (t *Tx) AppendEvent(ctx context.Context, e item.Event) error {
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	_, err = t.tx.ExecContext(ctx, "INSERT INTO events(item_id,event_id,payload) VALUES(?,?,?)", e.ItemID, e.ID, b)
	return err
}
func (t *Tx) SaveProjection(ctx context.Context, s item.State, expected uint64) error {
	if e := s.Validate(); e != nil {
		return e
	}
	if s.Revision != expected+1 || s.Revision == 0 {
		return ErrConflict
	}
	b, e := json.Marshal(s)
	if e != nil {
		return e
	}
	r, e := t.tx.ExecContext(ctx, "UPDATE items SET state=?,revision=? WHERE id=? AND revision=?", b, s.Revision, s.Config.ID, expected)
	if e != nil {
		return e
	}
	n, e := r.RowsAffected()
	if e != nil {
		return e
	}
	if n != 1 {
		return ErrConflict
	}
	return nil
}
func (t *Tx) SaveQuestion(ctx context.Context, p app.QuestionPlan) error {
	s, e := t.LoadItem(ctx, p.ItemID)
	if e != nil {
		return e
	}
	if s.Revision != p.ExpectedRevision || p.SendAt.IsZero() || p.RequestedDeadline.IsZero() {
		return ErrConflict
	}
	return t.Put(ctx, "plan", p.ItemID, p)
}
func (t *Tx) CommandReceipt(ctx context.Context, itemID, commandID string) (*app.Receipt, error) {
	var r app.Receipt
	e := t.Get(ctx, "receipt", itemID+":"+commandID, &r)
	if errors.Is(e, ErrNotFound) {
		return nil, nil
	}
	return &r, e
}
func (t *Tx) SaveReceipt(ctx context.Context, r app.Receipt) error {
	return t.Insert(ctx, "receipt", r.ItemID+":"+r.CommandID, r)
}
func (t *Tx) Enqueue(ctx context.Context, m app.OutboxMessage) error {
	if m.HouseholdID != strconv.FormatInt(t.owner, 10) {
		return errors.New("outbox ownership mismatch")
	}
	return t.Insert(ctx, "app_outbox", m.ID, m)
}

// ApplyEvent commits neither on its own nor over the network. The caller can
// atomically save the new plan, dialog, update cursor and outbox in this same Tx.
func (t *Tx) ApplyEvent(ctx context.Context, e item.Event, expected uint64) (item.State, error) {
	b, err := json.Marshal(e)
	if err != nil {
		return item.State{}, err
	}
	sum := sha256.Sum256(b)
	hash := hex.EncodeToString(sum[:])
	receipt, err := t.CommandReceipt(ctx, e.ItemID, e.ID)
	if err != nil {
		return item.State{}, err
	}
	s, err := t.LoadItem(ctx, e.ItemID)
	if err != nil {
		return s, err
	}
	if receipt != nil {
		if receipt.PayloadHash != hash {
			return s, ErrConflict
		}
		return s, nil
	}
	if s.Revision != expected {
		return s, ErrConflict
	}
	n, err := s.Apply(e)
	if err != nil {
		return s, err
	}
	if err = t.AppendEvent(ctx, e); err != nil {
		return s, err
	}
	if err = t.SaveProjection(ctx, n, expected); err != nil {
		return s, err
	}
	err = t.SaveReceipt(ctx, app.Receipt{ItemID: e.ItemID, CommandID: e.ID, PayloadHash: hash, ResultRevision: n.Revision})
	return n, err
}

func (t *Tx) Replay(ctx context.Context, id string) (item.State, error) {
	var b []byte
	var version string
	var s item.State
	err := t.tx.QueryRowContext(ctx, "SELECT initial,model FROM items WHERE id=?", id).Scan(&b, &version)
	if errors.Is(err, sql.ErrNoRows) {
		return s, ErrNotFound
	}
	if err != nil {
		return s, err
	}
	if version != forecast.Version {
		return s, errors.New("unsupported replay model version")
	}
	if err = json.Unmarshal(b, &s); err != nil {
		return s, err
	}
	rows, err := t.tx.QueryContext(ctx, "SELECT payload FROM events WHERE item_id=? ORDER BY seq", id)
	if err != nil {
		return s, err
	}
	defer rows.Close()
	for rows.Next() {
		var payload []byte
		var e item.Event
		if err = rows.Scan(&payload); err != nil {
			return s, err
		}
		if err = json.Unmarshal(payload, &e); err != nil {
			return s, err
		}
		if s, err = s.Apply(e); err != nil {
			return s, err
		}
	}
	return s, rows.Err()
}
