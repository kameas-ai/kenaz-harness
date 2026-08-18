package rpc

// Search privacy toggle + audit trail WIRING tests (consent-surfaces-truth
// -01PMTR01 WP01 / dead-code-audit finding A3).
//
// The consumer half of this contract (core/rpc/views/search) is already
// fully tested against a real sqlite FTS5 index in
// core/rpc/views/search/integration_test.go — this file exists because
// that coverage proves nothing about api.go's Search() wiring method,
// which is the thing that was actually broken: cfg.Enabled and cfg.Audit
// were never assigned, so the on-screen copy's promises ("Turning this
// off short-circuits the search and never touches the index"; "Search
// activity is logged with a truncated query_hash") were both false no
// matter how correct the consumer-side logic was.
//
// These tests boot a real core.Core + rpc.API over a temp DataDir (real
// sqlite, real FTS5 migration — session.NewSQLStore, not
// session.NewMemoryStore, per the blind-spot-#2 discipline in CLAUDE.md),
// flip the persisted Settings.SearchDisabled bit through the same
// full-settings round trip the frontend uses, and assert the observable
// refusal/audit row on the SAME long-lived *API instance the app would
// hold for a session — proving the dial is a live read, not a boot
// snapshot.

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core"
	auditview "github.com/kameas-ai/kenaz-harness/core/rpc/views/audit"
	searchview "github.com/kameas-ai/kenaz-harness/core/rpc/views/search"
	"github.com/kameas-ai/kenaz-harness/core/session"
)

// searchWiringAPI boots a real Core + rpc.API over a temp DataDir, with
// the settings store sandboxed away from the developer's real
// ~/Library/Application Support (see sandboxUserConfigDir's doc comment
// for why this is load-bearing, not decorative).
func searchWiringAPI(t *testing.T) *API {
	t.Helper()
	sandboxUserConfigDir(t)
	dataDir := t.TempDir()
	c, err := core.New(core.Options{DataDir: dataDir})
	if err != nil {
		t.Fatalf("core.New: %v", err)
	}
	api := New(c)
	assertSettingsStoreIsSandboxed(t, api)
	return api
}

// seedSearchableMessage writes one session + one message carrying a
// unique, distinctive token directly through the session store backing
// the same *sql.DB the rpc.API's Search() method lazily wires itself
// onto (api.go's a.core.Storage() → SQL() escape hatch). Real sqlite,
// real messages_fts — not session.NewMemoryStore(), which would skip
// the very index this WP's acceptance criteria are about.
func seedSearchableMessage(t *testing.T, api *API, sessionID, token string) {
	t.Helper()
	store := session.NewSQLStore(session.NewStorageDB(api.core.Storage()))
	now := time.Now().UTC()
	if err := store.Create(context.Background(), session.Record{
		ID:           sessionID,
		Name:         "search wiring test",
		CreatedAt:    now,
		UpdatedAt:    now,
		LastActiveAt: now,
		ContextKind:  session.ContextKindSystem,
	}); err != nil {
		t.Fatalf("Create session: %v", err)
	}
	if _, err := store.AppendMessage(context.Background(), session.Message{
		ID:        sessionID + "-m1",
		SessionID: sessionID,
		Role:      session.RoleUser,
		Content:   token,
		CreatedAt: now,
	}); err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}
}

// TestSearchWiring_ToggleGatesLiveOnSameInstance is FR-001's headline
// acceptance test: with the toggle on, a query returns hits; flipped off
// mid-process (no restart, same *API, same cached SearchAPI), the next
// call returns zero; flipped back on, hits return again.
//
// Mutation: revert cfg.Enabled to unassigned (nil) in api.go's Search()
//
//	→ EnabledFn nil means "always enabled" (impl.go:286) → the
//	  toggled-off assertion fails.
//
// Mutation: replace `if a.enabled != nil && !a.enabled()` with `if
//
//	false` in impl.go → same failure, from the consumer side.
//
// Mutation: snapshot cfg.Enabled's *value* once at wiring time instead
//
//	of closing over the live settings store (e.g. `enabled :=
//	s.SearchEnabled(); cfg.Enabled = func() bool { return enabled }`)
//	→ the toggle-back-on assertion at the end of this test fails,
//	  because Search() memoizes a.searchAPI and never re-wires cfg.
func TestSearchWiring_ToggleGatesLiveOnSameInstance(t *testing.T) {
	ctx := context.Background()
	api := searchWiringAPI(t)
	const token = "zzqorvantoken"
	seedSearchableMessage(t, api, "s-wire-1", token)

	sa := api.Search()
	filters := searchview.SearchFilters{}

	// Toggle on (default: SearchDisabled is zero-value false).
	hits, err := sa.Search(ctx, token, filters)
	if err != nil {
		t.Fatalf("Search (enabled): %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("Search (enabled): got %d hits, want 1 — %#v", len(hits), hits)
	}
	uhits, err := sa.UnifiedSearch(ctx, token, filters)
	if err != nil {
		t.Fatalf("UnifiedSearch (enabled): %v", err)
	}
	if len(uhits) == 0 {
		t.Fatalf("UnifiedSearch (enabled): got 0 hits, want >=1")
	}

	// Flip the toggle off through the same full-settings round trip the
	// frontend uses (get → mutate one field → set).
	s, err := api.Settings().Get(ctx)
	if err != nil {
		t.Fatalf("Settings().Get: %v", err)
	}
	s.SearchDisabled = true
	if err := api.Settings().Set(ctx, s); err != nil {
		t.Fatalf("Settings().Set(disabled): %v", err)
	}

	// SAME sa (SearchAPI) instance — proves the closure re-reads the
	// store per call rather than the dial being baked in at Search()'s
	// first (memoized) construction.
	hits, err = sa.Search(ctx, token, filters)
	if err != nil {
		t.Fatalf("Search (disabled): %v", err)
	}
	if len(hits) != 0 {
		t.Fatalf("Search (disabled): got %d hits, want 0 — the toggle did not short-circuit "+
			"the query, which is exactly the false promise this WP exists to fix", len(hits))
	}
	uhits, err = sa.UnifiedSearch(ctx, token, filters)
	if err != nil {
		t.Fatalf("UnifiedSearch (disabled): %v", err)
	}
	if len(uhits) != 0 {
		t.Fatalf("UnifiedSearch (disabled): got %d hits, want 0", len(uhits))
	}

	// Flip back on — must take effect with no restart.
	s, err = api.Settings().Get(ctx)
	if err != nil {
		t.Fatalf("Settings().Get (2): %v", err)
	}
	s.SearchDisabled = false
	if err := api.Settings().Set(ctx, s); err != nil {
		t.Fatalf("Settings().Set(re-enabled): %v", err)
	}
	hits, err = sa.Search(ctx, token, filters)
	if err != nil {
		t.Fatalf("Search (re-enabled): %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("Search (re-enabled): got %d hits, want 1 — flipping the dial back on "+
			"mid-process did not take effect, meaning the read is not live", len(hits))
	}
}

// TestSearchWiring_SettingsReadFailureFailsOpen pins the fail-open
// contract from spec §5: a nil settingsAPI (the pre-boot / test-chassis
// posture, matching workflowCedarModeFn's `settingsImpl == nil` branch)
// must not silently disable search.
//
// Mutation: change the nil-settingsAPI branch in api.go's Search() to
//
//	return false
//
// → this test's assertion (hits present) fails.
func TestSearchWiring_SettingsReadFailureFailsOpen(t *testing.T) {
	ctx := context.Background()
	// rpc.New(nil) is the documented nil-Core test posture (see
	// coreDataDir's doc comment) — a.core is nil so Search() falls
	// through to the stubSearch{} nil-safe fallback, which always
	// returns empty results. That path doesn't exercise cfg.Enabled at
	// all, so it isn't useful for THIS assertion. Instead, boot a real
	// Core (so the SQL-backed searchAPI gets constructed and cfg.Enabled
	// gets wired) but never populate a.settingsAPI's dial to a
	// disabling state — Settings.Get on a freshly booted store returns
	// the zero-value Settings{}, whose SearchEnabled() is true by
	// construction (SearchDisabled defaults false). That already proves
	// the "no data yet" fail-open path end to end without needing to
	// fake out settingsAPI itself.
	api := searchWiringAPI(t)
	const token = "faylopentoken"
	seedSearchableMessage(t, api, "s-wire-2", token)

	sa := api.Search()
	hits, err := sa.Search(ctx, token, searchview.SearchFilters{})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("Search on a freshly booted (unconfigured) settings store: got %d hits, want 1 "+
			"— a settings read that hasn't been explicitly disabled must fail open, not closed", len(hits))
	}
}

// TestSearchWiring_AuditTrailEmitsQueryHashNotRawQuery is FR-002's
// headline acceptance test: every Search call emits exactly one
// search.executed audit row via the real audit.API ring (not a fake
// capture emitter — this WP's defect was that NO process-wide emitter
// existed to hand to cfg.Audit at all), carrying query_hash and never
// the raw query string.
//
// Mutation: drop the `cfg.Audit = &searchAuditEmitter{...}` assignment
//
//	in api.go's Search() → entries stays empty → this test fails.
//
// Mutation: add `"query": query` to the attrs map at
//
//	views/search/impl.go:306 → the raw-query-absent assertion fails.
func TestSearchWiring_AuditTrailEmitsQueryHashNotRawQuery(t *testing.T) {
	ctx := context.Background()
	api := searchWiringAPI(t)
	const token = "auditrailtoken9000"
	seedSearchableMessage(t, api, "s-wire-3", token)

	sa := api.Search()
	if _, err := sa.Search(ctx, token, searchview.SearchFilters{}); err != nil {
		t.Fatalf("Search: %v", err)
	}

	entries, err := api.Audit().ListEntries(ctx, auditview.Filter{
		Categories: []string{"SEARCH"},
		Limit:      50,
	})
	if err != nil {
		t.Fatalf("Audit().ListEntries: %v", err)
	}
	var found *auditview.Entry
	for i := range entries {
		if entries[i].Subject == searchview.KindSearchExecuted {
			found = &entries[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("no %q audit entry found among %d SEARCH-category entries: %#v",
			searchview.KindSearchExecuted, len(entries), entries)
	}
	if !strings.Contains(found.Trailing, "query_hash=") {
		t.Errorf("audit entry Trailing = %q, want it to contain query_hash=", found.Trailing)
	}
	if strings.Contains(found.Trailing, token) {
		t.Errorf("audit entry Trailing = %q, leaks the raw query token %q — privacy contract violated",
			found.Trailing, token)
	}
}
