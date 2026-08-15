package session_test

import (
	"context"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/session"
	"github.com/kameas-ai/kenaz-harness/core/storage"
	storagesqlite "github.com/kameas-ai/kenaz-harness/core/storage/sqlite"
)

func openDB(t *testing.T) storage.DB {
	t.Helper()
	cfg := storage.Config{
		DataDir:          t.TempDir(),
		EncryptionStatus: storage.EncryptionStatusDisabledWithDiskEncryption,
	}
	db, err := storagesqlite.Open(cfg)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close(context.Background()) })
	return db
}

// TestMigration0312_FTSTableExists confirms the messages_fts virtual
// table is created after a fresh Open (i.e. migration 0312 fired).
func TestMigration0312_FTSTableExists(t *testing.T) {
	t.Parallel()
	db := openDB(t)
	ctx := context.Background()

	var name string
	row := db.Reader().QueryRow(ctx,
		"SELECT name FROM sqlite_master WHERE type='table' AND name='messages_fts'")
	if err := row.Scan(&name); err != nil {
		t.Fatalf("messages_fts table missing: %v", err)
	}
	if name != "messages_fts" {
		t.Errorf("table name = %q, want messages_fts", name)
	}
}

// TestMigration0312_TriggersExist confirms the FTS sync triggers are
// present in sqlite_master after the sessions migrations apply.
//
// The names are still 0312's three: migration 0335
// (model-moves-transcript-01PMCH01 WP06) replaces them with role-guarded
// equivalents in place. The update trigger keeps ONE body carrying two
// independently-guarded statements — see migrations_search_fts_tool_rows.go
// for why two separate triggers is the one shape that cannot work.
func TestMigration0312_TriggersExist(t *testing.T) {
	t.Parallel()
	db := openDB(t)
	ctx := context.Background()

	for _, trig := range []string{
		"messages_fts_ai",
		"messages_fts_au",
		"messages_fts_ad",
	} {
		var got string
		row := db.Reader().QueryRow(ctx,
			"SELECT name FROM sqlite_master WHERE type='trigger' AND name=?", trig)
		if err := row.Scan(&got); err != nil {
			t.Errorf("trigger %q missing: %v", trig, err)
			continue
		}
		if got != trig {
			t.Errorf("trigger %q -> %q", trig, got)
		}
	}
}

// TestMigration0312_IdempotentApply confirms that opening the same DB a
// second time does not error (migration 0312 uses CREATE … IF NOT
// EXISTS so re-application is a no-op).
func TestMigration0312_IdempotentApply(t *testing.T) {
	t.Parallel()
	cfg := storage.Config{
		DataDir:          t.TempDir(),
		EncryptionStatus: storage.EncryptionStatusDisabledWithDiskEncryption,
	}
	ctx := context.Background()

	// First open applies migrations.
	db1, err := storagesqlite.Open(cfg)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	if err := db1.Close(ctx); err != nil {
		t.Fatalf("first Close: %v", err)
	}

	// Second open must succeed without re-applying anything.
	db2, err := storagesqlite.Open(cfg)
	if err != nil {
		t.Fatalf("second Open: %v", err)
	}
	defer db2.Close(ctx)

	// Verify the FTS table still exists.
	var name string
	if err := db2.Reader().QueryRow(ctx,
		"SELECT name FROM sqlite_master WHERE type='table' AND name='messages_fts'",
	).Scan(&name); err != nil {
		t.Fatalf("messages_fts missing after second open: %v", err)
	}
}

// TestMigration0312_TriggerFiresOnInsert inserts a session + message
// and then confirms the FTS index has a matching row (i.e. the AFTER
// INSERT trigger fired and the FTS virtual table is queryable).
func TestMigration0312_TriggerFiresOnInsert(t *testing.T) {
	t.Parallel()
	db := openDB(t)
	ctx := context.Background()

	store := session.NewSQLStore(session.NewStorageDB(db))
	now := time.Now().UTC()

	rec := session.Record{
		ID:           "s-fts-1",
		Name:         "fts test session",
		CreatedAt:    now,
		UpdatedAt:    now,
		LastActiveAt: now,
		Position:     0,
		ContextKind:  session.ContextKindSystem,
	}
	if err := store.Create(ctx, rec); err != nil {
		t.Fatalf("Create session: %v", err)
	}

	// Append a message with a distinctive term.
	if _, err := store.AppendMessage(ctx, session.Message{
		ID:        "m-fts-1",
		SessionID: "s-fts-1",
		Role:      session.RoleUser,
		Content:   "rust programming language borrow checker",
		CreatedAt: now,
	}); err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}

	// Query the FTS table directly — the trigger must have fired.
	var rowCount int
	row := db.Reader().QueryRow(ctx,
		"SELECT COUNT(*) FROM messages_fts WHERE messages_fts MATCH 'rust'")
	if err := row.Scan(&rowCount); err != nil {
		t.Fatalf("FTS MATCH query: %v", err)
	}
	if rowCount < 1 {
		t.Errorf("FTS MATCH 'rust' returned %d rows, want ≥1", rowCount)
	}
}
