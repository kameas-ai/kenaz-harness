package session_test

import (
	"context"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/storage"
	storagesqlite "github.com/kameas-ai/kenaz-harness/core/storage/sqlite"
)

// TestMigration0314_ColumnsExistAfterOpen verifies that migration 0314
// adds the four usage columns to session_messages.
func TestMigration0314_ColumnsExistAfterOpen(t *testing.T) {
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

	for _, col := range []string{"prompt_tokens", "completion_tokens", "cost_usd", "cost_source"} {
		var n int
		row := db.Reader().QueryRow(ctx,
			"SELECT COUNT(*) FROM pragma_table_info('session_messages') WHERE name=?", col)
		if err := row.Scan(&n); err != nil {
			t.Fatalf("pragma_table_info(%q): %v", col, err)
		}
		if n != 1 {
			t.Errorf("column %q missing from session_messages (count=%d)", col, n)
		}
	}
}
