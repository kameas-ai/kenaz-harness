package migrations

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// fakeDB is a minimalist in-memory Executor for migration tests. It is
// not a SQL engine — it only understands the specific shapes of SQL
// statements used by the migration framework:
//
//   - CREATE TABLE IF NOT EXISTS <name> (...)        // recorded as a "table exists"
//   - CREATE INDEX IF NOT EXISTS <name> ...          // recorded as an "index exists"
//   - DROP TABLE IF EXISTS <name>                    // removed
//   - DROP INDEX IF EXISTS <name>                    // removed
//   - INSERT INTO harness_migrations (...) VALUES (...)
//   - SELECT ... FROM harness_migrations ORDER BY rowid
//   - INSERT INTO harness_meta (...) ON CONFLICT DO NOTHING
//
// Anything else returns an error. Migrations under test that need richer
// SQL should use the libSQL-backed integration tests (WP01) instead.
type fakeDB struct {
	mu          sync.Mutex
	tables      map[string]bool
	indexes     map[string]bool
	ledger      []LedgerEntry
	meta        map[string]string // install_id -> created_at
	failOnExec  func(query string) error
	closed      bool
	failedTxs   int
	committedTx int
}

func newFakeDB() *fakeDB {
	return &fakeDB{
		tables:  map[string]bool{},
		indexes: map[string]bool{},
		meta:    map[string]string{},
	}
}

// Snapshot returns a deep copy of the visible state for assertions.
type fakeSnapshot struct {
	Tables  map[string]bool
	Indexes map[string]bool
	Ledger  []LedgerEntry
	Meta    map[string]string
}

func (f *fakeDB) Snapshot() fakeSnapshot {
	f.mu.Lock()
	defer f.mu.Unlock()
	t := make(map[string]bool, len(f.tables))
	for k, v := range f.tables {
		t[k] = v
	}
	idx := make(map[string]bool, len(f.indexes))
	for k, v := range f.indexes {
		idx[k] = v
	}
	led := make([]LedgerEntry, len(f.ledger))
	copy(led, f.ledger)
	m := make(map[string]string, len(f.meta))
	for k, v := range f.meta {
		m[k] = v
	}
	return fakeSnapshot{Tables: t, Indexes: idx, Ledger: led, Meta: m}
}

// WriteTx implements Executor.
func (f *fakeDB) WriteTx(ctx context.Context, fn func(tx WriteTx) error) error {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	f.mu.Lock()
	if f.closed {
		f.mu.Unlock()
		return errors.New("fakeDB closed")
	}
	// snapshot the state for transactional rollback.
	snap := f.cloneLocked()
	f.mu.Unlock()

	tx := &fakeTx{db: f}
	if err := fn(tx); err != nil {
		f.mu.Lock()
		// roll back to snapshot
		f.tables = snap.tables
		f.indexes = snap.indexes
		f.ledger = snap.ledger
		f.meta = snap.meta
		f.failedTxs++
		f.mu.Unlock()
		return err
	}
	f.mu.Lock()
	f.committedTx++
	f.mu.Unlock()
	return nil
}

type fakeState struct {
	tables  map[string]bool
	indexes map[string]bool
	ledger  []LedgerEntry
	meta    map[string]string
}

func (f *fakeDB) cloneLocked() fakeState {
	t := make(map[string]bool, len(f.tables))
	for k, v := range f.tables {
		t[k] = v
	}
	i := make(map[string]bool, len(f.indexes))
	for k, v := range f.indexes {
		i[k] = v
	}
	l := make([]LedgerEntry, len(f.ledger))
	copy(l, f.ledger)
	m := make(map[string]string, len(f.meta))
	for k, v := range f.meta {
		m[k] = v
	}
	return fakeState{tables: t, indexes: i, ledger: l, meta: m}
}

func (f *fakeDB) QueryRow(ctx context.Context, query string, args ...any) Row {
	rows, err := f.Query(ctx, query, args...)
	if err != nil {
		return &errRow{err: err}
	}
	if !rows.Next() {
		_ = rows.Close()
		return &errRow{err: errNoRows}
	}
	return &queryRowRows{rows: rows}
}

var errNoRows = errors.New("fakeDB: no rows")

type errRow struct{ err error }

func (r *errRow) Scan(dest ...any) error { return r.err }

type queryRowRows struct{ rows Rows }

func (r *queryRowRows) Scan(dest ...any) error {
	defer r.rows.Close()
	return r.rows.Scan(dest...)
}

func (f *fakeDB) Query(ctx context.Context, query string, args ...any) (Rows, error) {
	q := normalizeQuery(query)
	switch {
	case strings.HasPrefix(q, "select version, id, applied_at, content_hash, owning_mission, action, rolled_back_from_version from harness_migrations"):
		f.mu.Lock()
		defer f.mu.Unlock()
		// rows preserved in insertion order (rowid asc).
		entries := make([]LedgerEntry, len(f.ledger))
		copy(entries, f.ledger)
		return &fakeLedgerRows{entries: entries}, nil
	}
	return nil, fmt.Errorf("fakeDB: unsupported query: %q", query)
}

type fakeLedgerRows struct {
	entries []LedgerEntry
	idx     int
	cur     LedgerEntry
	closed  bool
}

func (r *fakeLedgerRows) Next() bool {
	if r.closed {
		return false
	}
	if r.idx >= len(r.entries) {
		return false
	}
	r.cur = r.entries[r.idx]
	r.idx++
	return true
}

func (r *fakeLedgerRows) Scan(dest ...any) error {
	if len(dest) != 7 {
		return fmt.Errorf("fakeLedgerRows.Scan: want 7 dests, got %d", len(dest))
	}
	*dest[0].(*int) = r.cur.Version
	*dest[1].(*string) = r.cur.ID
	*dest[2].(*string) = r.cur.AppliedAt
	*dest[3].(*string) = r.cur.ContentHash
	*dest[4].(*string) = r.cur.OwningMission
	*dest[5].(*string) = string(r.cur.Action)
	if r.cur.RolledBackFromVersion != nil {
		*dest[6].(*any) = int64(*r.cur.RolledBackFromVersion)
	} else {
		*dest[6].(*any) = nil
	}
	return nil
}

func (r *fakeLedgerRows) Close() error { r.closed = true; return nil }
func (r *fakeLedgerRows) Err() error   { return nil }

type fakeTx struct {
	db *fakeDB
}

func (t *fakeTx) Exec(ctx context.Context, query string, args ...any) (Result, error) {
	if t.db.failOnExec != nil {
		if err := t.db.failOnExec(query); err != nil {
			return nil, err
		}
	}
	q := normalizeQuery(query)
	switch {
	case strings.HasPrefix(q, "create table if not exists harness_meta"):
		t.db.mu.Lock()
		t.db.tables["harness_meta"] = true
		t.db.mu.Unlock()
		return fakeResult{}, nil
	case strings.HasPrefix(q, "create table if not exists harness_migrations"):
		t.db.mu.Lock()
		t.db.tables["harness_migrations"] = true
		t.db.mu.Unlock()
		return fakeResult{}, nil
	case strings.HasPrefix(q, "create table if not exists "):
		// generic CREATE TABLE
		name := extractObjectName(q, "create table if not exists ")
		t.db.mu.Lock()
		t.db.tables[name] = true
		t.db.mu.Unlock()
		return fakeResult{}, nil
	case strings.HasPrefix(q, "create table "):
		// generic CREATE TABLE without IF NOT EXISTS
		name := extractObjectName(q, "create table ")
		t.db.mu.Lock()
		t.db.tables[name] = true
		t.db.mu.Unlock()
		return fakeResult{}, nil
	case strings.HasPrefix(q, "create index if not exists "):
		name := extractObjectName(q, "create index if not exists ")
		t.db.mu.Lock()
		t.db.indexes[name] = true
		t.db.mu.Unlock()
		return fakeResult{}, nil
	case strings.HasPrefix(q, "drop table if exists "):
		name := strings.TrimSpace(strings.TrimPrefix(q, "drop table if exists "))
		t.db.mu.Lock()
		delete(t.db.tables, name)
		t.db.mu.Unlock()
		return fakeResult{}, nil
	case strings.HasPrefix(q, "drop index if exists "):
		name := strings.TrimSpace(strings.TrimPrefix(q, "drop index if exists "))
		t.db.mu.Lock()
		delete(t.db.indexes, name)
		t.db.mu.Unlock()
		return fakeResult{}, nil
	case strings.HasPrefix(q, "insert into harness_migrations"):
		// args: version, id, applied_at, content_hash, owning_mission, action, rolled_back_from_version
		if len(args) != 7 {
			return nil, fmt.Errorf("fakeDB: harness_migrations insert args=%d", len(args))
		}
		var rbFrom *int
		if v, ok := args[6].(int); ok {
			vv := v
			rbFrom = &vv
		} else if v, ok := args[6].(int64); ok {
			vv := int(v)
			rbFrom = &vv
		}
		entry := LedgerEntry{
			Version:               args[0].(int),
			ID:                    args[1].(string),
			AppliedAt:             args[2].(string),
			ContentHash:           args[3].(string),
			OwningMission:         args[4].(string),
			Action:                LedgerAction(args[5].(string)),
			RolledBackFromVersion: rbFrom,
		}
		t.db.mu.Lock()
		t.db.ledger = append(t.db.ledger, entry)
		t.db.mu.Unlock()
		return fakeResult{}, nil
	case strings.HasPrefix(q, "insert into harness_meta"):
		if len(args) < 2 {
			return nil, fmt.Errorf("fakeDB: harness_meta insert args=%d", len(args))
		}
		t.db.mu.Lock()
		if _, exists := t.db.meta[args[0].(string)]; !exists {
			t.db.meta[args[0].(string)] = args[1].(string)
		}
		t.db.mu.Unlock()
		return fakeResult{}, nil
	}
	return nil, fmt.Errorf("fakeDB: unsupported exec %q", query)
}

func (t *fakeTx) QueryRow(ctx context.Context, query string, args ...any) Row {
	return t.db.QueryRow(ctx, query, args...)
}

func (t *fakeTx) Query(ctx context.Context, query string, args ...any) (Rows, error) {
	return t.db.Query(ctx, query, args...)
}

type fakeResult struct{}

func (fakeResult) RowsAffected() (int64, error) { return 0, nil }
func (fakeResult) LastInsertID() (int64, error) { return 0, nil }

func normalizeQuery(q string) string {
	// Lowercase, collapse whitespace, trim.
	q = strings.ToLower(q)
	var b strings.Builder
	prevSpace := false
	for i := 0; i < len(q); i++ {
		c := q[i]
		if c == ' ' || c == '\t' || c == '\n' || c == '\r' {
			if !prevSpace {
				b.WriteByte(' ')
				prevSpace = true
			}
			continue
		}
		b.WriteByte(c)
		prevSpace = false
	}
	return strings.TrimSpace(b.String())
}

func extractObjectName(q, prefix string) string {
	rest := strings.TrimSpace(strings.TrimPrefix(q, prefix))
	// take token until '(' or whitespace
	end := len(rest)
	for i := 0; i < len(rest); i++ {
		c := rest[i]
		if c == '(' || c == ' ' || c == '\t' {
			end = i
			break
		}
	}
	return strings.TrimSpace(rest[:end])
}

// stableSortVersions keeps tests deterministic.
func stableSortVersions(versions []int) []int {
	out := make([]int, len(versions))
	copy(out, versions)
	sort.Ints(out)
	return out
}
