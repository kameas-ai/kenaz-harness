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
// containment chain. This type is built and unit-tested here (UNIT-3)
// but has NO production caller until UNIT-4 replaces the bare static
// resolver at core/rpc/api.go:4065-4074 with
// toolloop.NewMergedResolver(staticPerms, cedarToolResolver) — see that
// unit's commit for the go-live. Constructing this type here changes no
// behaviour: nothing calls Resolve outside its own tests.
//
// Owner ruling B-3 (2026-08-19 escalation register): model-created
// schedules permit only within a tool allowlist, which removes the
// human review moment for unattended execution. Once UNIT-4 merges
// this into the session arm, this resolver — not a UI confirmation —
// is the only remaining boundary. Two correctness properties matter
// more than usual as a result:
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
type cedarSessionKindResolver struct {
	sessions *session.Manager // may be nil (nil-core test chassis); Resolve degrades to auto_allow
	engine   *cedar.Engine    // may be nil for the same reason

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
		// No engine to consult (nil-core test chassis, or a
		// misconfigured boot — UNIT-4 is responsible for making that
		// second case unreachable in production). auto_allow + empty
		// Reason is "no match": the static resolver (or its own
		// nil-static auto_allow default) still governs.
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
