package sqlstore

import (
	"errors"
	"testing"

	"github.com/mattn/go-sqlite3"
)

func TestForeignKeyRejectsOrphanEvent(t *testing.T) {
	db, _, s := fixture(t)
	event := addition(s)
	event.ItemID = "missing-item"
	err := db.Transaction(ctx, func(tx *Tx) error { return tx.AppendEvent(ctx, event) })
	var constraint sqlite3.Error
	if !errors.As(err, &constraint) || constraint.ExtendedCode != sqlite3.ErrConstraintForeignKey {
		t.Fatalf("orphan event was not rejected by the database foreign key: %v", err)
	}
	// The failure must be relational, not a stub that rejects all appends.
	if err = db.Transaction(ctx, func(tx *Tx) error { return tx.AppendEvent(ctx, addition(s)) }); err != nil {
		t.Fatalf("valid parent event rejected: %v", err)
	}
}

func TestReplayRefusesUnsupportedModelVersion(t *testing.T) {
	db, _, s := fixture(t)
	if _, err := db.db.ExecContext(ctx, "UPDATE items SET model=? WHERE id=?", "unsupported-model", s.Config.ID); err != nil {
		t.Fatal(err)
	}
	err := db.Transaction(ctx, func(tx *Tx) error { _, err := tx.Replay(ctx, s.Config.ID); return err })
	if err == nil {
		t.Fatal("replayed historical evidence with an unsupported model version")
	}
}
