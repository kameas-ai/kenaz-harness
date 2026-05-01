package session_test

import (
	"context"
	"testing"
	"time"

	"github.com/sigil-tech/kaneaz-harness/core/session"
	"github.com/sigil-tech/kaneaz-harness/core/storage"
	storagesqlite "github.com/sigil-tech/kaneaz-harness/core/storage/sqlite"
)

// TestMigration0316_ColumnsExistAfterOpen confirms the four new
// autonomy columns ride along on a fresh Open (i.e. migration 0316
// fired and is observable in pragma_table_info).
func TestMigration0316_ColumnsExistAfterOpen(t *testing.T) {
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

	for _, tt := range []struct {
		table string
		col   string
	}{
		{"projects", "autonomy_level"},
		{"projects", "autonomy_overrides"},
		{"sessions", "autonomy_level"},
		{"sessions", "autonomy_overrides"},
	} {
		var n int
		row := db.Reader().QueryRow(ctx,
			"SELECT COUNT(*) FROM pragma_table_info('"+tt.table+"') WHERE name=?", tt.col)
		if err := row.Scan(&n); err != nil {
			t.Fatalf("pragma_table_info %s.%s: %v", tt.table, tt.col, err)
		}
		if n != 1 {
			t.Errorf("column %s.%s missing (count=%d)", tt.table, tt.col, n)
		}
	}
}

// TestMigration0316_ExistingRowsHaveNullColumns seeds a project + session
// before reading the new columns back; every new column must be NULL on
// rows that predate autonomy persistence.
func TestMigration0316_ExistingRowsHaveNullColumns(t *testing.T) {
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
            INSERT INTO projects
                (id, name, description, created_at, updated_at)
            VALUES (?, ?, ?, ?, ?)
        `, "p-316", "v0316 project", "", now.UnixNano(), now.UnixNano()); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `
            INSERT INTO sessions
                (id, name, created_at, updated_at, last_active_at,
                 position, draft, scroll_position, archived_at,
                 system_prompt, context_kind, project_id)
            VALUES (?, ?, ?, ?, ?, 0, '', 0, NULL, '', 'system', NULL)
        `, "s-316", "v0316 session", now.UnixNano(), now.UnixNano(), now.UnixNano())
		return err
	}); err != nil {
		t.Fatalf("seed rows: %v", err)
	}

	// Read each new column back as a nullable. Each must be NULL.
	row := db.Reader().QueryRow(ctx, `
        SELECT autonomy_level, autonomy_overrides
        FROM projects WHERE id = ?
    `, "p-316")
	var (
		lvl *int64
		ovr *string
	)
	if err := row.Scan(&lvl, &ovr); err != nil {
		t.Fatalf("scan project autonomy columns: %v", err)
	}
	if lvl != nil {
		t.Errorf("project autonomy_level = %d, want NULL", *lvl)
	}
	if ovr != nil {
		t.Errorf("project autonomy_overrides = %q, want NULL", *ovr)
	}

	row = db.Reader().QueryRow(ctx, `
        SELECT autonomy_level, autonomy_overrides
        FROM sessions WHERE id = ?
    `, "s-316")
	lvl = nil
	ovr = nil
	if err := row.Scan(&lvl, &ovr); err != nil {
		t.Fatalf("scan session autonomy columns: %v", err)
	}
	if lvl != nil {
		t.Errorf("session autonomy_level = %d, want NULL", *lvl)
	}
	if ovr != nil {
		t.Errorf("session autonomy_overrides = %q, want NULL", *ovr)
	}
}

// TestMigration0316_NewColumnsAreWritable confirms the new columns
// accept non-NULL writes — sanity check that the schema is addressable
// for the storage layer.
func TestMigration0316_NewColumnsAreWritable(t *testing.T) {
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
	wantOverrides := `{"maxIterations":75,"tokenCeilingPerTurn":262144}`
	wantLevel := int64(2)

	if err := sdb.WriteTx(ctx, func(tx session.WriteTx) error {
		_, err := tx.Exec(ctx, `
            INSERT INTO projects
                (id, name, description, created_at, updated_at,
                 autonomy_level, autonomy_overrides)
            VALUES (?, ?, ?, ?, ?, ?, ?)
        `, "p-w", "writable", "", now.UnixNano(), now.UnixNano(),
			wantLevel, wantOverrides)
		return err
	}); err != nil {
		t.Fatalf("write project autonomy columns: %v", err)
	}

	row := db.Reader().QueryRow(ctx, `
        SELECT autonomy_level, autonomy_overrides
        FROM projects WHERE id = ?
    `, "p-w")
	var (
		gotLevel int64
		gotOvr   string
	)
	if err := row.Scan(&gotLevel, &gotOvr); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if gotLevel != wantLevel {
		t.Errorf("autonomy_level = %d, want %d", gotLevel, wantLevel)
	}
	if gotOvr != wantOverrides {
		t.Errorf("autonomy_overrides = %q, want %q", gotOvr, wantOverrides)
	}
}

// TestMigration0316_IdempotentAfterReopen confirms re-opening a DB that
// already has migration 0316 in its ledger does NOT re-run the
// migration (which would fail on the duplicate column ALTER). This is
// the framework's contract — once a migration's ledger row is present,
// the runner skips it — but pinning it for autonomy specifically rules
// out a regression where the migration is registered with a bad version
// or a hash mismatch.
func TestMigration0316_IdempotentAfterReopen(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cfg := storage.Config{
		DataDir:          dir,
		EncryptionStatus: storage.EncryptionStatusDisabledWithDiskEncryption,
	}
	db, err := storagesqlite.Open(cfg)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := db.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
	// Reopen — should be a no-op for the autonomy migration.
	db2, err := storagesqlite.Open(cfg)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer db2.Close(context.Background())
	ctx := context.Background()

	for _, tt := range []struct {
		table string
		col   string
	}{
		{"projects", "autonomy_level"},
		{"projects", "autonomy_overrides"},
		{"sessions", "autonomy_level"},
		{"sessions", "autonomy_overrides"},
	} {
		var n int
		row := db2.Reader().QueryRow(ctx,
			"SELECT COUNT(*) FROM pragma_table_info('"+tt.table+"') WHERE name=?", tt.col)
		if err := row.Scan(&n); err != nil {
			t.Fatalf("pragma_table_info %s.%s: %v", tt.table, tt.col, err)
		}
		if n != 1 {
			t.Errorf("column %s.%s after reopen count=%d, want 1", tt.table, tt.col, n)
		}
	}
}
