package session_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/sigil-tech/kaneaz-harness/core/llm"
	"github.com/sigil-tech/kaneaz-harness/core/session"
	"github.com/sigil-tech/kaneaz-harness/core/storage"
	storagesqlite "github.com/sigil-tech/kaneaz-harness/core/storage/sqlite"
)

// TestMigration0302_TablesExistAfterOpen verifies the new table +
// index ride along with content_json on a fresh Open.
func TestMigration0302_TablesExistAfterOpen(t *testing.T) {
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
	for _, want := range []struct {
		kind string
		name string
	}{
		{"table", "media_artifacts"},
		{"index", "idx_media_artifacts_content_hash"},
	} {
		var got string
		row := db.Reader().QueryRow(ctx,
			"SELECT name FROM sqlite_master WHERE type=? AND name=?", want.kind, want.name)
		if err := row.Scan(&got); err != nil {
			t.Errorf("%s %q missing: %v", want.kind, want.name, err)
			continue
		}
		if got != want.name {
			t.Errorf("%s %q -> %q", want.kind, want.name, got)
		}
	}

	// content_json column exists on session_messages.
	row := db.Reader().QueryRow(ctx,
		"SELECT COUNT(*) FROM pragma_table_info('session_messages') WHERE name='content_json'")
	var n int
	if err := row.Scan(&n); err != nil {
		t.Fatalf("pragma_table_info: %v", err)
	}
	if n != 1 {
		t.Errorf("content_json column missing from session_messages")
	}
	// media_id column exists on context_attachments with ON DELETE SET NULL.
	row = db.Reader().QueryRow(ctx,
		"SELECT COUNT(*) FROM pragma_table_info('context_attachments') WHERE name='media_id'")
	if err := row.Scan(&n); err != nil {
		t.Fatalf("pragma_table_info media_id: %v", err)
	}
	if n != 1 {
		t.Errorf("media_id column missing from context_attachments")
	}
}

// TestMigration0302_WP01ShapeRowsRoundTrip emulates A6: a v1-shape DB
// (rows written before content_json existed) reads back via the new
// loader as a single text block. This is the migration-clean upgrade
// from spec.A6.
func TestMigration0302_WP01ShapeRowsRoundTrip(t *testing.T) {
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

	// Seed a sessions + session_messages row with content_json NULL —
	// simulates a row written before 0302 landed.
	sdb := session.NewStorageDB(db)
	now := time.Now().UTC().Truncate(time.Microsecond)
	if err := sdb.WriteTx(ctx, func(tx session.WriteTx) error {
		if _, err := tx.Exec(ctx, `
            INSERT INTO sessions
                (id, name, created_at, updated_at, last_active_at,
                 position, draft, scroll_position, archived_at,
                 system_prompt, context_kind, project_id)
            VALUES (?, ?, ?, ?, ?, ?, '', 0, NULL, '', 'system', NULL)
        `, "legacy-1", "v1 session", now.UnixNano(), now.UnixNano(), now.UnixNano(), 0); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `
            INSERT INTO session_messages
                (id, session_id, sequence, role, content, tool_calls, created_at, content_json)
            VALUES (?, ?, ?, ?, ?, NULL, ?, NULL)
        `, "msg-1", "legacy-1", 0, "user", "hello legacy", now.UnixNano())
		return err
	}); err != nil {
		t.Fatalf("seed legacy row: %v", err)
	}

	store := session.NewSQLStore(sdb)
	msgs, err := store.ListMessages(ctx, "legacy-1")
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("len(msgs) = %d, want 1", len(msgs))
	}
	got := msgs[0]
	if got.Content != "hello legacy" {
		t.Errorf("Content = %q", got.Content)
	}
	if len(got.ContentBlocks) != 1 {
		t.Fatalf("ContentBlocks = %d, want 1", len(got.ContentBlocks))
	}
	if got.ContentBlocks[0].Type != "text" || got.ContentBlocks[0].Text != "hello legacy" {
		t.Errorf("ContentBlocks[0] = %+v", got.ContentBlocks[0])
	}
}

// TestStore_AppendMessage_WritesContentJSON verifies the new write
// path fills both columns, and ListMessages re-hydrates the
// ContentBlocks shape verbatim including a non-text block.
func TestStore_AppendMessage_WritesContentJSON(t *testing.T) {
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
	store := session.NewSQLStore(sdb)
	now := time.Now().UTC().Truncate(time.Microsecond)
	rec := session.Record{
		ID:           "s1",
		Name:         "mixed",
		CreatedAt:    now,
		UpdatedAt:    now,
		LastActiveAt: now,
		Position:     0,
		ContextKind:  session.ContextKindSystem,
	}
	if err := store.Create(ctx, rec); err != nil {
		t.Fatalf("Create: %v", err)
	}
	mixed := []llm.ContentBlock{
		{Type: "text", Text: "describe this image"},
		{Type: "image", Source: &llm.MediaSource{
			Kind:      "base64",
			MediaType: "image/png",
			Data:      "ZmFrZS1wbmctZGF0YQ==",
		}},
	}
	out, err := store.AppendMessage(ctx, session.Message{
		ID:            "m1",
		SessionID:     "s1",
		Role:          session.RoleUser,
		ContentBlocks: mixed,
		CreatedAt:     now,
	})
	if err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}

	// Legacy `content` column carries the flattened text.
	if out.Content != "describe this image" {
		t.Errorf("Content = %q", out.Content)
	}

	// Read back via ListMessages — content_json must drive the result.
	msgs, err := store.ListMessages(ctx, "s1")
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("len(msgs) = %d", len(msgs))
	}
	if len(msgs[0].ContentBlocks) != 2 {
		t.Fatalf("blocks = %d", len(msgs[0].ContentBlocks))
	}
	if msgs[0].ContentBlocks[1].Source == nil ||
		msgs[0].ContentBlocks[1].Source.MediaType != "image/png" {
		t.Errorf("image block did not round-trip: %+v", msgs[0].ContentBlocks[1])
	}

	// Confirm the content_json column was actually populated (not NULL).
	row := db.Reader().QueryRow(ctx,
		"SELECT content_json FROM session_messages WHERE id = ?", "m1")
	var raw string
	if err := row.Scan(&raw); err != nil {
		t.Fatalf("scan content_json: %v", err)
	}
	if raw == "" {
		t.Errorf("content_json was empty")
	}
	var decoded []llm.ContentBlock
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
		t.Fatalf("invalid content_json: %v", err)
	}
	if len(decoded) != 2 {
		t.Errorf("decoded blocks = %d", len(decoded))
	}
}

// TestStore_AppendMessage_PlainTextBackcompat — callers populating
// only Content (string) round-trip through the new write path: a
// single text block lands in content_json and Content is preserved.
func TestStore_AppendMessage_PlainTextBackcompat(t *testing.T) {
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
	store := session.NewSQLStore(sdb)
	now := time.Now().UTC().Truncate(time.Microsecond)
	if err := store.Create(ctx, session.Record{
		ID:           "s1",
		Name:         "plain",
		CreatedAt:    now,
		UpdatedAt:    now,
		LastActiveAt: now,
		ContextKind:  session.ContextKindSystem,
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := store.AppendMessage(ctx, session.Message{
		ID:        "m1",
		SessionID: "s1",
		Role:      session.RoleUser,
		Content:   "plain hello",
		CreatedAt: now,
	}); err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}
	msgs, err := store.ListMessages(ctx, "s1")
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("len = %d", len(msgs))
	}
	if msgs[0].Content != "plain hello" {
		t.Errorf("Content = %q", msgs[0].Content)
	}
	if len(msgs[0].ContentBlocks) != 1 ||
		msgs[0].ContentBlocks[0].Type != "text" ||
		msgs[0].ContentBlocks[0].Text != "plain hello" {
		t.Errorf("ContentBlocks = %+v", msgs[0].ContentBlocks)
	}
}
