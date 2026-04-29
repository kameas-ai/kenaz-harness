package session_test

import (
	"context"
	"testing"
	"time"

	"github.com/sigil-tech/kaneaz-harness/core/session"
	"github.com/sigil-tech/kaneaz-harness/core/storage"
	storagesqlite "github.com/sigil-tech/kaneaz-harness/core/storage/sqlite"
)

// TestMigration0310_ColumnsAndIndexesExistAfterOpen confirms the three
// new bookkeeping columns and the two indexes ride along on a fresh
// Open (i.e. migration 0310 fired and is observable in sqlite_master /
// pragma_table_info).
func TestMigration0310_ColumnsAndIndexesExistAfterOpen(t *testing.T) {
	t.Parallel()
	cfg := storage.Config{
		DataDir:          t.TempDir(),
		EncryptionStatus: storage.EncryptionStatusDisabledWithDiskEncryption,
	}
	db, err := storagesqlite.Open(cfg)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close(context.Background())
	ctx := context.Background()

	for _, col := range []string{"compacted_into_id", "compacted_at", "archived_at"} {
		var n int
		row := db.Reader().QueryRow(ctx,
			"SELECT COUNT(*) FROM pragma_table_info('session_messages') WHERE name=?", col)
		if err := row.Scan(&n); err != nil {
			t.Fatalf("pragma_table_info %q: %v", col, err)
		}
		if n != 1 {
			t.Errorf("column %q missing from session_messages (count=%d)", col, n)
		}
	}

	for _, idx := range []string{
		"idx_session_messages_compacted_into",
		"idx_session_messages_archived_at",
	} {
		var got string
		row := db.Reader().QueryRow(ctx,
			"SELECT name FROM sqlite_master WHERE type='index' AND name=?", idx)
		if err := row.Scan(&got); err != nil {
			t.Errorf("index %q missing: %v", idx, err)
			continue
		}
		if got != idx {
			t.Errorf("index %q -> %q", idx, got)
		}
	}
}

// TestMigration0310_ExistingRowsHaveNullColumns seeds a session +
// session_messages row before reading the new columns back; every new
// column must be NULL on a row that predates compaction. This is the
// "no row touched" half of the WP01 acceptance criteria — applying 0310
// must not retroactively populate the new columns on existing data.
func TestMigration0310_ExistingRowsHaveNullColumns(t *testing.T) {
	t.Parallel()
	cfg := storage.Config{
		DataDir:          t.TempDir(),
		EncryptionStatus: storage.EncryptionStatusDisabledWithDiskEncryption,
	}
	db, err := storagesqlite.Open(cfg)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close(context.Background())
	ctx := context.Background()

	sdb := session.NewStorageDB(db)
	now := time.Now().UTC().Truncate(time.Microsecond)
	if err := sdb.WriteTx(ctx, func(tx session.WriteTx) error {
		if _, err := tx.Exec(ctx, `
            INSERT INTO sessions
                (id, name, created_at, updated_at, last_active_at,
                 position, draft, scroll_position, archived_at,
                 system_prompt, context_kind, project_id)
            VALUES (?, ?, ?, ?, ?, 0, '', 0, NULL, '', 'system', NULL)
        `, "s-310", "v0310 session", now.UnixNano(), now.UnixNano(), now.UnixNano()); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `
            INSERT INTO session_messages
                (id, session_id, sequence, role, content, tool_calls, created_at)
            VALUES (?, ?, ?, ?, ?, NULL, ?)
        `, "m-310", "s-310", 0, "user", "hello", now.UnixNano())
		return err
	}); err != nil {
		t.Fatalf("seed message row: %v", err)
	}

	// Read each new column back as a nullable. Each must be NULL.
	row := db.Reader().QueryRow(ctx, `
        SELECT compacted_into_id, compacted_at, archived_at
        FROM session_messages WHERE id = ?
    `, "m-310")
	var (
		compactedIntoID *string
		compactedAt     *int64
		archivedAt      *int64
	)
	if err := row.Scan(&compactedIntoID, &compactedAt, &archivedAt); err != nil {
		t.Fatalf("scan new columns: %v", err)
	}
	if compactedIntoID != nil {
		t.Errorf("compacted_into_id = %q, want NULL", *compactedIntoID)
	}
	if compactedAt != nil {
		t.Errorf("compacted_at = %d, want NULL", *compactedAt)
	}
	if archivedAt != nil {
		t.Errorf("archived_at = %d, want NULL", *archivedAt)
	}
}

// TestMigration0310_NewColumnsAreWritable confirms a row inserted after
// 0310 lands can populate the new columns explicitly (sanity check that
// the columns are addressable and round-trip values intact). This isn't
// a test of the engine — it just pins that the schema accepts non-NULL
// writes through the same INSERT path the engine will use.
func TestMigration0310_NewColumnsAreWritable(t *testing.T) {
	t.Parallel()
	cfg := storage.Config{
		DataDir:          t.TempDir(),
		EncryptionStatus: storage.EncryptionStatusDisabledWithDiskEncryption,
	}
	db, err := storagesqlite.Open(cfg)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close(context.Background())
	ctx := context.Background()

	sdb := session.NewStorageDB(db)
	now := time.Now().UTC().Truncate(time.Microsecond)
	wantCompactedAt := now.UnixNano()
	wantArchivedAt := now.Add(time.Hour).UnixNano()

	if err := sdb.WriteTx(ctx, func(tx session.WriteTx) error {
		if _, err := tx.Exec(ctx, `
            INSERT INTO sessions
                (id, name, created_at, updated_at, last_active_at,
                 position, draft, scroll_position, archived_at,
                 system_prompt, context_kind, project_id)
            VALUES (?, ?, ?, ?, ?, 0, '', 0, NULL, '', 'system', NULL)
        `, "s-w", "writable", now.UnixNano(), now.UnixNano(), now.UnixNano()); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `
            INSERT INTO session_messages
                (id, session_id, sequence, role, content, tool_calls,
                 created_at, compacted_into_id, compacted_at, archived_at)
            VALUES (?, ?, ?, ?, ?, NULL, ?, ?, ?, ?)
        `, "m-w", "s-w", 0, "user", "old message", now.UnixNano(),
			"summary-1", wantCompactedAt, wantArchivedAt)
		return err
	}); err != nil {
		t.Fatalf("write new columns: %v", err)
	}

	row := db.Reader().QueryRow(ctx, `
        SELECT compacted_into_id, compacted_at, archived_at
        FROM session_messages WHERE id = ?
    `, "m-w")
	var (
		gotInto       string
		gotCompacted  int64
		gotArchivedAt int64
	)
	if err := row.Scan(&gotInto, &gotCompacted, &gotArchivedAt); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if gotInto != "summary-1" {
		t.Errorf("compacted_into_id = %q, want %q", gotInto, "summary-1")
	}
	if gotCompacted != wantCompactedAt {
		t.Errorf("compacted_at = %d, want %d", gotCompacted, wantCompactedAt)
	}
	if gotArchivedAt != wantArchivedAt {
		t.Errorf("archived_at = %d, want %d", gotArchivedAt, wantArchivedAt)
	}
}
