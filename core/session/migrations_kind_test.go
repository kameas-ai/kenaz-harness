package session_test

import (
	"context"
	"testing"

	"github.com/sigil-tech/kaneaz-harness/core/session"
	"github.com/sigil-tech/kaneaz-harness/core/storage"
	storagesqlite "github.com/sigil-tech/kaneaz-harness/core/storage/sqlite"
)

// TestMigration0318_KindColumnExists verifies the kind column lands.
func TestMigration0318_KindColumnExists(t *testing.T) {
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

	var n int
	row := db.Reader().QueryRow(context.Background(),
		"SELECT COUNT(*) FROM pragma_table_info('sessions') WHERE name='kind'")
	if err := row.Scan(&n); err != nil {
		t.Fatalf("pragma_table_info: %v", err)
	}
	if n != 1 {
		t.Errorf("column kind missing from sessions (count=%d)", n)
	}
}

// TestManager_CreateWithKind asserts kind round-trips through the
// session.Manager surface and that the default Create path lands "chat".
func TestManager_CreateWithKind(t *testing.T) {
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

	store := session.NewSQLStore(session.NewStorageDB(db))
	mgr := session.NewManager(store)
	ctx := context.Background()

	chat, err := mgr.Create(ctx, "chat session")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if chat.Kind != session.SessionKindChat {
		t.Errorf("default kind = %q, want %q", chat.Kind, session.SessionKindChat)
	}

	on, err := mgr.CreateWithKind(ctx, "onboarding session", nil, session.SessionKindOnboarding)
	if err != nil {
		t.Fatalf("CreateWithKind: %v", err)
	}
	if on.Kind != session.SessionKindOnboarding {
		t.Errorf("kind = %q, want %q", on.Kind, session.SessionKindOnboarding)
	}

	got, err := mgr.Get(ctx, on.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Kind != session.SessionKindOnboarding {
		t.Errorf("round-trip kind = %q, want %q", got.Kind, session.SessionKindOnboarding)
	}
}
