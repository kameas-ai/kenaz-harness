package rpc

import (
	"context"
	"sync"

	cedarlib "github.com/cedar-policy/cedar-go"
	"github.com/kameas-ai/kenaz-harness/core/policy/cedar"
	"github.com/kameas-ai/kenaz-harness/core/session"
	"github.com/kameas-ai/kenaz-harness/core/toolloop"
)

// cedarSessionKindResolver implements toolloop.PermissionResolver by
// asking the shared Cedar engine's ActionUseTool evaluation for a
// harness-self tool under the calling session's Kind
// ("onboarding" | "chat" | "").
//
// Mission harness-self-attach-01PMHS01, the UNIT-2 -> UNIT-3 -> UNIT-4
// containment chain. UNIT-3 built and unit-tested this type with NO
// production caller. UNIT-4 gives it one: core/rpc/api.go's
// newLLMStack now constructs it unconditionally and merges it via
// toolloop.NewMergedResolver(staticPerms, sessionArm) — replacing what
// used to be a bare, sometimes-nil static resolver. See that commit for
// the go-live and harness_session_kind_resolver_wiring_test.go for the
// production-wiring-level tests (as opposed to this file's own
// resolver-level tests in _test.go, added by UNIT-3).
//
// Owner ruling B-3 (2026-08-19 escalation register): model-created
// schedules permit only within a tool allowlist, which removes the
// human review moment for unattended execution. Now that UNIT-4 has
// merged this into the session arm, this resolver — not a UI
// confirmation — is the only remaining boundary. Three correctness
// properties matter more than usual as a result:
//
//  1. A no-match verdict (nothing in the harness-self bundle applies —
//     e.g. a non-harness-self tool) MUST report PolicyAutoAllow with an
//     EMPTY Reason. sessionResolverShim (core/toolloop/perms.go:314-321)
//     treats "auto_allow + non-empty reason" as a MATCH, which would
//     make this arm win over the static resolver's own rules for every
//     tool, not just harness-self ones — see spec.md §6.1.3 item 1 of
//     the mission for the failure this would cause.
//  2. An unresolvable session kind (no session id, unknown session id,
//     or a session row whose Kind column is itself empty) must not
//     widen access. FR-006 states this as "`\"\"` must never widen
//     access" — the resolver achieves it by NOT substituting a default
//     kind string (in particular, NOT literally "chat") for these
//     cases: it passes the empty string straight through to Cedar,
//     which is what the shipped harness_write_forbid.cedar's
//     `session_kind != "onboarding"` clause already denies on its own,
//     without the resolver's Go code needing to know that fact. See
//     the commit body for why this diverges from a literal reading of
//     tasks.md's "empty/not-found ⇒ chat" paraphrase — spec.md's own
//     AC-003 mutation ("change the forbid policy to `session_kind ==
//     \"chat\"` ... so `\"\"` no longer matches") only makes sense, and
//     only stays falsifiable, if the fed value is the empty string, not
//     a "chat" substitution that would coincidentally still satisfy the
//     mutated policy.
//  3. A nil engine (no Cedar to consult at all — an empty DataDir, or a
//     boot-time construction failure) must deny, not auto_allow. See
//     Resolve's nil-engine branch for the full reasoning; this is the
//     fail-safe AC-015 pins.
type cedarSessionKindResolver struct {
	sessions *session.Manager // may be nil; Resolve tolerates it (kindFor falls back to "")
	engine   *cedar.Engine    // may be nil (empty DataDir, or boot failure); Resolve then denies everything — see below

	mu    sync.RWMutex
	cache map[string]string // sessionID -> last-known Kind; invalidated via session.Manager.AddKindTransitionObserver
}

// newCedarSessionKindResolver builds the resolver and — when sessions
// is non-nil — registers it as a KindTransitionObserver so a SetKind
// call from ANY caller (in production, the onboarding FSM's two call
// sites, core/onboarding/fsm.go:386 and :472) evicts the stale cache
// entry synchronously. This is not a TTL: there is no polling and no
// expiry other than an explicit transition, per C-010 ("the kind
// transitions back to \"chat\" mid-life" and any design that resolves
// kind once rather than per call leaves write access live after
// onboarding completes).
func newCedarSessionKindResolver(sessions *session.Manager, engine *cedar.Engine) *cedarSessionKindResolver {
	r := &cedarSessionKindResolver{
		sessions: sessions,
		engine:   engine,
		cache:    make(map[string]string),
	}
	if sessions != nil {
		sessions.AddKindTransitionObserver(r.invalidate)
	}
	return r
}

// invalidate drops the cached kind for sessionID. Registered as a
// session.KindTransitionObserver; also safe to call directly (tests do,
// to prove the observer path and a direct call produce the same
// outcome).
func (r *cedarSessionKindResolver) invalidate(sessionID, _ string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	delete(r.cache, sessionID)
	r.mu.Unlock()
}

// kindFor resolves sessionID -> Kind without a SQL read per call: a hit
// short-circuits from the in-memory cache, a miss reads through
// session.Manager once and populates it.
//
// sessionID == "" (C-011: the session-less discovery path,
// wfToolDiscovererAdapter.Discover) and "no session store wired" both
// return "" directly — never cached, since there is nothing to key a
// cache entry on and the read is already free. A record-not-found error
// or a record whose Kind is itself "" also yields "" — see the type
// doc's point 2 for why this is NOT normalised to "chat".
func (r *cedarSessionKindResolver) kindFor(ctx context.Context, sessionID string) string {
	if sessionID == "" || r == nil || r.sessions == nil {
		return ""
	}

	r.mu.RLock()
	kind, ok := r.cache[sessionID]
	r.mu.RUnlock()
	if ok {
		return kind
	}

	kind = ""
	if rec, err := r.sessions.Get(ctx, sessionID); err == nil {
		kind = rec.Kind
	}

	r.mu.Lock()
	r.cache[sessionID] = kind
	r.mu.Unlock()
	return kind
}

// Resolve implements toolloop.PermissionResolver.
func (r *cedarSessionKindResolver) Resolve(ctx context.Context, sessionID, server, tool string) (toolloop.Resolution, error) {
	res := toolloop.Resolution{Server: server, Tool: tool, Policy: toolloop.PolicyAutoAllow}
	if r == nil || r.engine == nil {
		// harness-self-attach-01PMHS01 UNIT-4, B-3 rule 3: "if the
		// session arm itself cannot be constructed... fail the boot
		// loudly or install a deny-all session arm. Never fall back to
		// the static resolver alone, and never to nil."
		//
		// A nil Cedar engine (api.go's buildCedarEngineOrNil returns nil
		// for an empty DataDir or a construction failure) means this
		// arm has NO way to evaluate ANY tool's permission — for ANY
		// session, not only ones touching harness-self. Reporting
		// auto_allow here (the pre-UNIT-4 posture) would let the static
		// arm's own auto_allow default govern everything, reproducing
		// the exact "could not determine the allowlist" hole this
		// mission exists to close, one level down. AC-015 pins this
		// directly: an empty-DataDir boot must still deny a chat
		// session's harness_write_add_provider.
		//
		// This is the one deliberate, documented widening of this
		// resolver's blast radius past harness-self: with no Cedar
		// engine, EVERY tool call in EVERY session — interactive chat
		// included — is denied by this arm until the static resolver's
		// own rules would have allowed it anyway (a nil static defaults
		// to auto_allow, so in the common "no DataDir at all" case this
		// denies literally everything). See the commit body for why
		// this trade was accepted rather than scoping the deny to
		// harness-self tools only: a scoped deny would leave "no engine
		// = unrestricted" open for every OTHER tool this arm was meant
		// to eventually police, which is precisely the shape of hole
		// B-3 says must not exist.
		res.Policy = toolloop.PolicyDeny
		res.Reason = "containment unavailable: no Cedar engine to evaluate session-kind policy"
		return res, nil
	}

	kind := r.kindFor(ctx, sessionID)
	toolUID := cedar.PermissionToolUID(server + "__" + tool)
	decision := r.engine.Evaluate(
		ctx,
		cedar.UserUID(),
		cedar.ActionUseTool,
		toolUID,
		map[cedarlib.String]cedarlib.Value{
			cedarlib.String(cedar.CtxKeySessionKind): cedarlib.String(kind),
		},
	)

	if decision.Outcome == cedar.Deny {
		res.Policy = toolloop.PolicyDeny
		res.Reason = decision.Reason
		return res, nil
	}
	// Allow and NotApplicable both report PolicyAutoAllow with an EMPTY
	// Reason — see the type doc's point 1. Do NOT carry decision.Reason
	// through here even though it is non-empty for a real Allow: doing
	// so would make sessionResolverShim treat this as a match and this
	// arm would then override the static resolver for every tool.
	return res, nil
}
