package sqlstore

import (
	"reflect"
	"testing"

	"github.com/EugenSleptsov/hoarder/internal/item"
)

func TestV1MigrationPreservesLegacyEventsAndReceipts(t *testing.T) {
	db, path, s := fixture(t)
	old := addition(s)
	if err := db.Transaction(ctx, func(tx *Tx) error { _, err := tx.ApplyEvent(ctx, old, 0); return err }); err != nil {
		t.Fatal(err)
	}
	if _, err := db.db.ExecContext(ctx, "PRAGMA user_version=1"); err != nil {
		t.Fatal(err)
	}
	var payload string
	if err := db.db.QueryRowContext(ctx, "SELECT payload FROM events").Scan(&payload); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if wrong, err := Open(ctx, path, 456); err == nil {
		wrong.Close()
		t.Fatal("wrong owner migrated database")
	}
	db, err := Open(ctx, path, 123)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var version int
	if err = db.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil || version != 3 {
		t.Fatal(version, err)
	}
	var actual string
	if err = db.db.QueryRowContext(ctx, "SELECT payload FROM events").Scan(&actual); err != nil || payload != actual {
		t.Fatal("rewrote legacy event", err)
	}
	if err = db.Transaction(ctx, func(tx *Tx) error {
		state, err := tx.ApplyEvent(ctx, old, 0) // Original payload hash still matches.
		if err != nil {
			return err
		}
		e := item.Event{ID: "pause", ItemID: s.Config.ID, Kind: item.Pause, At: old.At}
		state, err = tx.ApplyEvent(ctx, e, state.Revision)
		if err != nil {
			return err
		}
		replay, err := tx.Replay(ctx, s.Config.ID)
		if err == nil && !reflect.DeepEqual(state, replay) {
			t.Fatal("legacy plus control replay differs")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

func TestV2MigrationPreservesPendingDialogBytes(t *testing.T) {
	db, path, _ := fixture(t)
	raw := `{"ID":"legacy-dialog","Dialog":{"step":"closed","creating":true},"Used":false}`
	if _, err := db.db.ExecContext(ctx, "INSERT INTO records(kind,id,body) VALUES('screen','legacy-dialog',?)", raw); err != nil {
		t.Fatal(err)
	}
	if _, err := db.db.ExecContext(ctx, "PRAGMA user_version=2"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	reopened, err := Open(ctx, path, 123)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	var version int
	if err = reopened.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil || version != 3 {
		t.Fatal(version, err)
	}
	var got string
	if err = reopened.db.QueryRowContext(ctx, "SELECT body FROM records WHERE kind='screen' AND id='legacy-dialog'").Scan(&got); err != nil || got != raw {
		t.Fatal("dialog history rewritten", err)
	}
}
