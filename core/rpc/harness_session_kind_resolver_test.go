package rpc

import (
	"context"
	"sync"
	"testing"

	harnessmcp "github.com/kameas-ai/kenaz-harness/core/mcp/builtin/harness"
	"github.com/kameas-ai/kenaz-harness/core/policy/cedar"
	"github.com/kameas-ai/kenaz-harness/core/session"
	"github.com/kameas-ai/kenaz-harness/core/storage"
	storagesqlite "github.com/kameas-ai/kenaz-harness/core/storage/sqlite"
	"github.com/kameas-ai/kenaz-harness/core/toolloop"
)

// harness-self-attach-01PMHS01 UNIT-3 — the Cedar-backed session-kind
// PermissionResolver, built and unit-tested here with NO production
// caller (that is UNIT-4). Every test drives real sqlite via
// session.Manager.CreateWithKind, never session.NewMemoryStore() —
// spec §12.1's rule, restated in CLAUDE.md's blind-spot #2 — because
// the store's SQL round-trip is where a load-bearing fact about this
// resolver's design was actually found: see
// harnessKindResolverFixture's doc comment and
// TestCedarSessionKindResolver_UnresolvableSessionDeniesWrite's for the
// one place this diverges from tasks.md's literal wording.

// harnessKindResolverFixture builds a real cedar.Engine with the three
// EmbeddedCedar harness-self snippets installed (mirroring UNIT-2's
// production install path, via the same harnessmcp.CedarSnippets()
// UNIT-2 added) and a real sqlite-backed session.Manager.
func harnessKindResolverFixture(t *testing.T) (*session.Manager, *cedar.Engine) {
	t.Helper()

	engine, err := cedar.NewEngine(cedar.Options{IncludeEmbedded: false, LoadFromDisk: false})
	if err != nil {
		t.Fatalf("cedar.NewEngine: %v", err)
	}
	snippets, err := harnessmcp.CedarSnippets()
	if err != nil {
		t.Fatalf("harnessmcp.CedarSnippets: %v", err)
	}
	if err := engine.LoadHarnessSnippets(snippets); err != nil {
		t.Fatalf("LoadHarnessSnippets: %v", err)
	}

	cfg := storage.Config{
		DataDir:          t.TempDir(),
		EncryptionStatus: storage.EncryptionStatusDisabledWithDiskEncryption,
	}
	db, err := storagesqlite.Open(cfg)
	if err != nil {
		t.Fatalf("storagesqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close(context.Background()) })

	store := session.NewSQLStore(session.NewStorageDB(db))
	mgr := session.NewManager(store)
	return mgr, engine
}

func TestCedarSessionKindResolver_OnboardingAllowsWrite(t *testing.T) {
	t.Parallel()
	mgr, engine := harnessKindResolverFixture(t)
	r := newCedarSessionKindResolver(mgr, engine)
	ctx := context.Background()

	rec, err := mgr.CreateWithKind(ctx, "onboarding session", nil, session.SessionKindOnboarding)
	if err != nil {
		t.Fatalf("CreateWithKind: %v", err)
	}

	res, err := r.Resolve(ctx, rec.ID, "harness-self", "harness_write_add_provider")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if res.Policy != toolloop.PolicyAutoAllow {
		t.Fatalf("onboarding session write: policy = %q, want %q (reason=%q)", res.Policy, toolloop.PolicyAutoAllow, res.Reason)
	}
}

// TestCedarSessionKindResolver_ChatDeniesWrite pins the headline
// behaviour B-3 depends on: a plain chat session cannot call a
// harness-self write tool.
//
// Mutation: drop the Cedar call in Resolve and return PolicyAutoAllow
// unconditionally. Must fail — verified by hand below.
func TestCedarSessionKindResolver_ChatDeniesWrite(t *testing.T) {
	t.Parallel()
	mgr, engine := harnessKindResolverFixture(t)
	r := newCedarSessionKindResolver(mgr, engine)
	ctx := context.Background()

	rec, err := mgr.Create(ctx, "chat session")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	res, err := r.Resolve(ctx, rec.ID, "harness-self", "harness_write_add_provider")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if res.Policy != toolloop.PolicyDeny {
		t.Fatalf("chat session write: policy = %q, want %q", res.Policy, toolloop.PolicyDeny)
	}
	if res.Reason == "" {
		t.Fatal("chat session write deny: Reason is empty, want the Cedar denial reason")
	}
}

// TestCedarSessionKindResolver_ChatReadNotDenied is AC-004: without it,
// the chat-denies-write test above is satisfiable by denying every
// harness-self tool regardless of kind, which is not what
// harness_read_default.cedar specifies.
//
// Mutation: broaden harness_write_forbid.cedar to match harness_* (not
// just harness_write_*). Must fail.
func TestCedarSessionKindResolver_ChatReadNotDenied(t *testing.T) {
	t.Parallel()
	mgr, engine := harnessKindResolverFixture(t)
	r := newCedarSessionKindResolver(mgr, engine)
	ctx := context.Background()

	rec, err := mgr.Create(ctx, "chat session")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	res, err := r.Resolve(ctx, rec.ID, "harness-self", "harness_read_get_status")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if res.Policy == toolloop.PolicyDeny {
		t.Fatalf("chat session read: policy = %q (reason=%q), want not-deny", res.Policy, res.Reason)
	}
}

// TestCedarSessionKindResolver_UnresolvableSessionDeniesWrite is AC-003
// (FR-002/FR-006): a session-less caller and an unknown session id must
// both be treated as not-onboarding and deny write tools.
//
// This test covers TWO of tasks.md's three named sub-cases
// (sessionID == "" and an unknown session id), not three. The third —
// "a session with kind ''" — is UNREACHABLE through session.Manager's
// real sqlite store, and this is worth stating loudly rather than
// silently dropping: core/session/store.go's sqlStore.Create defaults
// an empty Kind to SessionKindChat at INSERT time (store.go ~line 788),
// and independently scanRecord (the shared Get/List scan path,
// store.go ~line 1591) defaults an empty `kind` column to
// SessionKindChat again on every READ — including a row whose kind
// column was forced empty via a direct SetKind(ctx, id, "") (SetKind
// itself does not normalise; store.go's SetKind writes the literal
// argument). Manager.Get() therefore cannot return Record{Kind: ""}
// under any sequence of public calls: the resolver never sees that
// value from a found record. Both the "return chat" (Create) and
// "return chat" (scan) defaults are pre-existing, not introduced by
// this unit.
//
// Because of that store-level guarantee, this resolver's own fallback
// (kindFor) only ever has to supply "" for the two cases where there is
// no record to read at all — sessionID == "" and record-not-found —
// which is exactly what it does. See harness_session_kind_resolver.go's
// type doc, point 2, for why "" (not a "chat" substitution) is the
// correct fallback for those two cases specifically, and why
// substituting "chat" there would make the mutation below
// undetectable.
//
// Mutation: change harness_write_forbid.cedar's `when` clause from
// `context.session_kind != "onboarding"` to
// `context.session_kind == "chat"` (so the empty string this resolver
// feeds Cedar for these two cases no longer matches). Both must fail —
// verified by hand below. This is what proves the gate fails closed via
// Cedar's `!=` semantics rather than by the resolver's Go code
// happening to pick a value ("chat") the (correct) policy was always
// going to deny anyway.
func TestCedarSessionKindResolver_UnresolvableSessionDeniesWrite(t *testing.T) {
	t.Parallel()
	mgr, engine := harnessKindResolverFixture(t)
	r := newCedarSessionKindResolver(mgr, engine)
	ctx := context.Background()

	cases := map[string]string{
		"empty sessionID":    "",
		"unknown session id": "session-that-was-never-created",
	}
	for name, sessionID := range cases {
		res, err := r.Resolve(ctx, sessionID, "harness-self", "harness_write_add_provider")
		if err != nil {
			t.Fatalf("%s: Resolve: %v", name, err)
		}
		if res.Policy != toolloop.PolicyDeny {
			t.Fatalf("%s: policy = %q, want %q", name, res.Policy, toolloop.PolicyDeny)
		}
	}
}

// TestCedarSessionKindResolver_NonHarnessToolPassesThrough proves the
// no-match contract MergedResolver depends on: a tool outside the
// harness-self bundle must resolve to PolicyAutoAllow with an EMPTY
// Reason, or core/toolloop's sessionResolverShim
// (matched := res.Policy != PolicyAutoAllow || res.Reason != "") treats
// it as a match and this arm silently overrides the static resolver's
// own rules for every tool in the app — not just harness-self ones.
//
// Mutation: set a non-empty Reason on the Allow/NotApplicable branch of
// Resolve (e.g. carry decision.Reason through unconditionally). Must
// fail — verified by hand below.
func TestCedarSessionKindResolver_NonHarnessToolPassesThrough(t *testing.T) {
	t.Parallel()
	mgr, engine := harnessKindResolverFixture(t)
	r := newCedarSessionKindResolver(mgr, engine)
	ctx := context.Background()

	rec, err := mgr.Create(ctx, "chat session")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	res, err := r.Resolve(ctx, rec.ID, "filesystem", "list")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if res.Policy != toolloop.PolicyAutoAllow {
		t.Fatalf("non-harness tool: policy = %q, want %q", res.Policy, toolloop.PolicyAutoAllow)
	}
	if res.Reason != "" {
		t.Fatalf("non-harness tool: Reason = %q, want empty (a non-empty Reason reads as a MATCH to sessionResolverShim)", res.Reason)
	}
}

// TestCedarSessionKindResolver_KindTransitionInvalidatesCache is C-010:
// the onboarding FSM's SetKind(ctx, id, "chat") on terminal state must
// be visible to the NEXT Resolve call for that session with no process
// restart.
//
// Mutation: resolve kind once and memoise it without registering (or
// honouring) the KindTransitionObserver — i.e. cache-forever. Must
// fail — verified by hand below.
func TestCedarSessionKindResolver_KindTransitionInvalidatesCache(t *testing.T) {
	t.Parallel()
	mgr, engine := harnessKindResolverFixture(t)
	r := newCedarSessionKindResolver(mgr, engine)
	ctx := context.Background()

	rec, err := mgr.CreateWithKind(ctx, "onboarding session", nil, session.SessionKindOnboarding)
	if err != nil {
		t.Fatalf("CreateWithKind: %v", err)
	}

	before, err := r.Resolve(ctx, rec.ID, "harness-self", "harness_write_add_provider")
	if err != nil {
		t.Fatalf("Resolve (pre-transition): %v", err)
	}
	if before.Policy != toolloop.PolicyAutoAllow {
		t.Fatalf("pre-transition: policy = %q, want %q", before.Policy, toolloop.PolicyAutoAllow)
	}

	if err := mgr.SetKind(ctx, rec.ID, session.SessionKindChat); err != nil {
		t.Fatalf("SetKind: %v", err)
	}

	after, err := r.Resolve(ctx, rec.ID, "harness-self", "harness_write_add_provider")
	if err != nil {
		t.Fatalf("Resolve (post-transition): %v", err)
	}
	if after.Policy != toolloop.PolicyDeny {
		t.Fatalf("post-transition (no restart): policy = %q, want %q — cache was not invalidated by SetKind", after.Policy, toolloop.PolicyDeny)
	}
}

// TestCedarSessionKindResolver_ConcurrentResolve drives parallel
// Resolve calls across two session kinds through go test -race
// (core/toolloop/perms.go:43-45 requires PermissionResolver
// implementations be concurrency-safe; this resolver sits on the
// dispatch hot path per UNIT-4).
func TestCedarSessionKindResolver_ConcurrentResolve(t *testing.T) {
	mgr, engine := harnessKindResolverFixture(t)
	r := newCedarSessionKindResolver(mgr, engine)
	ctx := context.Background()

	onboarding, err := mgr.CreateWithKind(ctx, "ob", nil, session.SessionKindOnboarding)
	if err != nil {
		t.Fatalf("CreateWithKind: %v", err)
	}
	chat, err := mgr.Create(ctx, "chat")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(3)
		go func() {
			defer wg.Done()
			_, _ = r.Resolve(ctx, onboarding.ID, "harness-self", "harness_write_add_provider")
		}()
		go func() {
			defer wg.Done()
			_, _ = r.Resolve(ctx, chat.ID, "harness-self", "harness_write_add_provider")
		}()
		go func() {
			defer wg.Done()
			_, _ = r.Resolve(ctx, "", "harness-self", "harness_write_add_provider")
		}()
	}
	wg.Wait()
}
