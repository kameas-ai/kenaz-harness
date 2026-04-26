package session_test

import (
	"context"
	"testing"

	"github.com/sigil-tech/kaneaz-harness/core/storage"
	storagesqlite "github.com/sigil-tech/kaneaz-harness/core/storage/sqlite"
)

// TestBootMigrationLedger_Records300And301 verifies that a fresh harness
// boot lands rows in `harness_migrations` for the two sessions-block
// migrations the context-library mission depends on (0300 init + 0301
// context_attachments).
//
// Acceptance hook (WP07 mission close): the harness boot log must show
// the migration sequence on a freshly upgraded machine. Asserting on
// the persisted ledger is the most stable proxy — a stale binary with
// missing migration registration would fail this test before it had a
// chance to silently break the chat path.
func TestBootMigrationLedger_Records300And301(t *testing.T) {
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
	rows, err := db.Reader().Query(ctx,
		"SELECT version FROM harness_migrations ORDER BY version ASC")
	if err != nil {
		t.Fatalf("query ledger: %v", err)
	}
	defer rows.Close()
	seen := map[int]bool{}
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			t.Fatalf("scan: %v", err)
		}
		seen[v] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows.Err: %v", err)
	}
	if !seen[300] {
		t.Errorf("expected migration 300 in ledger; got %v", seen)
	}
	if !seen[301] {
		t.Errorf("expected migration 301 in ledger; got %v", seen)
	}
}
