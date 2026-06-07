package session_test

import (
	"context"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/storage"
	storagesqlite "github.com/kameas-ai/kenaz-harness/core/storage/sqlite"
)

// TestMigration0322_ColumnExistsAfterOpen verifies that migration 0322
// adds the last_usage_json column to the sessions table.
func TestMigration0322_ColumnExistsAfterOpen(t *testing.T) {
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
		"SELECT COUNT(*) FROM pragma_table_info('sessions') WHERE name='last_usage_json'")
	if err := row.Scan(&n); err != nil {
		t.Fatalf("pragma_table_info: %v", err)
	}
	if n != 1 {
		t.Errorf("column last_usage_json missing from sessions (count=%d)", n)
	}
}
