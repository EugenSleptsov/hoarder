package sqlstore

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/EugenSleptsov/hoarder/internal/item"
)

var ctx = context.Background()

func fixture(t *testing.T) (*DB, string, item.State) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "state.db")
	db, e := Open(ctx, path, 123)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { db.Close() })
	s, e := item.New(item.Config{ID: "paste", Name: "Паста", WithReserve: true, ReserveUnits: 1, LeadDays: 3, MaxCheckDays: 90}, item.Interval{Low: 1.5, High: 1.5}, item.Interval{Low: .02, High: .04}, time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC))
	if e != nil {
		t.Fatal(e)
	}
	if e = db.Transaction(ctx, func(tx *Tx) error { return tx.CreateItem(ctx, s) }); e != nil {
		t.Fatal(e)
	}
	return db, path, s
}
func addition(s item.State) item.Event {
	return item.Event{ID: "purchase", ItemID: s.Config.ID, Kind: item.Addition, At: s.AsOf.Add(24 * time.Hour), Quantity: item.Interval{Low: 1, High: 1}}
}
func TestRollbackAndReplay(t *testing.T) {
	db, path, s := fixture(t)
	event := addition(s)
	boom := errors.New("injected crash")
	err := db.Transaction(ctx, func(tx *Tx) error {
		if _, e := tx.ApplyEvent(ctx, event, 0); e != nil {
			return e
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatal(err)
	}
	err = db.Transaction(ctx, func(tx *Tx) error {
		actual, e := tx.LoadItem(ctx, "paste")
		if e != nil {
			return e
		}
		if !reflect.DeepEqual(actual, s) {
			t.Fatal("projection leaked")
		}
		r, e := tx.CommandReceipt(ctx, "paste", "purchase")
		if e != nil {
			return e
		}
		if r != nil {
			t.Fatal("receipt leaked")
		}
		replay, e := tx.Replay(ctx, "paste")
		if e == nil && !reflect.DeepEqual(replay, s) {
			t.Fatal("event leaked")
		}
		return e
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = db.Transaction(ctx, func(tx *Tx) error { _, e := tx.ApplyEvent(ctx, event, 0); return e }); err != nil {
		t.Fatal(err)
	}
	db.Close()
	db, e := Open(ctx, path, 123)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	if e = db.Transaction(ctx, func(tx *Tx) error {
		actual, e := tx.LoadItem(ctx, "paste")
		if e != nil {
			return e
		}
		replay, e := tx.Replay(ctx, "paste")
		if e != nil {
			return e
		}
		if !reflect.DeepEqual(actual, replay) || actual.Config.ReserveUnits != 1 || actual.Revision != 1 {
			t.Fatalf("replay mismatch: %+v", actual)
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
}
func TestDuplicateAndConflictingCommand(t *testing.T) {
	db, _, s := fixture(t)
	e := addition(s)
	for i := 0; i < 2; i++ {
		if err := db.Transaction(ctx, func(tx *Tx) error { _, err := tx.ApplyEvent(ctx, e, 0); return err }); err != nil {
			t.Fatal(err)
		}
	}
	e.Quantity = item.Interval{Low: 2, High: 2}
	err := db.Transaction(ctx, func(tx *Tx) error { _, err := tx.ApplyEvent(ctx, e, 0); return err })
	if !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	if err = db.Transaction(ctx, func(tx *Tx) error {
		n, e := tx.LoadItem(ctx, s.Config.ID)
		if n.Revision != 1 {
			t.Fatal(n.Revision)
		}
		return e
	}); err != nil {
		t.Fatal(err)
	}
}
func TestConcurrentRevisionConflict(t *testing.T) {
	db, _, s := fixture(t)
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, id := range []string{"a", "b"} {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			event := addition(s)
			event.ID = id
			results <- db.Transaction(ctx, func(tx *Tx) error { _, e := tx.ApplyEvent(ctx, event, 0); return e })
		}(id)
	}
	wg.Wait()
	close(results)
	ok, conflict := 0, 0
	for e := range results {
		if e == nil {
			ok++
		} else if errors.Is(e, ErrConflict) {
			conflict++
		} else {
			t.Fatal(e)
		}
	}
	if ok != 1 || conflict != 1 {
		t.Fatal(ok, conflict)
	}
}
func TestOwnerSchemaAndBackup(t *testing.T) {
	db, _, s := fixture(t)
	backup := filepath.Join(t.TempDir(), "backup.db")
	if e := db.Backup(ctx, backup); e != nil {
		t.Fatal(e)
	}
	if e := db.Backup(ctx, backup); e == nil {
		t.Fatal("overwrote backup")
	}
	if other, e := Open(ctx, backup, 456); e == nil {
		other.Close()
		t.Fatal("owner mismatch accepted")
	}
	restored, e := Open(ctx, backup, 123)
	if e != nil {
		t.Fatal(e)
	}
	defer restored.Close()
	if e = restored.Transaction(ctx, func(tx *Tx) error {
		actual, e := tx.LoadItem(ctx, s.Config.ID)
		if e == nil && !reflect.DeepEqual(actual, s) {
			t.Fatal("backup mismatch")
		}
		return e
	}); e != nil {
		t.Fatal(e)
	}
}
func TestCursorIsTransactional(t *testing.T) {
	db, _, _ := fixture(t)
	boom := errors.New("rollback")
	db.Transaction(ctx, func(tx *Tx) error {
		if e := tx.AdvanceOffset(ctx, 10); e != nil {
			return e
		}
		return boom
	})
	if e := db.Transaction(ctx, func(tx *Tx) error {
		n, e := tx.Offset(ctx)
		if e != nil {
			return e
		}
		if n != 0 {
			t.Fatal(n)
		}
		if e = tx.AdvanceOffset(ctx, 10); e != nil {
			return e
		}
		return tx.AdvanceOffset(ctx, 5)
	}); e != nil {
		t.Fatal(e)
	}
	if e := db.Transaction(ctx, func(tx *Tx) error {
		n, e := tx.Offset(ctx)
		if n != 11 {
			t.Fatal(n)
		}
		return e
	}); e != nil {
		t.Fatal(e)
	}
}
func TestEventsAreImmutable(t *testing.T) {
	db, _, s := fixture(t)
	if e := db.Transaction(ctx, func(tx *Tx) error { _, e := tx.ApplyEvent(ctx, addition(s), 0); return e }); e != nil {
		t.Fatal(e)
	}
	for _, query := range []string{"UPDATE events SET event_id='changed'", "DELETE FROM events"} {
		if _, e := db.db.ExecContext(ctx, query); e == nil {
			t.Fatal("mutable event")
		}
	}
}
func TestRecordsAndIncompatibleSchema(t *testing.T) {
	db, path, _ := fixture(t)
	if e := db.Transaction(ctx, func(tx *Tx) error {
		if e := tx.Insert(ctx, "dialog", "id", map[string]int{"generation": 1}); e != nil {
			return e
		}
		if e := tx.Insert(ctx, "dialog", "id", 0); !errors.Is(e, ErrConflict) {
			t.Fatal(e)
		}
		var v map[string]int
		if e := tx.Get(ctx, "dialog", "id", &v); e != nil {
			return e
		}
		if v["generation"] != 1 {
			t.Fatal(v)
		}
		rows, e := tx.List(ctx, "dialog")
		if e != nil {
			return e
		}
		if len(rows) != 1 {
			t.Fatal(rows)
		}
		return tx.Delete(ctx, "dialog", "id")
	}); e != nil {
		t.Fatal(e)
	}
	if _, e := db.db.ExecContext(ctx, "PRAGMA user_version=999"); e != nil {
		t.Fatal(e)
	}
	db.Close()
	if unsupported, e := Open(ctx, path, 123); e == nil {
		unsupported.Close()
		t.Fatal("future schema accepted")
	}
}
