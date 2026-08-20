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
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core"
	auditview "github.com/kameas-ai/kenaz-harness/core/rpc/views/audit"
	searchview "github.com/kameas-ai/kenaz-harness/core/rpc/views/search"
	"github.com/kameas-ai/kenaz-harness/core/rpc/views/settings"
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
	// rpc.New starts background workers, among them the fleet ConfigPoller,
	// which calls keyring.Get() for the life of the test BINARY and races
	// keyring.MockInit()'s write to go-keyring's package global. Shutdown
	// is nil-safe and idempotent.
	t.Cleanup(api.Shutdown)
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

// erroringSettings wraps a real settings.SettingsAPI and fails only
// Get — the one call cfg.Enabled's closure makes. Embedding the
// interface keeps the other ~40 methods intact without a hand-written
// fake that would rot on every interface addition.
type erroringSettings struct {
	settings.SettingsAPI
}

func (erroringSettings) Get(context.Context) (settings.Settings, error) {
	return settings.Settings{}, errors.New("settings store unavailable")
}

// TestSearchWiring_SettingsReadFailureFailsOpen pins the fail-open
// contract from spec §5: an *unreadable* settings store must not
// silently disable search. Both fail-open branches of cfg.Enabled's
// closure are driven directly, because cfg.Enabled re-reads
// a.settingsAPI on every call — so swapping the field after Search()
// has memoized a.searchAPI still reaches the closure.
//
// This replaces an earlier version of this test that booted a default
// store and asserted hits were returned. That assertion was vacuous:
// a freshly booted store has SearchDisabled=false, so it exercised the
// ordinary enabled path, not either fail-open branch — and neither of
// the mutations named below killed it. Per tasks.md, "an assertion
// whose named mutation still passes is not evidence."
//
// Mutation: change the `a.settingsAPI == nil` branch in api.go's
//
//	Search() to `return false` → the nil sub-test fails.
//
// Mutation: change the `err != nil` branch to `return false`
//
//	→ the erroring sub-test fails.
func TestSearchWiring_SettingsReadFailureFailsOpen(t *testing.T) {
	ctx := context.Background()
	api := searchWiringAPI(t)
	const token = "faylopentoken"
	seedSearchableMessage(t, api, "s-wire-2", token)

	sa := api.Search()
	real := api.settingsAPI

	// Baseline: the wiring works at all before we break the store.
	if hits, err := sa.Search(ctx, token, searchview.SearchFilters{}); err != nil || len(hits) != 1 {
		t.Fatalf("baseline Search: got %d hits, err %v — want 1 hit, nil", len(hits), err)
	}

	t.Run("nil settings store fails open", func(t *testing.T) {
		api.settingsAPI = nil
		t.Cleanup(func() { api.settingsAPI = real })

		hits, err := sa.Search(ctx, token, searchview.SearchFilters{})
		if err != nil {
			t.Fatalf("Search: %v", err)
		}
		if len(hits) != 1 {
			t.Fatalf("nil settings store: got %d hits, want 1 — an unwired settings store must "+
				"fail OPEN, not silently disable search", len(hits))
		}
	})

	t.Run("erroring settings store fails open", func(t *testing.T) {
		api.settingsAPI = erroringSettings{real}
		t.Cleanup(func() { api.settingsAPI = real })

		hits, err := sa.Search(ctx, token, searchview.SearchFilters{})
		if err != nil {
			t.Fatalf("Search: %v", err)
		}
		if len(hits) != 1 {
			t.Fatalf("erroring settings store: got %d hits, want 1 — a settings-read FAILURE must "+
				"fail OPEN, not silently disable search (spec §5)", len(hits))
		}
	})
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

// TestSearchWiring_AuditNeverLeaksAdversarialQuery hardens FR-002's
// privacy contract against queries designed to defeat it, asserted on
// the PERSISTED audit row (Audit().ListEntries) rather than on the
// intermediate attrs map — the attrs map is the search view's own
// tested contract; what this WP added is the bridge that renders it
// into audit.Entry.Trailing, and a bridge is where a leak would be
// introduced.
//
// The three shapes that matter for a privacy control:
//   - credential-shaped: the query itself is the secret.
//   - unicode / multibyte: a naive byte-wise redactor mangles or misses.
//   - very long: a truncating renderer could spill a prefix.
//
// Also pins that query_hash is TRUNCATED (16 hex chars, not the full
// 64-char sha256) — the on-screen copy promises "a truncated
// query_hash", and an untruncated digest of a low-entropy query is
// itself reversible by dictionary attack.
//
// Mutation: add `"query": query` to the attrs map at
//
//	views/search/impl.go:306 → every sub-test fails.
//
// Mutation: return the full digest from queryHash (drop the [:16])
//
//	→ the truncation assertion fails.
func TestSearchWiring_AuditNeverLeaksAdversarialQuery(t *testing.T) {
	ctx := context.Background()
	api := searchWiringAPI(t)
	seedSearchableMessage(t, api, "s-wire-adv", "advtoken")
	sa := api.Search()

	cases := []struct {
		name  string
		query string
		frags []string
	}{
		{
			name:  "credential shaped",
			query: "AKIAIOSFODNN7EXAMPLE sk-proj-abc123secret ghp_deadbeefcafe",
			frags: []string{"AKIAIOSFODNN7EXAMPLE", "sk-proj-abc123secret", "ghp_deadbeefcafe"},
		},
		{
			name:  "unicode multibyte",
			query: "пароль 密码 🔐 école",
			frags: []string{"пароль", "密码", "🔐", "école"},
		},
		{
			name:  "very long",
			query: strings.Repeat("SUPERSECRETPAYLOAD", 4000),
			frags: []string{"SUPERSECRETPAYLOAD"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := sa.Search(ctx, tc.query, searchview.SearchFilters{ProjectID: "p", Limit: 3}); err != nil {
				t.Fatalf("Search: %v", err)
			}
			if _, err := sa.UnifiedSearch(ctx, tc.query, searchview.SearchFilters{Limit: 3}); err != nil {
				t.Fatalf("UnifiedSearch: %v", err)
			}

			entries, err := api.Audit().ListEntries(ctx, auditview.Filter{Limit: 500})
			if err != nil {
				t.Fatalf("Audit().ListEntries: %v", err)
			}
			rows := 0
			for _, e := range entries {
				if e.Subject != searchview.KindSearchExecuted {
					continue
				}
				rows++
				// Every persisted field, not just Trailing — a leak that
				// landed in ID or Subject would be just as bad.
				blob := e.ID + "|" + e.Timestamp + "|" + e.Category + "|" + e.Subject + "|" + e.Trailing
				if strings.Contains(blob, tc.query) {
					t.Errorf("persisted audit row carries the raw query: %q", blob)
				}
				for _, frag := range tc.frags {
					if strings.Contains(blob, frag) {
						t.Errorf("persisted audit row leaks query fragment %q: %q", frag, blob)
					}
				}
				if !strings.Contains(e.Trailing, "query_hash=") {
					t.Errorf("persisted audit row has no query_hash: %q", e.Trailing)
				}
				for _, kv := range strings.Fields(e.Trailing) {
					h, ok := strings.CutPrefix(kv, "query_hash=")
					if !ok {
						continue
					}
					if len(h) != 16 {
						t.Errorf("query_hash is %d chars (%q) — the copy promises a TRUNCATED hash; "+
							"a full sha256 of a low-entropy query is reversible by dictionary attack",
							len(h), h)
					}
				}
			}
			if rows == 0 {
				t.Fatalf("no %q rows persisted — the audit trail did not fire at all",
					searchview.KindSearchExecuted)
			}
		})
	}
}
