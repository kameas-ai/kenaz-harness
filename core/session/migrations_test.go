package session

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/sigil-tech/kaneaz-harness/core/storage/migrations"
)

// migFakeDB is a tiny in-memory Executor that understands the SQL shapes
// the session migrations use:
//
//   - CREATE TABLE IF NOT EXISTS <name> (...)
//   - CREATE INDEX IF NOT EXISTS <name> ON <table>(...)
//   - DROP TABLE IF EXISTS <name>
//   - DROP INDEX IF EXISTS <name>
//   - INSERT INTO harness_migrations (...) VALUES (...)
//   - SELECT ... FROM harness_migrations ORDER BY rowid ASC
//
// Anything else returns an error. Tests using session-specific schema
// must avoid taking dependencies on richer SQL.
type migFakeDB struct {
	mu      sync.Mutex
	tables  map[string]bool
	indexes map[string]bool
	ledger  []migrations.LedgerEntry
}

func newMigFakeDB() *migFakeDB {
	return &migFakeDB{
		tables:  map[string]bool{},
		indexes: map[string]bool{},
	}
}

func (f *migFakeDB) WriteTx(ctx context.Context, fn func(tx migrations.WriteTx) error) error {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	f.mu.Lock()
	tablesSnap := copyMap(f.tables)
	indexesSnap := copyMap(f.indexes)
	ledgerSnap := append([]migrations.LedgerEntry(nil), f.ledger...)
	f.mu.Unlock()

	tx := &migFakeTx{db: f}
	if err := fn(tx); err != nil {
		f.mu.Lock()
		f.tables = tablesSnap
		f.indexes = indexesSnap
		f.ledger = ledgerSnap
		f.mu.Unlock()
		return err
	}
	return nil
}

func (f *migFakeDB) QueryRow(ctx context.Context, query string, args ...any) migrations.Row {
	rows, err := f.Query(ctx, query, args...)
	if err != nil {
		return &migErrRow{err: err}
	}
	if !rows.Next() {
		_ = rows.Close()
		return &migErrRow{err: errors.New("no rows")}
	}
	return &migRowAdapter{rows: rows}
}

func (f *migFakeDB) Query(ctx context.Context, query string, args ...any) (migrations.Rows, error) {
	q := normalize(query)
	if strings.HasPrefix(q, "select version, id, applied_at, content_hash, owning_mission, action, rolled_back_from_version from harness_migrations") {
		f.mu.Lock()
		defer f.mu.Unlock()
		entries := append([]migrations.LedgerEntry(nil), f.ledger...)
		return &migLedgerRows{entries: entries}, nil
	}
	// Migration 0301 seeds context_attachments from sessions rows; the
	// fake has no row store, so report an empty cursor.
	if strings.HasPrefix(q, "select id, coalesce(system_prompt") {
		return &migEmptyRows{}, nil
	}
	// Migration 0311 checks whether auto_titled already exists via
	// pragma_table_info. The fake doesn't track columns, so report 0
	// (column absent) so the ALTER TABLE branch fires. The ALTER TABLE
	// fake accepts ADD COLUMN as a no-op, so the combined behaviour is
	// correct for the migration framework (which never re-runs a migration
	// that is already in the ledger).
	if strings.HasPrefix(q, "select count(*) from pragma_table_info(") {
		return &migCountRows{n: 0}, nil
	}
	return nil, fmt.Errorf("migFakeDB: unsupported query: %q", query)
}

type migFakeTx struct {
	db *migFakeDB
}

func (t *migFakeTx) Exec(ctx context.Context, query string, args ...any) (migrations.Result, error) {
	q := normalize(query)
	switch {
	case strings.HasPrefix(q, "create table if not exists harness_meta"):
		t.db.mu.Lock()
		t.db.tables["harness_meta"] = true
		t.db.mu.Unlock()
		return migResult{}, nil
	case strings.HasPrefix(q, "create table if not exists harness_migrations"):
		t.db.mu.Lock()
		t.db.tables["harness_migrations"] = true
		t.db.mu.Unlock()
		return migResult{}, nil
	case strings.HasPrefix(q, "create table if not exists "):
		name := extractName(q, "create table if not exists ")
		t.db.mu.Lock()
		t.db.tables[name] = true
		t.db.mu.Unlock()
		return migResult{}, nil
	case strings.HasPrefix(q, "create virtual table if not exists "):
		// FTS5 virtual table (cross-session-search WP01, migration 0312).
		name := extractName(q, "create virtual table if not exists ")
		t.db.mu.Lock()
		t.db.tables[name] = true
		t.db.mu.Unlock()
		return migResult{}, nil
	case strings.HasPrefix(q, "create trigger if not exists "):
		// FTS5 sync triggers — name-only registration; the fake doesn't
		// fire them.
		name := extractName(q, "create trigger if not exists ")
		t.db.mu.Lock()
		t.db.indexes[name] = true
		t.db.mu.Unlock()
		return migResult{}, nil
	case strings.HasPrefix(q, "drop trigger if exists "):
		name := strings.TrimSpace(strings.TrimPrefix(q, "drop trigger if exists "))
		t.db.mu.Lock()
		delete(t.db.indexes, name)
		t.db.mu.Unlock()
		return migResult{}, nil
	case strings.HasPrefix(q, "create index if not exists "):
		name := extractName(q, "create index if not exists ")
		t.db.mu.Lock()
		t.db.indexes[name] = true
		t.db.mu.Unlock()
		return migResult{}, nil
	case strings.HasPrefix(q, "drop table if exists "):
		name := strings.TrimSpace(strings.TrimPrefix(q, "drop table if exists "))
		t.db.mu.Lock()
		delete(t.db.tables, name)
		t.db.mu.Unlock()
		return migResult{}, nil
	case strings.HasPrefix(q, "drop table "):
		name := strings.TrimSpace(strings.TrimPrefix(q, "drop table "))
		t.db.mu.Lock()
		delete(t.db.tables, name)
		t.db.mu.Unlock()
		return migResult{}, nil
	case strings.HasPrefix(q, "drop index if exists "):
		name := strings.TrimSpace(strings.TrimPrefix(q, "drop index if exists "))
		t.db.mu.Lock()
		delete(t.db.indexes, name)
		t.db.mu.Unlock()
		return migResult{}, nil
	case strings.HasPrefix(q, "alter table "):
		// The fake doesn't track columns, but the session migrations
		// only ALTER … ADD COLUMN or ALTER … RENAME TO against a
		// known table. ADD COLUMN is a no-op (no column-level state);
		// RENAME TO swaps the source-name flag for the new name so
		// later index creates can see it.
		rest := strings.TrimPrefix(q, "alter table ")
		end := len(rest)
		for i := 0; i < len(rest); i++ {
			c := rest[i]
			if c == ' ' || c == '\t' {
				end = i
				break
			}
		}
		name := strings.TrimSpace(rest[:end])
		t.db.mu.Lock()
		exists := t.db.tables[name]
		t.db.mu.Unlock()
		if !exists {
			return nil, fmt.Errorf("alter table: unknown table %q", name)
		}
		if rename := strings.Index(rest, " rename to "); rename >= 0 {
			newName := strings.TrimSpace(rest[rename+len(" rename to "):])
			t.db.mu.Lock()
			delete(t.db.tables, name)
			t.db.tables[newName] = true
			t.db.mu.Unlock()
		}
		return migResult{}, nil
	case strings.HasPrefix(q, "insert into ") && strings.Contains(q, " select "):
		// Migration 0304 copies rows artifacts → artifacts_new via
		// INSERT INTO ... SELECT ... FROM .... The fake has no row
		// store; treat the copy as a no-op so the schema-shape side
		// of the migration still exercises.
		return migResult{}, nil
	case strings.HasPrefix(q, "insert into harness_migrations"):
		if len(args) != 7 {
			return nil, fmt.Errorf("ledger insert: want 7 args got %d", len(args))
		}
		var rbFrom *int
		switch v := args[6].(type) {
		case int:
			vv := v
			rbFrom = &vv
		case int64:
			vv := int(v)
			rbFrom = &vv
		}
		t.db.mu.Lock()
		t.db.ledger = append(t.db.ledger, migrations.LedgerEntry{
			Version:               args[0].(int),
			ID:                    args[1].(string),
			AppliedAt:             args[2].(string),
			ContentHash:           args[3].(string),
			OwningMission:         args[4].(string),
			Action:                migrations.LedgerAction(args[5].(string)),
			RolledBackFromVersion: rbFrom,
		})
		t.db.mu.Unlock()
		return migResult{}, nil
	}
	return nil, fmt.Errorf("migFakeDB: unsupported exec: %q", query)
}

func (t *migFakeTx) QueryRow(ctx context.Context, query string, args ...any) migrations.Row {
	return t.db.QueryRow(ctx, query, args...)
}

func (t *migFakeTx) Query(ctx context.Context, query string, args ...any) (migrations.Rows, error) {
	return t.db.Query(ctx, query, args...)
}

type migResult struct{}

func (migResult) RowsAffected() (int64, error) { return 0, nil }
func (migResult) LastInsertID() (int64, error) { return 0, nil }

type migErrRow struct{ err error }

func (r *migErrRow) Scan(dest ...any) error { return r.err }

type migRowAdapter struct{ rows migrations.Rows }

func (r *migRowAdapter) Scan(dest ...any) error {
	defer r.rows.Close()
	return r.rows.Scan(dest...)
}

type migLedgerRows struct {
	entries []migrations.LedgerEntry
	idx     int
	cur     migrations.LedgerEntry
	closed  bool
}

func (r *migLedgerRows) Next() bool {
	if r.closed || r.idx >= len(r.entries) {
		return false
	}
	r.cur = r.entries[r.idx]
	r.idx++
	return true
}

func (r *migLedgerRows) Scan(dest ...any) error {
	if len(dest) != 7 {
		return fmt.Errorf("scan: want 7 got %d", len(dest))
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

func (r *migLedgerRows) Close() error { r.closed = true; return nil }
func (r *migLedgerRows) Err() error   { return nil }

// migEmptyRows is a no-op iterator used by the fake to satisfy queries
// against tables it doesn't track (e.g. the seed-from-sessions query in
// migration 0301).
type migEmptyRows struct{}

func (migEmptyRows) Next() bool             { return false }
func (migEmptyRows) Scan(_ ...any) error    { return errors.New("no rows") }
func (migEmptyRows) Close() error           { return nil }
func (migEmptyRows) Err() error             { return nil }

// migCountRows is a single-row iterator returning a count value. Used by
// the fake to satisfy pragma_table_info COUNT(*) queries (migration 0311).
type migCountRows struct {
	n    int
	done bool
}

func (r *migCountRows) Next() bool {
	if r.done {
		return false
	}
	r.done = true
	return true
}
func (r *migCountRows) Scan(dest ...any) error {
	if len(dest) != 1 {
		return fmt.Errorf("migCountRows: want 1 dest got %d", len(dest))
	}
	*dest[0].(*int) = r.n
	return nil
}
func (r *migCountRows) Close() error { return nil }
func (r *migCountRows) Err() error   { return nil }

func copyMap(m map[string]bool) map[string]bool {
	out := make(map[string]bool, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func normalize(q string) string {
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

func extractName(q, prefix string) string {
	rest := strings.TrimSpace(strings.TrimPrefix(q, prefix))
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

// TestMigrations_RegisterAndApply verifies the session migrations
// register cleanly into the canonical 300-399 block, the runner walks
// them in version order, and every expected table + index is created.
func TestMigrations_RegisterAndApply(t *testing.T) {
	t.Parallel()
	db := newMigFakeDB()
	reg := migrations.NewRegistry(db, func() string { return "ts" }, nil)

	// Bootstrap ledger so Apply can write rows.
	if err := migrations.EnsureLedger(context.Background(), db); err != nil {
		t.Fatalf("EnsureLedger: %v", err)
	}
	for _, m := range migrations.StorageBootstrap() {
		if err := reg.Register(m); err != nil {
			t.Fatalf("register bootstrap: %v", err)
		}
	}
	if err := RegisterMigrations(reg); err != nil {
		t.Fatalf("RegisterMigrations: %v", err)
	}
	if err := reg.Apply(context.Background()); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	if !db.tables["sessions"] {
		t.Errorf("expected sessions table to exist")
	}
	if !db.tables["session_messages"] {
		t.Errorf("expected session_messages table to exist")
	}
	for _, idx := range []string{
		"idx_session_messages_session_seq",
		"idx_sessions_position",
		"idx_sessions_last_active_at",
	} {
		if !db.indexes[idx] {
			t.Errorf("expected index %q to exist", idx)
		}
	}

	// Ledger rows: 2 storage bootstrap + 12 sessions migrations
	// (0300 init + 0301 context_attachments + 0302 content_json +
	// 0303 artifacts + 0304 artifacts-promote + 0305 telemetry +
	// 0306 branches + 0307 corpora + 0308 memory_hook_journal +
	// 0309 agent_graph_events + 0310 compaction + 0311 auto_titled +
	// 0312 search_fts5) = 15 applied entries.
	// 0312 cross-session-search FTS5 + 0313 subagent-metadata = 16 applied entries
	// (2 chassis bootstrap + 14 sessions migrations).
	if got := len(db.ledger); got != 16 {
		t.Fatalf("ledger size = %d, want 16", got)
	}
	wantVersions := []int{1, 2, 300, 301, 302, 303, 304, 305, 306, 307, 308, 309, 310, 311, 312, 313}
	for i, want := range wantVersions {
		if db.ledger[i].Version != want {
			t.Errorf("ledger[%d].Version = %d, want %d", i, db.ledger[i].Version, want)
		}
	}
	for _, e := range db.ledger[2:] {
		if e.OwningMission != OwningMission {
			t.Errorf("OwningMission = %q, want %q", e.OwningMission, OwningMission)
		}
		if e.Action != migrations.LedgerActionApplied {
			t.Errorf("Action = %q, want applied", e.Action)
		}
	}
	// projects table + index ride along in the consolidated init.
	if !db.tables["projects"] {
		t.Errorf("expected projects table to exist")
	}
	if !db.indexes["idx_projects_name"] {
		t.Errorf("expected idx_projects_name to exist")
	}
	// context_attachments table + index land in 0301.
	if !db.tables["context_attachments"] {
		t.Errorf("expected context_attachments table to exist")
	}
	if !db.indexes["idx_context_attachments_scope"] {
		t.Errorf("expected idx_context_attachments_scope to exist")
	}
	// artifacts table + indexes land in 0303.
	if !db.tables["artifacts"] {
		t.Errorf("expected artifacts table to exist")
	}
	for _, idx := range []string{
		"idx_artifacts_session",
		"idx_artifacts_project",
		"idx_artifacts_content_hash",
	} {
		if !db.indexes[idx] {
			t.Errorf("expected index %q to exist", idx)
		}
	}
}

// TestMigrations_VersionsInBlock pins that every migration this package
// declares falls within the reserved 300-399 range.
func TestMigrations_VersionsInBlock(t *testing.T) {
	t.Parallel()
	block, ok := migrations.LookupBlock(OwningMission)
	if !ok {
		t.Fatalf("no block reservation for %q", OwningMission)
	}
	for _, m := range Migrations() {
		if !block.Contains(m.Version) {
			t.Errorf("migration %s version %d outside [%d,%d]",
				m.ID, m.Version, block.Min, block.Max)
		}
	}
}
