package sqlite_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/session"
	"github.com/kameas-ai/kenaz-harness/core/storage"
	storagesqlite "github.com/kameas-ai/kenaz-harness/core/storage/sqlite"

	_ "modernc.org/sqlite"
)

func newConfig(dir string) storage.Config {
	return storage.Config{
		DataDir:          dir,
		EncryptionStatus: storage.EncryptionStatusDisabledWithDiskEncryption,
	}
}

func mustOpen(t *testing.T, dir string) storage.DB {
	t.Helper()
	db, err := storagesqlite.Open(newConfig(dir))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close(context.Background())
	})
	return db
}

func TestOpen_HappyPath(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	db := mustOpen(t, dir)

	ctx := context.Background()
	r := db.Reader()

	var journalMode string
	if err := r.QueryRow(ctx, "PRAGMA journal_mode").Scan(&journalMode); err != nil {
		t.Fatalf("PRAGMA journal_mode: %v", err)
	}
	if journalMode != "wal" {
		t.Errorf("journal_mode = %q, want wal", journalMode)
	}

	var fk int
	if err := r.QueryRow(ctx, "PRAGMA foreign_keys").Scan(&fk); err != nil {
		t.Fatalf("PRAGMA foreign_keys: %v", err)
	}
	if fk != 1 {
		t.Errorf("foreign_keys = %d, want 1", fk)
	}

	var busy int
	if err := r.QueryRow(ctx, "PRAGMA busy_timeout").Scan(&busy); err != nil {
		t.Fatalf("PRAGMA busy_timeout: %v", err)
	}
	if busy != 5000 {
		t.Errorf("busy_timeout = %d, want 5000", busy)
	}

	if _, err := os.Stat(filepath.Join(dir, "data.db")); err != nil {
		t.Errorf("data.db not on disk: %v", err)
	}
}

func TestOpen_RejectsEncryptionEnabled(t *testing.T) {
	t.Parallel()
	cfg := newConfig(t.TempDir())
	cfg.EncryptionStatus = storage.EncryptionStatusEnabled
	cfg.EncryptionKey = storage.CredentialReference{Keychain: "harness-test"}
	_, err := storagesqlite.Open(cfg)
	if !errors.Is(err, storage.ErrNotImplemented) {
		t.Fatalf("got %v, want ErrNotImplemented", err)
	}
}

func TestOpen_LockHeld(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	first := mustOpen(t, dir)
	defer first.Close(context.Background())

	_, err := storagesqlite.Open(newConfig(dir))
	if !errors.Is(err, storage.ErrDBLocked) {
		t.Fatalf("got %v, want ErrDBLocked", err)
	}
}

func TestOpen_RegistersSessionMigrations(t *testing.T) {
	t.Parallel()
	db := mustOpen(t, t.TempDir())
	ctx := context.Background()
	for _, table := range []string{"sessions", "session_messages"} {
		var name string
		row := db.Reader().QueryRow(ctx,
			"SELECT name FROM sqlite_master WHERE type='table' AND name=?", table)
		if err := row.Scan(&name); err != nil {
			t.Errorf("table %q missing: %v", table, err)
			continue
		}
		if name != table {
			t.Errorf("table lookup for %q returned %q", table, name)
		}
	}

	// Confirm the migration ledger recorded the session block.
	rows, err := db.Reader().Query(ctx,
		"SELECT version FROM harness_migrations WHERE owning_mission='sessions' ORDER BY version")
	if err != nil {
		t.Fatalf("query ledger: %v", err)
	}
	defer rows.Close()
	var versions []int
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			t.Fatal(err)
		}
		versions = append(versions, v)
	}
	// 0306 (branches) lands with the agent-kernel-graph branching bundle;
	// 0307 (corpora), 0308 (memory_hook_journal), 0309 (agent_graph_events);
	// 0310 (compaction); 0311 (auto_titled); 0312 (FTS5 messages_fts);
	// 0313 (subagent-metadata) lands with branch-as-subagent-recommendation WP04;
	// 0314 (session_usage_columns); 0315 (cost_threshold_fired) lands with
	// token-cost-telemetry-01KQ8TD7 WP06; 0316 (autonomy_columns) lands
	// with autonomy-dial-01KR3M2A WP02; 0317 (streaming-resume columns on
	// session_messages) lands with long-turn-resilience-01KR3PRS WP03;
	// 0318 (sessions.kind) lands with harness-self-mcp-onboarding-01KQ8TDU
	// WP03; 0319 (workflows + workflow_versions) lands with
	// workflows-01KQ8TDG WP06; 0320 (workflow_runs_cache) lands with
	// workflows-01KQ8TDG WP08; 0321 (workflow_schedules) lands with
	// workflows-agentic-01KW2D3X WP02; 0322 (last_usage_json) lands with
	// backend-context-window-length-01KQ8TD3 WP02; 0323 (branch display-meta
	// columns) lands with branching-ux-polish-01KQ8TD7 WP01.
	// 0324 (artifact_versions); 0325 (scheduled_chat_runs + indexes) lands
	// with scheduled-chat-runs-01KX5R8B WP01.
	// 0326 (agent_graph_node_provenance) lands with
	// manifest-versioning-01NDFSEX02 WP03.
	// 0327 (source_model_output) lands with
	// multimodal-io-extended-01KQ8TD2 WP02.
	// 0328 (media_artifact_meta: image_width/image_height/page_count columns)
	// lands with multimodal-io-01KQ8TDF FR-017.
	// 0329 (provider_capabilities) + 0330 (knobs) land with
	// provider-implementation-uniformity-01KQ8V4F WP05/WP09.
	// 0331 (custom_endpoint_capabilities) lands with
	// custom-openai-compatible-endpoint-01KQ8VN0 WP03.
	// 0332 (artifacts_global_scope) lands with unified-context-artifacts-01NCTXU01.
	want := []int{300, 301, 302, 303, 304, 305, 306, 307, 308, 309, 310, 311, 312, 313, 314, 315, 316, 317, 318, 319, 320, 321, 322, 323, 324, 325, 326, 327, 328, 329, 330, 331, 332}
	if len(versions) != len(want) {
		t.Fatalf("session migrations applied = %v, want %v", versions, want)
	}
	for i, v := range want {
		if versions[i] != v {
			t.Errorf("migration[%d] = %d, want %d", i, versions[i], v)
		}
	}
}

func TestOpen_ApplyIdempotent(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	first := mustOpen(t, dir)
	if err := first.Close(context.Background()); err != nil {
		t.Fatalf("first close: %v", err)
	}

	// Second Open must succeed without re-applying anything.
	second := mustOpen(t, dir)
	ctx := context.Background()
	rows, err := second.Reader().Query(ctx,
		"SELECT COUNT(*) FROM harness_migrations WHERE action='applied'")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()
	if !rows.Next() {
		t.Fatal("no count row")
	}
	var count int
	if err := rows.Scan(&count); err != nil {
		t.Fatal(err)
	}
	// 2 storage bootstrap + 1 session init + 1 context_attachments +
	// 1 content_json + 1 artifacts + 1 artifacts-promote + 1 telemetry +
	// 1 branches (0306) + 1 corpora (0307) + 1 memory_hook_journal (0308) +
	// 1 agent_graph_events (0309) + 1 compaction (0310) +
	// 1 sessions.auto_titled (0311) + 1 search-fts5 (0312) +
	// 1 subagent-metadata (0313) + 1 session_usage (0314) +
	// 1 cost_threshold_fired (0315) + 1 autonomy_columns (0316) +
	// 1 streaming-resume (0317) + 1 sessions.kind (0318) +
	// 1 workflows (0319) + 1 workflow_runs_cache (0320) +
	// 1 workflow_schedules (0321) + 1 last_usage_json (0322) +
	// 1 branch-display-meta (0323) + 1 artifact_versions (0324) +
	// 1 scheduled_chat_runs (0325, scheduled-chat-runs-01KX5R8B) +
	// 1 agent_graph_node_provenance (0326, manifest-versioning-01NDFSEX02) +
	// 1 source_model_output (0327, multimodal-io-extended-01KQ8TD2) +
	// 1 media_artifact_meta (0328, multimodal-io-01KQ8TDF FR-017) +
	// 1 provider_capabilities (0329, provider-implementation-uniformity-01KQ8V4F) +
	// 1 knobs (0330, provider-implementation-uniformity-01KQ8V4F) +
	// 1 custom_endpoint_capabilities (0331, custom-openai-compatible-endpoint) +
	// 1 artifacts_global_scope (0332, unified-context-artifacts-01NCTXU01) +
	// 1 slash_commands_user (1000) +
	// 1 units (1100, unified-context-artifacts-01NCTXU01) = 37.
	if count != 37 {
		t.Errorf("ledger count = %d, want 37", count)
	}
}

func TestSessionRoundTrip_ThroughAdapter(t *testing.T) {
	t.Parallel()
	db := mustOpen(t, t.TempDir())
	store := session.NewSQLStore(session.NewStorageDB(db))

	ctx := context.Background()
	now := time.Now().UTC()
	rec := session.Record{
		ID:           "s1",
		Name:         "first",
		CreatedAt:    now,
		UpdatedAt:    now,
		LastActiveAt: now,
		Position:     0,
		ContextKind:  session.ContextKindSystem,
	}
	if err := store.Create(ctx, rec); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := store.Get(ctx, "s1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ID != "s1" || got.Name != "first" {
		t.Errorf("round-trip = (%q,%q), want (s1, first)", got.ID, got.Name)
	}

	msg, err := store.AppendMessage(ctx, session.Message{
		ID:        "m1",
		SessionID: "s1",
		Role:      session.RoleUser,
		Content:   "hello",
		CreatedAt: now,
	})
	if err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}
	if msg.Sequence != 0 {
		t.Errorf("first sequence = %d, want 0", msg.Sequence)
	}
	msgs, err := store.ListMessages(ctx, "s1")
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if len(msgs) != 1 || msgs[0].Content != "hello" {
		t.Errorf("messages = %v, want one with content hello", msgs)
	}
}

