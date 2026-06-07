package usage_test

import (
	"context"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/session"
	"github.com/kameas-ai/kenaz-harness/core/storage"
	storagesqlite "github.com/kameas-ai/kenaz-harness/core/storage/sqlite"
	"github.com/kameas-ai/kenaz-harness/core/usage"
)

func openTestDB(t *testing.T) storage.DB {
	t.Helper()
	cfg := storage.Config{
		DataDir:          t.TempDir(),
		EncryptionStatus: storage.EncryptionStatusDisabledWithDiskEncryption,
	}
	db, err := storagesqlite.Open(cfg)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close(context.Background()) })
	return db
}

func seedSession(t *testing.T, db storage.DB, sessionID string) {
	t.Helper()
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
		`, sessionID, "test session", now.UnixNano(), now.UnixNano(), now.UnixNano())
		return err
	}); err != nil {
		t.Fatalf("seed session %q: %v", sessionID, err)
	}
}

func seedMessage(t *testing.T, db storage.DB, sessionID, msgID, role string, seq int64) {
	t.Helper()
	ctx := context.Background()
	sdb := session.NewStorageDB(db)
	now := time.Now().UTC().Truncate(time.Microsecond)
	if err := sdb.WriteTx(ctx, func(tx session.WriteTx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO session_messages
				(id, session_id, sequence, role, content, created_at)
			VALUES (?, ?, ?, ?, '', ?)
		`, msgID, sessionID, seq, role, now.UnixNano())
		return err
	}); err != nil {
		t.Fatalf("seed message %q: %v", msgID, err)
	}
}

func TestManager_Add_GetSession_Provider(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	seedSession(t, db, "sess-1")
	seedMessage(t, db, "sess-1", "msg-1", "assistant", 1)

	mgr := usage.New(db)
	ctx := context.Background()

	cost := 0.012
	if err := mgr.Add(ctx, usage.UsageTurn{
		SessionID:        "sess-1",
		MessageID:        "msg-1",
		ProviderKind:     "openrouter",
		ModelID:          "anthropic/claude-sonnet",
		PromptTokens:     100,
		CompletionTokens: 50,
		CostUSD:          &cost,
		CostSource:       "provider",
	}); err != nil {
		t.Fatalf("Add: %v", err)
	}

	agg, err := mgr.GetSession(ctx, "sess-1")
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if agg.PromptTokens != 100 {
		t.Errorf("PromptTokens = %d, want 100", agg.PromptTokens)
	}
	if agg.CompletionTokens != 50 {
		t.Errorf("CompletionTokens = %d, want 50", agg.CompletionTokens)
	}
	if agg.TotalTokens != 150 {
		t.Errorf("TotalTokens = %d, want 150", agg.TotalTokens)
	}
	if agg.CostSource != "provider" {
		t.Errorf("CostSource = %q, want provider", agg.CostSource)
	}
	if agg.MessageCount != 1 {
		t.Errorf("MessageCount = %d, want 1", agg.MessageCount)
	}
}

func TestManager_Add_GetSession_Derived(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	seedSession(t, db, "sess-2")
	seedMessage(t, db, "sess-2", "msg-2", "assistant", 1)

	mgr := usage.New(db)
	ctx := context.Background()

	cost := 0.005
	if err := mgr.Add(ctx, usage.UsageTurn{
		SessionID:        "sess-2",
		MessageID:        "msg-2",
		ProviderKind:     "anthropic",
		ModelID:          "claude-sonnet-4-5",
		PromptTokens:     200,
		CompletionTokens: 100,
		CostUSD:          &cost,
		CostSource:       "derived",
	}); err != nil {
		t.Fatalf("Add: %v", err)
	}

	agg, err := mgr.GetSession(ctx, "sess-2")
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if agg.CostSource != "derived" {
		t.Errorf("CostSource = %q, want derived", agg.CostSource)
	}
}

func TestManager_GetSession_Unknown_NoRows(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	seedSession(t, db, "sess-empty")

	mgr := usage.New(db)
	ctx := context.Background()

	agg, err := mgr.GetSession(ctx, "sess-empty")
	if err != nil {
		t.Fatalf("GetSession on empty session: %v", err)
	}
	if agg.PromptTokens != 0 || agg.CostUSD != 0 {
		t.Errorf("expected zeros for empty session, got %+v", agg)
	}
	if agg.CostSource != "unknown" {
		t.Errorf("CostSource = %q, want unknown for empty session", agg.CostSource)
	}
}

func TestManager_GetSession_MixedSource(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	seedSession(t, db, "sess-mix")
	seedMessage(t, db, "sess-mix", "msg-a", "assistant", 1)
	seedMessage(t, db, "sess-mix", "msg-b", "assistant", 2)

	mgr := usage.New(db)
	ctx := context.Background()

	costA := 0.01
	if err := mgr.Add(ctx, usage.UsageTurn{
		SessionID: "sess-mix", MessageID: "msg-a",
		PromptTokens: 100, CompletionTokens: 50,
		CostUSD: &costA, CostSource: "provider",
	}); err != nil {
		t.Fatalf("Add A: %v", err)
	}

	costB := 0.02
	if err := mgr.Add(ctx, usage.UsageTurn{
		SessionID: "sess-mix", MessageID: "msg-b",
		PromptTokens: 200, CompletionTokens: 100,
		CostUSD: &costB, CostSource: "derived",
	}); err != nil {
		t.Fatalf("Add B: %v", err)
	}

	agg, err := mgr.GetSession(ctx, "sess-mix")
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if agg.CostSource != "mixed" {
		t.Errorf("CostSource = %q, want mixed", agg.CostSource)
	}
	if agg.PromptTokens != 300 {
		t.Errorf("PromptTokens = %d, want 300", agg.PromptTokens)
	}
}

func TestManager_Add_EmptyMessageID_IsNoop(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)

	mgr := usage.New(db)
	// Should not error when MessageID is empty.
	if err := mgr.Add(context.Background(), usage.UsageTurn{
		SessionID: "sess-x", MessageID: "", PromptTokens: 10,
	}); err != nil {
		t.Fatalf("unexpected error on empty MessageID: %v", err)
	}
}

func TestNoopManager_EnvOff(t *testing.T) {
	t.Setenv("HARNESS_COST_TELEMETRY", "off")
	// nil db is fine for noop path.
	mgr := usage.New(nil)
	ctx := context.Background()
	cost := 0.01
	if err := mgr.Add(ctx, usage.UsageTurn{
		SessionID: "x", MessageID: "y", CostUSD: &cost, CostSource: "provider",
	}); err != nil {
		t.Errorf("noopManager.Add: %v", err)
	}
	agg, err := mgr.GetSession(ctx, "x")
	if err != nil {
		t.Errorf("noopManager.GetSession: %v", err)
	}
	if agg.CostSource != "unknown" {
		t.Errorf("noopManager should return unknown source, got %q", agg.CostSource)
	}
}
