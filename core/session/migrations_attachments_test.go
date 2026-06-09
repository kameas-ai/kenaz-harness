package session_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/session"
	"github.com/kameas-ai/kenaz-harness/core/storage"
	storagesqlite "github.com/kameas-ai/kenaz-harness/core/storage/sqlite"
)

// TestMigration0301_TableExistsAfterOpen verifies the new table is on
// disk after a fresh Open (i.e. migration 0301 fired).
func TestMigration0301_TableExistsAfterOpen(t *testing.T) {
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
	var name string
	row := db.Reader().QueryRow(ctx,
		"SELECT name FROM sqlite_master WHERE type='table' AND name='context_attachments'")
	if err := row.Scan(&name); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if name != "context_attachments" {
		t.Errorf("table = %q", name)
	}

	// Index ride-along.
	if err := db.Reader().QueryRow(ctx,
		"SELECT name FROM sqlite_master WHERE type='index' AND name='idx_context_attachments_scope'").
		Scan(&name); err != nil {
		t.Fatalf("scan idx: %v", err)
	}
	if name != "idx_context_attachments_scope" {
		t.Errorf("index = %q", name)
	}
}

// TestMigration0301_SeedsLegacySystemPrompt drops the new table (so 0301
// becomes effectively unapplied), seeds a sessions row carrying a
// system_prompt, then re-applies 0301 by manually invoking the seed
// helper. This exercises the migration's Go-side seeding without
// fighting the registry's "applied" state.
//
// Re-running the seed is idempotent: a second invocation must not
// duplicate the row (matched by scope_kind + scope_id + content_source).
func TestMigration0301_SeedsLegacySystemPrompt(t *testing.T) {
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

	// Insert a sessions row with system_prompt — bypass the manager so
	// the row mirrors what an old install carries.
	now := time.Now().UTC().Truncate(time.Microsecond)
	sdb := session.NewStorageDB(db)
	if err := sdb.WriteTx(ctx, func(tx session.WriteTx) error {
		_, err := tx.Exec(ctx, `
            INSERT INTO sessions
                (id, name, created_at, updated_at, last_active_at,
                 position, draft, scroll_position, archived_at,
                 system_prompt, context_kind, project_id)
            VALUES (?, ?, ?, ?, ?, 0, '', 0, NULL, ?, 'system', NULL)
        `, "old-1", "legacy", now.UnixNano(), now.UnixNano(), now.UnixNano(), "remember the alamo")
		return err
	}); err != nil {
		t.Fatalf("seed sessions row: %v", err)
	}

	// Drop the rows that 0301 seeded for our session at boot — the
	// session row was inserted *after* migrations applied, so the
	// boot-time seed never saw it. To keep the test independent of
	// boot-time ordering, clear any rows for this session and re-run
	// the seed twice to assert idempotency.
	if err := sdb.WriteTx(ctx, func(tx session.WriteTx) error {
		_, err := tx.Exec(ctx, "DELETE FROM context_attachments WHERE scope_id = ?", "old-1")
		return err
	}); err != nil {
		t.Fatalf("reset attachments: %v", err)
	}

	// Run the seed twice via the package's exported test helper.
	for i := 0; i < 2; i++ {
		if err := sdb.WriteTx(ctx, func(tx session.WriteTx) error {
			return session.SeedAttachmentsForTest(ctx, tx)
		}); err != nil {
			t.Fatalf("seed pass %d: %v", i, err)
		}
	}

	hash := sha256.Sum256([]byte("remember the alamo"))
	wantSource := "inline:" + hex.EncodeToString(hash[:])

	rows, err := db.Reader().Query(ctx, `
        SELECT content_source, kind, position, scope_kind, content
        FROM context_attachments
        WHERE scope_id = 'old-1'
    `)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var (
			gotSource, gotKind, gotScope, gotContent string
			gotPos                                   int
		)
		if err := rows.Scan(&gotSource, &gotKind, &gotPos, &gotScope, &gotContent); err != nil {
			t.Fatalf("scan: %v", err)
		}
		if gotSource != wantSource {
			t.Errorf("source = %q, want %q", gotSource, wantSource)
		}
		if gotKind != "system" {
			t.Errorf("kind = %q, want system", gotKind)
		}
		if gotPos != 0 {
			t.Errorf("position = %d, want 0", gotPos)
		}
		if gotScope != "session" {
			t.Errorf("scope_kind = %q, want session", gotScope)
		}
		if gotContent != "remember the alamo" {
			t.Errorf("content = %q", gotContent)
		}
		count++
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows err: %v", err)
	}
	if count != 1 {
		t.Errorf("attachment row count = %d, want 1 (idempotency violated?)", count)
	}
}

// TestMigration0301_UserSeedKindMapsToUser confirms a session created
// with context_kind='user_seed' produces an attachment of kind='user'.
func TestMigration0301_UserSeedKindMapsToUser(t *testing.T) {
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

	// Seed a sessions row with kind='user_seed' via the SQL store — that
	// path validates the kind enum.
	store := session.NewSQLStore(session.NewStorageDB(db))
	now := time.Now().UTC().Truncate(time.Microsecond)
	rec := session.Record{
		ID:           "us-1",
		Name:         "with seed",
		CreatedAt:    now,
		UpdatedAt:    now,
		LastActiveAt: now,
		Position:     0,
		SystemPrompt: "user-seed snapshot",
		ContextKind:  session.ContextKindUserSeed,
	}
	if err := store.Create(ctx, rec); err != nil {
		t.Fatalf("Create: %v", err)
	}

	sdb := session.NewStorageDB(db)
	if err := sdb.WriteTx(ctx, func(tx session.WriteTx) error {
		return session.SeedAttachmentsForTest(ctx, tx)
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	var kind string
	if err := db.Reader().QueryRow(ctx,
		"SELECT kind FROM context_attachments WHERE scope_id='us-1'",
	).Scan(&kind); err != nil {
		t.Fatalf("query kind: %v", err)
	}
	if kind != "user" {
		t.Errorf("kind = %q, want user", kind)
	}
}
