package session_test

import (
	"context"
	"testing"

	"github.com/sigil-tech/kaneaz-harness/core/storage"
	storagesqlite "github.com/sigil-tech/kaneaz-harness/core/storage/sqlite"
)

// TestBootMigrationLedger_RecordsSessionsBlock verifies that a fresh
// harness boot lands rows in `harness_migrations` for every
// sessions-block migration shipped to date (0300 init + 0301
// context_attachments + 0302 content_json + media_artifacts).
//
// Acceptance hook (WP07 mission close): the harness boot log must show
// the migration sequence on a freshly upgraded machine. Asserting on
// the persisted ledger is the most stable proxy — a stale binary with
// missing migration registration would fail this test before it had a
// chance to silently break the chat path.
func TestBootMigrationLedger_RecordsSessionsBlock(t *testing.T) {
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
	for _, want := range []int{300, 301, 302} {
		if !seen[want] {
			t.Errorf("expected migration %d in ledger; got %v", want, seen)
		}
	}
}
