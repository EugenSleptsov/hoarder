// Package sqlstore provides a single-household SQLite transaction boundary.
package sqlstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"

	_ "github.com/mattn/go-sqlite3"
)

var (
	ErrNotFound = errors.New("record not found")
	ErrConflict = errors.New("revision or command conflict")
)

const schema = `
CREATE TABLE metadata(key TEXT PRIMARY KEY, value TEXT NOT NULL);
CREATE TABLE items(id TEXT PRIMARY KEY, revision INTEGER NOT NULL CHECK(revision>=0), initial BLOB NOT NULL, state BLOB NOT NULL, model TEXT NOT NULL);
CREATE TABLE events(seq INTEGER PRIMARY KEY AUTOINCREMENT, item_id TEXT NOT NULL REFERENCES items(id), event_id TEXT NOT NULL, payload BLOB NOT NULL, UNIQUE(item_id,event_id));
CREATE TRIGGER events_no_update BEFORE UPDATE ON events BEGIN SELECT RAISE(ABORT,'immutable event'); END;
CREATE TRIGGER events_no_delete BEFORE DELETE ON events BEGIN SELECT RAISE(ABORT,'immutable event'); END;
CREATE TABLE records(kind TEXT NOT NULL, id TEXT NOT NULL, body BLOB NOT NULL, PRIMARY KEY(kind,id));
PRAGMA user_version=1;`

// DB is permanently bound to one explicitly configured private chat. A different
// owner cannot accidentally open the same household database.
type DB struct {
	db    *sql.DB
	owner int64
}
type Tx struct {
	tx    *sql.Tx
	owner int64
}

func Open(ctx context.Context, path string, owner int64) (*DB, error) {
	if path == "" || owner <= 0 {
		return nil, errors.New("database path and positive owner chat ID are required")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if err = os.MkdirAll(filepath.Dir(abs), 0700); err != nil {
		return nil, err
	}
	if stat, e := os.Lstat(abs); e == nil {
		if !stat.Mode().IsRegular() {
			return nil, errors.New("database must be a regular file")
		}
	} else if !errors.Is(e, os.ErrNotExist) {
		return nil, e
	}
	f, err := os.OpenFile(abs, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = f.Chmod(0600); err != nil {
		f.Close()
		return nil, err
	}
	if err = f.Close(); err != nil {
		return nil, err
	}
	u := url.URL{Scheme: "file", Path: abs}
	q := u.Query()
	q.Set("_foreign_keys", "on")
	q.Set("_journal_mode", "WAL")
	q.Set("_synchronous", "FULL")
	q.Set("_busy_timeout", "5000")
	q.Set("_txlock", "immediate")
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite3", u.String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	s := &DB{db: db, owner: owner}
	err = s.Transaction(ctx, func(t *Tx) error {
		var version int
		if e := t.tx.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); e != nil {
			return e
		}
		if version == 0 {
			var count int
			if e := t.tx.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'").Scan(&count); e != nil {
				return e
			}
			if count != 0 {
				return errors.New("refusing unrecognised database")
			}
			if _, e := t.tx.ExecContext(ctx, schema); e != nil {
				return e
			}
			if _, e := t.tx.ExecContext(ctx, "INSERT INTO metadata(key,value) VALUES('owner',?)", strconv.FormatInt(owner, 10)); e != nil {
				return e
			}
		} else if version != 1 && version != 2 {
			return errors.New("unsupported database schema version")
		}
		var stored string
		if e := t.tx.QueryRowContext(ctx, "SELECT value FROM metadata WHERE key='owner'").Scan(&stored); e != nil {
			return e
		}
		if stored != strconv.FormatInt(owner, 10) {
			return errors.New("database belongs to a different owner")
		}
		// Schema v2 is a reader-compatibility fence: earlier binaries do not
		// understand lifecycle/configuration events and must refuse this database.
		// JSON adds optional fields; old snapshots and receipts are not rewritten.
		if version < 2 {
			_, e := t.tx.ExecContext(ctx, "PRAGMA user_version=2")
			return e
		}
		return nil
	})
	if err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}
func (s *DB) Close() error { return s.db.Close() }
func (s *DB) Owner() int64 { return s.owner }
func (s *DB) Transaction(ctx context.Context, fn func(*Tx) error) error {
	if fn == nil {
		return errors.New("transaction function required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = fn(&Tx{tx: tx, owner: s.owner}); err != nil {
		return err
	}
	return tx.Commit()
}

func (t *Tx) Get(ctx context.Context, kind, id string, out any) error {
	var b []byte
	err := t.tx.QueryRowContext(ctx, "SELECT body FROM records WHERE kind=? AND id=?", kind, id).Scan(&b)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	return json.Unmarshal(b, out)
}
func (t *Tx) Put(ctx context.Context, kind, id string, v any) error {
	b, e := json.Marshal(v)
	if e != nil {
		return e
	}
	if kind == "" || id == "" {
		return errors.New("record kind and ID required")
	}
	_, e = t.tx.ExecContext(ctx, "INSERT INTO records(kind,id,body) VALUES(?,?,?) ON CONFLICT(kind,id) DO UPDATE SET body=excluded.body", kind, id, b)
	return e
}
func (t *Tx) Insert(ctx context.Context, kind, id string, v any) error {
	b, e := json.Marshal(v)
	if e != nil {
		return e
	}
	r, e := t.tx.ExecContext(ctx, "INSERT INTO records(kind,id,body) VALUES(?,?,?) ON CONFLICT(kind,id) DO NOTHING", kind, id, b)
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
func (t *Tx) Delete(ctx context.Context, kind, id string) error {
	_, e := t.tx.ExecContext(ctx, "DELETE FROM records WHERE kind=? AND id=?", kind, id)
	return e
}
func (t *Tx) List(ctx context.Context, kind string) ([]json.RawMessage, error) {
	rows, e := t.tx.QueryContext(ctx, "SELECT body FROM records WHERE kind=? ORDER BY id", kind)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	result := []json.RawMessage{}
	for rows.Next() {
		var b []byte
		if e = rows.Scan(&b); e != nil {
			return nil, e
		}
		result = append(result, append(json.RawMessage(nil), b...))
	}
	return result, rows.Err()
}

// Backup uses SQLite's consistent VACUUM INTO snapshot; copying only the main
// database file while WAL is active would not be a safe backup.
func (s *DB) Backup(ctx context.Context, path string) error {
	abs, e := filepath.Abs(path)
	if e != nil {
		return e
	}
	f, e := os.OpenFile(abs, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return e
	}
	if e = f.Close(); e != nil {
		os.Remove(abs)
		return e
	}
	_, e = s.db.ExecContext(ctx, "VACUUM INTO ?", abs)
	if e != nil {
		os.Remove(abs)
		return fmt.Errorf("backup failed: %w", e)
	}
	return nil
}

// Offset and its corresponding update receipt are saved by the bot's transaction.
func (t *Tx) Offset(ctx context.Context) (int64, error) {
	var n int64
	e := t.Get(ctx, "runtime", "offset", &n)
	if errors.Is(e, ErrNotFound) {
		return 0, nil
	}
	return n, e
}
func (t *Tx) AdvanceOffset(ctx context.Context, updateID int64) error {
	if updateID < 0 || updateID == int64(^uint64(0)>>1) {
		return errors.New("invalid Telegram update ID")
	}
	old, e := t.Offset(ctx)
	if e != nil {
		return e
	}
	if updateID+1 > old {
		return t.Put(ctx, "runtime", "offset", updateID+1)
	}
	return nil
}
