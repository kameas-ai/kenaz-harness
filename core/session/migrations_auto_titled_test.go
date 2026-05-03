package session_test

import (
	"context"
	"testing"
	"time"

	"github.com/sigil-tech/kaneaz-harness/core/session"
	"github.com/sigil-tech/kaneaz-harness/core/storage"
	storagesqlite "github.com/sigil-tech/kaneaz-harness/core/storage/sqlite"
)

// TestMigration0311_ColumnExistsAfterOpen confirms the auto_titled column
// is present in the sessions table after storage.Open applies all migrations
// (including 0311).
func TestMigration0311_ColumnExistsAfterOpen(t *testing.T) {
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

	var n int
	row := db.Reader().QueryRow(ctx,
		"SELECT COUNT(*) FROM pragma_table_info('sessions') WHERE name='auto_titled'")
	if err := row.Scan(&n); err != nil {
		t.Fatalf("pragma_table_info: %v", err)
	}
	if n != 1 {
		t.Errorf("column auto_titled missing from sessions (count=%d)", n)
	}
}

// TestMigration0311_ExistingRowsHaveDefaultZero seeds a session row before
// reading auto_titled back; the column must default to 0 on pre-existing rows.
// This is the "no row touched" half of the WP01 acceptance criteria.
func TestMigration0311_ExistingRowsHaveDefaultZero(t *testing.T) {
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
		_, err := tx.Exec(ctx, `
            INSERT INTO sessions
                (id, name, created_at, updated_at, last_active_at,
                 position, draft, scroll_position, archived_at,
                 system_prompt, context_kind, project_id)
            VALUES (?, ?, ?, ?, ?, 0, '', 0, NULL, '', 'system', NULL)
        `, "s-311", "v0311 session", now.UnixNano(), now.UnixNano(), now.UnixNano())
		return err
	}); err != nil {
		t.Fatalf("seed session row: %v", err)
	}

	row := db.Reader().QueryRow(ctx,
		"SELECT auto_titled FROM sessions WHERE id = ?", "s-311")
	var autoTitled int64
	if err := row.Scan(&autoTitled); err != nil {
		t.Fatalf("scan auto_titled: %v", err)
	}
	if autoTitled != 0 {
		t.Errorf("auto_titled = %d, want 0 (DEFAULT)", autoTitled)
	}
}

// TestMigration0311_ColumnIsWritable confirms that auto_titled can be set
// to 1 and round-trips the value correctly.
func TestMigration0311_ColumnIsWritable(t *testing.T) {
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
		_, err := tx.Exec(ctx, `
            INSERT INTO sessions
                (id, name, created_at, updated_at, last_active_at,
                 position, draft, scroll_position, archived_at,
                 system_prompt, context_kind, project_id)
            VALUES (?, ?, ?, ?, ?, 0, '', 0, NULL, '', 'system', NULL)
        `, "s-311w", "writable test", now.UnixNano(), now.UnixNano(), now.UnixNano())
		return err
	}); err != nil {
		t.Fatalf("insert session: %v", err)
	}

	if err := sdb.WriteTx(ctx, func(tx session.WriteTx) error {
		_, err := tx.Exec(ctx,
			"UPDATE sessions SET auto_titled = 1 WHERE id = ?", "s-311w")
		return err
	}); err != nil {
		t.Fatalf("update auto_titled: %v", err)
	}

	row := db.Reader().QueryRow(ctx,
		"SELECT auto_titled FROM sessions WHERE id = ?", "s-311w")
	var got int64
	if err := row.Scan(&got); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if got != 1 {
		t.Errorf("auto_titled = %d, want 1", got)
	}
}
