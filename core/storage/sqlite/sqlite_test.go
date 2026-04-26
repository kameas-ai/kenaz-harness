package sqlite_test

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sigil-tech/kaneaz-harness/core/session"
	"github.com/sigil-tech/kaneaz-harness/core/storage"
	storagesqlite "github.com/sigil-tech/kaneaz-harness/core/storage/sqlite"

	_ "modernc.org/sqlite"
)

func newConfig(dir string) storage.Config {
	return storage.Config{
		DataDir:          dir,
		EncryptionStatus: storage.EncryptionStatusDisabledWithDiskEncryption,
	}
}

func mustOpen(t *testing.T, dir string) storage.DB {
	t.Helper()
	db, err := storagesqlite.Open(newConfig(dir))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close(context.Background())
	})
	return db
}

func TestOpen_HappyPath(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	db := mustOpen(t, dir)

	ctx := context.Background()
	r := db.Reader()

	var journalMode string
	if err := r.QueryRow(ctx, "PRAGMA journal_mode").Scan(&journalMode); err != nil {
		t.Fatalf("PRAGMA journal_mode: %v", err)
	}
	if journalMode != "wal" {
		t.Errorf("journal_mode = %q, want wal", journalMode)
	}

	var fk int
	if err := r.QueryRow(ctx, "PRAGMA foreign_keys").Scan(&fk); err != nil {
		t.Fatalf("PRAGMA foreign_keys: %v", err)
	}
	if fk != 1 {
		t.Errorf("foreign_keys = %d, want 1", fk)
	}

	var busy int
	if err := r.QueryRow(ctx, "PRAGMA busy_timeout").Scan(&busy); err != nil {
		t.Fatalf("PRAGMA busy_timeout: %v", err)
	}
	if busy != 5000 {
		t.Errorf("busy_timeout = %d, want 5000", busy)
	}

	if _, err := os.Stat(filepath.Join(dir, "data.db")); err != nil {
		t.Errorf("data.db not on disk: %v", err)
	}
}

func TestOpen_RejectsEncryptionEnabled(t *testing.T) {
	t.Parallel()
	cfg := newConfig(t.TempDir())
	cfg.EncryptionStatus = storage.EncryptionStatusEnabled
	cfg.EncryptionKey = storage.CredentialReference{Keychain: "harness-test"}
	_, err := storagesqlite.Open(cfg)
	if !errors.Is(err, storage.ErrNotImplemented) {
		t.Fatalf("got %v, want ErrNotImplemented", err)
	}
}

func TestOpen_LockHeld(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	first := mustOpen(t, dir)
	defer first.Close(context.Background())

	_, err := storagesqlite.Open(newConfig(dir))
	if !errors.Is(err, storage.ErrDBLocked) {
		t.Fatalf("got %v, want ErrDBLocked", err)
	}
}

func TestOpen_RegistersSessionMigrations(t *testing.T) {
	t.Parallel()
	db := mustOpen(t, t.TempDir())
	ctx := context.Background()
	for _, table := range []string{"sessions", "session_messages"} {
		var name string
		row := db.Reader().QueryRow(ctx,
			"SELECT name FROM sqlite_master WHERE type='table' AND name=?", table)
		if err := row.Scan(&name); err != nil {
			t.Errorf("table %q missing: %v", table, err)
			continue
		}
		if name != table {
			t.Errorf("table lookup for %q returned %q", table, name)
		}
	}

	// Confirm the migration ledger recorded the session block.
	rows, err := db.Reader().Query(ctx,
		"SELECT version FROM harness_migrations WHERE owning_mission='sessions' ORDER BY version")
	if err != nil {
		t.Fatalf("query ledger: %v", err)
	}
	defer rows.Close()
	var versions []int
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			t.Fatal(err)
		}
		versions = append(versions, v)
	}
	want := []int{300, 301, 302, 303, 304}
	if len(versions) != len(want) {
		t.Fatalf("session migrations applied = %v, want %v", versions, want)
	}
	for i, v := range want {
		if versions[i] != v {
			t.Errorf("migration[%d] = %d, want %d", i, versions[i], v)
		}
	}
}

func TestOpen_ApplyIdempotent(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	first := mustOpen(t, dir)
	if err := first.Close(context.Background()); err != nil {
		t.Fatalf("first close: %v", err)
	}

	// Second Open must succeed without re-applying anything.
	second := mustOpen(t, dir)
	ctx := context.Background()
	rows, err := second.Reader().Query(ctx,
		"SELECT COUNT(*) FROM harness_migrations WHERE action='applied'")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()
	if !rows.Next() {
		t.Fatal("no count row")
	}
	var count int
	if err := rows.Scan(&count); err != nil {
		t.Fatal(err)
	}
	// 2 storage bootstrap + 5 session migrations.
	if count != 7 {
		t.Errorf("ledger count = %d, want 7", count)
	}
}

func TestOpen_LegacyMigration(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	legacyPath := filepath.Join(dir, "sessions.db")
	createLegacySessionsDB(t, legacyPath, false /* hasSystemPrompt */, false /* hasContextKind */)

	if err := storagesqlite.MigrateLegacySessionsDB(dir); err != nil {
		t.Fatalf("MigrateLegacySessionsDB: %v", err)
	}

	// data.db should now exist; the legacy file should be renamed.
	if _, err := os.Stat(filepath.Join(dir, "data.db")); err != nil {
		t.Fatalf("data.db missing: %v", err)
	}
	if _, err := os.Stat(legacyPath); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("legacy still at original path: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "sessions.legacy.db")); err != nil {
		t.Errorf("renamed legacy missing: %v", err)
	}

	db := mustOpen(t, dir)
	ctx := context.Background()
	row := db.Reader().QueryRow(ctx,
		"SELECT id, name, system_prompt, context_kind FROM sessions WHERE id='legacy-1'")
	var id, name, prompt, kind string
	if err := row.Scan(&id, &name, &prompt, &kind); err != nil {
		t.Fatalf("query legacy row: %v", err)
	}
	if id != "legacy-1" || name != "Legacy" {
		t.Errorf("row = (%q,%q), want (legacy-1, Legacy)", id, name)
	}
	if prompt != "" || kind != "system" {
		t.Errorf("defaults = (%q,%q), want (\"\", system)", prompt, kind)
	}
}

func TestSessionRoundTrip_ThroughAdapter(t *testing.T) {
	t.Parallel()
	db := mustOpen(t, t.TempDir())
	store := session.NewSQLStore(session.NewStorageDB(db))

	ctx := context.Background()
	now := time.Now().UTC()
	rec := session.Record{
		ID:           "s1",
		Name:         "first",
		CreatedAt:    now,
		UpdatedAt:    now,
		LastActiveAt: now,
		Position:     0,
		ContextKind:  session.ContextKindSystem,
	}
	if err := store.Create(ctx, rec); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := store.Get(ctx, "s1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ID != "s1" || got.Name != "first" {
		t.Errorf("round-trip = (%q,%q), want (s1, first)", got.ID, got.Name)
	}

	msg, err := store.AppendMessage(ctx, session.Message{
		ID:        "m1",
		SessionID: "s1",
		Role:      session.RoleUser,
		Content:   "hello",
		CreatedAt: now,
	})
	if err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}
	if msg.Sequence != 0 {
		t.Errorf("first sequence = %d, want 0", msg.Sequence)
	}
	msgs, err := store.ListMessages(ctx, "s1")
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if len(msgs) != 1 || msgs[0].Content != "hello" {
		t.Errorf("messages = %v, want one with content hello", msgs)
	}
}

// ── test helpers ──────────────────────────────────────────────────────

// createLegacySessionsDB seeds a pre-consolidation sessions.db at path.
// The hasSystemPrompt / hasContextKind toggles model the schema of
// installs that did vs. didn't migrate to columns 303 / 304 under the
// old standalone shortcut.
func createLegacySessionsDB(t *testing.T, path string, hasSystemPrompt, hasContextKind bool) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	dsn := "file:" + url.PathEscape(path) +
		"?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("open legacy: %v", err)
	}
	defer db.Close()
	cols := `id              TEXT PRIMARY KEY,
            name            TEXT NOT NULL,
            created_at      INTEGER NOT NULL,
            updated_at      INTEGER NOT NULL,
            last_active_at  INTEGER NOT NULL,
            position        INTEGER NOT NULL,
            draft           TEXT NOT NULL DEFAULT '',
            scroll_position INTEGER NOT NULL DEFAULT 0,
            archived_at     INTEGER`
	if hasSystemPrompt {
		cols += `, system_prompt TEXT NOT NULL DEFAULT ''`
	}
	if hasContextKind {
		cols += `, context_kind TEXT NOT NULL DEFAULT 'system'`
	}
	if _, err := db.Exec("CREATE TABLE sessions (" + cols + ")"); err != nil {
		t.Fatalf("create legacy schema: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE session_messages (
        id          TEXT PRIMARY KEY,
        session_id  TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
        sequence    INTEGER NOT NULL,
        role        TEXT NOT NULL,
        content     TEXT NOT NULL,
        tool_calls  TEXT,
        created_at  INTEGER NOT NULL
    )`); err != nil {
		t.Fatalf("create legacy messages: %v", err)
	}
	now := time.Now().UnixNano()
	if hasSystemPrompt && hasContextKind {
		_, err = db.Exec(`INSERT INTO sessions
            (id, name, created_at, updated_at, last_active_at,
             position, draft, scroll_position, archived_at,
             system_prompt, context_kind)
            VALUES ('legacy-1','Legacy',?,?,?,0,'',0,NULL,'','system')`,
			now, now, now)
	} else {
		_, err = db.Exec(`INSERT INTO sessions
            (id, name, created_at, updated_at, last_active_at,
             position, draft, scroll_position, archived_at)
            VALUES ('legacy-1','Legacy',?,?,?,0,'',0,NULL)`,
			now, now, now)
	}
	if err != nil {
		t.Fatalf("seed legacy row: %v", err)
	}
}
