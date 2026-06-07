package search_test

// Integration test for cross-session-search WP08.
//
// Opens a real storage.DB (which runs the FTS5 migration 0312), inserts
// three session_messages via the session.Store API, then drives the
// SearchAPI end-to-end and asserts:
//
//   - Each indexed message is queryable by a unique term.
//   - Snippet text comes back without delimiter bytes.
//   - Highlights mark the matched span (non-empty + in-bounds).
//   - The audit emitter receives one search.executed event per Search call
//     with no raw query payload (only query_hash + result_count).
//   - The Enabled gate short-circuits results to nil.
//
// This is the WP08 smoke harness — exists to lock the contract for v0.3.0
// beta. Performance gates (NFR-001 < 200 ms p95, NFR-002 < 30% disk) are
// out of scope here; they belong in a longer-running benchmark suite.

import (
	"context"
	"database/sql"
	"sync"
	"testing"
	"time"

	searchview "github.com/kameas-ai/kenaz-harness/core/rpc/views/search"
	"github.com/kameas-ai/kenaz-harness/core/session"
	"github.com/kameas-ai/kenaz-harness/core/storage"
	storagesqlite "github.com/kameas-ai/kenaz-harness/core/storage/sqlite"
)

// openTestDB opens a real on-disk storage.DB rooted at a tmpdir so the
// FTS5 migration runs and the AFTER INSERT trigger fires for every row
// AppendMessage writes.
func openTestDB(t *testing.T) (storage.DB, *sql.DB) {
	t.Helper()
	cfg := storage.Config{
		DataDir:          t.TempDir(),
		EncryptionStatus: storage.EncryptionStatusDisabledWithDiskEncryption,
	}
	db, err := storagesqlite.Open(cfg)
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close(context.Background()) })

	// SearchAPI consumes the raw *sql.DB (NewManagerAPI signature) — pull
	// it via the structural SQL() escape hatch the storage backend exposes.
	type sqlHandle interface{ SQL() *sql.DB }
	h, ok := db.(sqlHandle)
	if !ok {
		t.Fatalf("storage backend does not expose SQL() *sql.DB")
	}
	raw := h.SQL()
	if raw == nil {
		t.Fatalf("SQL() returned nil")
	}
	return db, raw
}

// captureEmitter buffers every Emit call so the test can assert audit
// payload shape without touching the real event subsystem.
type captureEmitter struct {
	mu     sync.Mutex
	events []capturedEvent
}

type capturedEvent struct {
	kind  string
	attrs map[string]any
}

func (c *captureEmitter) Emit(_ context.Context, kind string, attrs map[string]any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	cp := make(map[string]any, len(attrs))
	for k, v := range attrs {
		cp[k] = v
	}
	c.events = append(c.events, capturedEvent{kind: kind, attrs: cp})
}

func (c *captureEmitter) snapshot() []capturedEvent {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]capturedEvent, len(c.events))
	copy(out, c.events)
	return out
}

// TestSearchEndToEnd_ThreeMessages indexes three messages across one
// session and asserts each is queryable with non-empty highlights.
func TestSearchEndToEnd_ThreeMessages(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	db, raw := openTestDB(t)
	store := session.NewSQLStore(session.NewStorageDB(db))
	now := time.Now().UTC()

	rec := session.Record{
		ID:           "s-int-1",
		Name:         "integration session",
		CreatedAt:    now,
		UpdatedAt:    now,
		LastActiveAt: now,
		Position:     0,
		ContextKind:  session.ContextKindSystem,
	}
	if err := store.Create(ctx, rec); err != nil {
		t.Fatalf("Create session: %v", err)
	}

	// Three messages with disjoint distinctive terms so each query
	// returns exactly one hit.
	msgs := []session.Message{
		{ID: "m1", SessionID: "s-int-1", Role: session.RoleUser, Content: "alpha quokka jumps", CreatedAt: now},
		{ID: "m2", SessionID: "s-int-1", Role: session.RoleAssistant, Content: "beta wombat sleeps", CreatedAt: now.Add(time.Second)},
		{ID: "m3", SessionID: "s-int-1", Role: session.RoleUser, Content: "gamma platypus swims", CreatedAt: now.Add(2 * time.Second)},
	}
	for _, m := range msgs {
		if _, err := store.AppendMessage(ctx, m); err != nil {
			t.Fatalf("AppendMessage %s: %v", m.ID, err)
		}
	}

	emitter := &captureEmitter{}
	api := searchview.NewManagerAPIWithConfig(raw, searchview.Config{Audit: emitter})

	// ── per-term assertions ────────────────────────────────────────────
	cases := []struct {
		query     string
		wantMsgID string
	}{
		{"quokka", "m1"},
		{"wombat", "m2"},
		{"platypus", "m3"},
	}
	for _, tc := range cases {
		hits, err := api.Search(ctx, tc.query, searchview.SearchFilters{})
		if err != nil {
			t.Fatalf("Search(%q): %v", tc.query, err)
		}
		if len(hits) != 1 {
			t.Fatalf("Search(%q): want 1 hit, got %d", tc.query, len(hits))
		}
		hit := hits[0]
		if hit.MessageID != tc.wantMsgID {
			t.Errorf("Search(%q): MessageID = %q, want %q", tc.query, hit.MessageID, tc.wantMsgID)
		}
		if hit.SessionID != "s-int-1" {
			t.Errorf("Search(%q): SessionID = %q, want s-int-1", tc.query, hit.SessionID)
		}
		if hit.Snippet == "" {
			t.Errorf("Search(%q): empty Snippet", tc.query)
		}
		// Snippet must NEVER carry the STX/ETX delimiter bytes — they
		// are stripped by extractHighlights and re-presented as the
		// Highlights array.
		for _, b := range []byte(hit.Snippet) {
			if b == 0x02 || b == 0x03 {
				t.Errorf("Search(%q): snippet leaks delimiter byte 0x%02x", tc.query, b)
			}
		}
		if len(hit.Highlights) == 0 {
			t.Errorf("Search(%q): no highlights returned", tc.query)
		}
		for _, hl := range hit.Highlights {
			if hl.Start < 0 || hl.End > len(hit.Snippet) || hl.Start >= hl.End {
				t.Errorf("Search(%q): highlight %v out of bounds (snippet len=%d)", tc.query, hl, len(hit.Snippet))
			}
		}
	}

	// ── audit assertions ──────────────────────────────────────────────
	events := emitter.snapshot()
	if len(events) != len(cases) {
		t.Fatalf("audit emitter saw %d events, want %d", len(events), len(cases))
	}
	for i, ev := range events {
		if ev.kind != searchview.KindSearchExecuted {
			t.Errorf("event %d kind = %q, want %q", i, ev.kind, searchview.KindSearchExecuted)
		}
		// Privacy: raw query MUST NOT appear in the attrs.
		if _, has := ev.attrs["query"]; has {
			t.Errorf("event %d leaked raw 'query' key in attrs: %v", i, ev.attrs)
		}
		if _, has := ev.attrs["text"]; has {
			t.Errorf("event %d leaked raw 'text' key in attrs: %v", i, ev.attrs)
		}
		// Required keys.
		for _, k := range []string{"query_hash", "result_count", "filters_applied", "took_ms"} {
			if _, has := ev.attrs[k]; !has {
				t.Errorf("event %d missing required key %q: %v", i, k, ev.attrs)
			}
		}
		// query_hash must be a 16-char hex string (the docstring on
		// queryHash promises this).
		if h, _ := ev.attrs["query_hash"].(string); len(h) != 16 {
			t.Errorf("event %d query_hash len = %d, want 16", i, len(h))
		}
		if rc, _ := ev.attrs["result_count"].(int); rc != 1 {
			t.Errorf("event %d result_count = %d, want 1", i, rc)
		}
	}
}

// TestSearchEnabledGate_ShortCircuits asserts that when the Enabled
// callback returns false, Search returns nil without touching the
// index AND without emitting an audit event.
func TestSearchEnabledGate_ShortCircuits(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	db, raw := openTestDB(t)
	store := session.NewSQLStore(session.NewStorageDB(db))
	now := time.Now().UTC()
	if err := store.Create(ctx, session.Record{
		ID: "s-int-2", Name: "gated", CreatedAt: now, UpdatedAt: now,
		LastActiveAt: now, ContextKind: session.ContextKindSystem,
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := store.AppendMessage(ctx, session.Message{
		ID: "m-gated", SessionID: "s-int-2", Role: session.RoleUser,
		Content: "indexable echidna", CreatedAt: now,
	}); err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}

	emitter := &captureEmitter{}
	api := searchview.NewManagerAPIWithConfig(raw, searchview.Config{
		Audit:   emitter,
		Enabled: func() bool { return false },
	})

	hits, err := api.Search(ctx, "echidna", searchview.SearchFilters{})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) != 0 {
		t.Errorf("disabled Search returned %d hits, want 0", len(hits))
	}
	// No audit event should fire when the gate short-circuits.
	if got := emitter.snapshot(); len(got) != 0 {
		t.Errorf("disabled Search emitted %d audit events, want 0", len(got))
	}
}

// TestSearchEndToEnd_FilterByRole asserts the role filter narrows the
// hit set in the real-DB path.
func TestSearchEndToEnd_FilterByRole(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	db, raw := openTestDB(t)
	store := session.NewSQLStore(session.NewStorageDB(db))
	now := time.Now().UTC()
	if err := store.Create(ctx, session.Record{
		ID: "s-int-3", Name: "role filter", CreatedAt: now, UpdatedAt: now,
		LastActiveAt: now, ContextKind: session.ContextKindSystem,
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	for _, m := range []session.Message{
		{ID: "u1", SessionID: "s-int-3", Role: session.RoleUser, Content: "shared keyword foo", CreatedAt: now},
		{ID: "a1", SessionID: "s-int-3", Role: session.RoleAssistant, Content: "shared keyword bar", CreatedAt: now.Add(time.Second)},
	} {
		if _, err := store.AppendMessage(ctx, m); err != nil {
			t.Fatalf("AppendMessage %s: %v", m.ID, err)
		}
	}

	api := searchview.NewManagerAPI(raw)

	// Unfiltered: both rows.
	all, err := api.Search(ctx, "shared", searchview.SearchFilters{})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(all) != 2 {
		t.Errorf("unfiltered hit count = %d, want 2", len(all))
	}

	// Role-filtered: only the assistant row.
	asst, err := api.Search(ctx, "shared", searchview.SearchFilters{RoleFilter: "assistant"})
	if err != nil {
		t.Fatalf("Search(role): %v", err)
	}
	if len(asst) != 1 {
		t.Errorf("role-filtered hit count = %d, want 1", len(asst))
	}
	if len(asst) == 1 && asst[0].MessageID != "a1" {
		t.Errorf("role-filtered MessageID = %q, want a1", asst[0].MessageID)
	}
}
