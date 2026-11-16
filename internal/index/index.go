package index

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"modernc.org/sqlite"
)

// Index is the per-machine SQLite cache of a library. It is safe for
// concurrent use: reads run in parallel, writes are serialised.
type Index struct {
	db    *sql.DB
	mu    sync.Mutex // serialises write transactions
	fresh bool
}

var errStale = errors.New("index: schema version mismatch")

// ErrDamaged is returned by Check for a database that opens but whose
// structure is broken. The remedy is to delete the file and rebuild.
var ErrDamaged = errors.New("index: database is damaged")

// SQLite primary result codes that mean the file itself is unusable.
const (
	sqliteCorrupt = 11
	sqliteNotADB  = 26
)

// Open opens the index at path, creating it and its directory when missing.
// A file written by another schema version, or one that is not a usable
// database, is deleted and recreated: the index only caches what the library
// folder holds. Fresh reports whether that happened.
func Open(path string) (*Index, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("index: %w", err)
	}
	ix, err := open(path)
	if err == nil {
		return ix, nil
	}
	if !errors.Is(err, errStale) && !isUnusable(err) {
		return nil, err
	}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if rmErr := os.Remove(path + suffix); rmErr != nil && !errors.Is(rmErr, os.ErrNotExist) {
			return nil, fmt.Errorf("index: discard stale cache: %w", rmErr)
		}
	}
	return open(path)
}

func open(path string) (*Index, error) {
	// modernc applies the pragmas to every pooled connection. Transactions
	// are only used for writing, so they take the write lock when they
	// begin: a deferred one that upgrades after another process has written
	// fails at once with SQLITE_BUSY, whatever the busy timeout.
	dsn := path + "?_txlock=immediate&_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("index: open %s: %w", path, err)
	}
	ix := &Index{db: db}
	if err := ix.init(); err != nil {
		db.Close()
		return nil, err
	}
	return ix, nil
}

func (ix *Index) init() error {
	var version, objects int
	if err := ix.db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		return fmt.Errorf("index: read schema version: %w", err)
	}
	if version == schemaVersion {
		return nil
	}
	if err := ix.db.QueryRow(`SELECT count(*) FROM sqlite_master`).Scan(&objects); err != nil {
		return fmt.Errorf("index: inspect: %w", err)
	}
	if version != 0 || objects != 0 {
		return errStale
	}
	tx, err := ix.db.Begin()
	if err != nil {
		return fmt.Errorf("index: create schema: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(schema); err != nil {
		return fmt.Errorf("index: create schema: %w", err)
	}
	if _, err := tx.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, schemaVersion)); err != nil {
		return fmt.Errorf("index: create schema: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("index: create schema: %w", err)
	}
	ix.fresh = true
	return nil
}

// Check runs SQLite's structural check over the whole database. A damaged
// page otherwise only shows when a query happens to read it, and a database
// damaged past its first page opens and even takes writes. It reads every
// page (about 0.2 s per 50,000 samples), so it belongs in the background,
// not in Open. Any failure is reported as ErrDamaged.
func (ix *Index) Check() error {
	rows, err := ix.db.Query(`PRAGMA quick_check(1)`)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrDamaged, err)
	}
	defer rows.Close()
	for rows.Next() {
		var verdict string
		if err := rows.Scan(&verdict); err != nil {
			return fmt.Errorf("%w: %w", ErrDamaged, err)
		}
		if verdict != "ok" {
			return fmt.Errorf("%w: %s", ErrDamaged, verdict)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("%w: %w", ErrDamaged, err)
	}
	return nil
}

func isUnusable(err error) bool {
	var se *sqlite.Error
	if !errors.As(err, &se) {
		return false
	}
	code := se.Code() & 0xff
	return code == sqliteCorrupt || code == sqliteNotADB
}

// Fresh reports whether Open had to create an empty index, in which case the
// caller needs to fill it from the library.
func (ix *Index) Fresh() bool { return ix.fresh }

// Close checkpoints and closes the database.
func (ix *Index) Close() error {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	return ix.db.Close()
}

// Reset empties the index, for a full rebuild.
func (ix *Index) Reset() error {
	return ix.write(func(w *writer) error {
		for _, table := range []string{
			"sample", "sample_mini", "sample_tag", "tag", "sample_fts",
			"pack", "collection", "collection_item", "meta",
		} {
			if err := w.exec(`DELETE FROM ` + table); err != nil {
				return err
			}
		}
		return nil
	})
}

// writer is one write transaction with a cache of its prepared statements,
// which is what makes bulk upserts fast.
type writer struct {
	tx        *sql.Tx
	stmts     map[string]*sql.Stmt
	packNames map[string]string
	tagNames  map[string]string
}

func (ix *Index) write(fn func(w *writer) error) error {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	tx, err := ix.db.Begin()
	if err != nil {
		return fmt.Errorf("index: begin: %w", err)
	}
	w := &writer{
		tx:        tx,
		stmts:     map[string]*sql.Stmt{},
		packNames: map[string]string{},
		tagNames:  map[string]string{},
	}
	if err := fn(w); err != nil {
		tx.Rollback()
		return fmt.Errorf("index: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("index: commit: %w", err)
	}
	return nil
}

func (w *writer) stmt(query string) (*sql.Stmt, error) {
	if s, ok := w.stmts[query]; ok {
		return s, nil
	}
	s, err := w.tx.Prepare(query)
	if err != nil {
		return nil, err
	}
	w.stmts[query] = s
	return s, nil
}

func (w *writer) exec(query string, args ...any) error {
	_, err := w.execResult(query, args...)
	return err
}

func (w *writer) execResult(query string, args ...any) (sql.Result, error) {
	s, err := w.stmt(query)
	if err != nil {
		return nil, err
	}
	return s.Exec(args...)
}

func (w *writer) queryRow(query string, args ...any) (*sql.Row, error) {
	s, err := w.stmt(query)
	if err != nil {
		return nil, err
	}
	return s.QueryRow(args...), nil
}
