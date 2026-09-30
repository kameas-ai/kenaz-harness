package rpc

// advice_respond.go — laya-advisors-01LAYA001 WP07: the RPC surface
// backing an advice chip's accept/dismiss buttons.
//
// # Why a small in-memory registry, not a features param on the wire
//
// A chip's accept/dismiss action must key on the EXACT Features value
// that produced it (advice.Advisor.Dismiss and
// labels.RecordActionIfSupported both cache/update on
// (session, kind, featuresHash) — see core/advice/cache.go's cacheKey).
// Sending the full Features JSON back over the wire on every click would
// work too, but would make the frontend responsible for round-tripping a
// backend-internal cache-key component it has no reason to know the
// shape of. Instead, chat.AdviceDeps.OnShown (advice_hook.go) notifies
// this registry the instant a chip is published, and Advice_Respond
// looks the features back up by (sessionID, kindID) — the same
// session-scoped, per-kind "most recent" semantics
// labels.Store.UpdateAction already uses.
import (
	"context"
	"fmt"
	"sync"

	"github.com/kameas-ai/kenaz-harness/core/advice"
	advicelabels "github.com/kameas-ai/kenaz-harness/core/advice/labels"
)

// adviceShownRegistry is the production OnShown sink: a process-
// lifetime, per-session-per-kind "last shown features" map. Race-safe
// per CLAUDE.md's mutex + snapshot pattern (Get returns a copy-by-value
// Features interface, which is safe to hand out under the lock since the
// concrete structs kinds use are themselves immutable after Extract
// returns).
type adviceShownRegistry struct {
	mu    sync.Mutex
	state map[string]map[string]advice.Features // sessionID -> kindID -> features
}

func newAdviceShownRegistry() *adviceShownRegistry {
	return &adviceShownRegistry{state: map[string]map[string]advice.Features{}}
}

func (r *adviceShownRegistry) note(sessionID, kindID string, features advice.Features) {
	if r == nil || sessionID == "" || kindID == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.state[sessionID] == nil {
		r.state[sessionID] = map[string]advice.Features{}
	}
	r.state[sessionID][kindID] = features
}

func (r *adviceShownRegistry) get(sessionID, kindID string) (advice.Features, bool) {
	if r == nil {
		return nil, false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	byKind, ok := r.state[sessionID]
	if !ok {
		return nil, false
	}
	f, ok := byKind[kindID]
	return f, ok
}

// adviceShown is the process-wide registry newLLMStack wires as
// chat.AdviceDeps.OnShown and Advice_Respond reads. A package-level
// instance (not a field threaded through every layer between
// buildChatRunner and this file) is the pragmatic choice here: unlike
// the Advisor/AdviceDeps values themselves (which are per-API-instance,
// test-constructible), this registry is pure transient UI-click
// bookkeeping with no persistence, no test-fixture need for isolation
// beyond a single process, and every production HarnessAPI in a real
// process is a singleton (New() is called once per boot).
var adviceShown = newAdviceShownRegistry()

// Advice_Respond implements HarnessAPI.Advice_Respond. action is
// "accept" or "dismiss". Returns the newly created child session id for
// a branch_now accept (spec §2's consumer-action table: "branch_now →
// offer/execute an event-log branch"); empty string for every other
// case, including a successful dismiss.
//
// compact_now / escalate_model's "accept" per spec §2's table
// ("compact_now → offer/execute compaction via the existing compactor";
// "escalate_model → offer switching the session to a stronger profile")
// records the acceptance (labels.RecordActionIfSupported) but does not
// itself trigger the compactor or open the model-move flow — both are
// existing UI-driven flows (the compaction panel; the model picker) that
// the frontend already owns; this WP wires the RECORD side honestly and
// leaves invoking those existing flows as the frontend's job on a
// successful Advice_Respond("accept") for those two kinds, rather than
// reaching backend-side into UI-owned machinery. `justify(blocker:
// "compactor trigger and model-move flow are both frontend-initiated
// today with no backend entry point that does not ALSO require session
// UI state (which panel is open, which profile is selected)", owner:
// alec, date: 2026-09-29)`.
func (a *API) Advice_Respond(ctx context.Context, sessionID, kindID, action string) (string, error) {
	if a == nil || a.chatAdvisor == nil {
		return "", fmt.Errorf("advice: advisor unavailable")
	}
	if sessionID == "" || kindID == "" {
		return "", fmt.Errorf("advice: session id and kind id are required")
	}
	kind, ok := advice.Get(kindID)
	if !ok {
		return "", fmt.Errorf("advice: unknown kind %q", kindID)
	}
	features, ok := adviceShown.get(sessionID, kindID)
	if !ok {
		// The chip may have already been dismissed, or the process
		// restarted since it was shown (adviceShownRegistry is in-memory
		// only). Treat as a benign no-op rather than an error — the
		// frontend's chip is already gone from the user's perspective
		// either way, so surfacing an error here would just be noise.
		return "", nil
	}
	sess := advice.SessionContext{SessionID: sessionID}

	switch action {
	case "dismiss":
		a.chatAdvisor.Dismiss(sess, kind, features)
		return "", nil
	case "accept":
		advicelabels.RecordActionIfSupported(ctx, a.chatAdvisor, sess, kind, features, advicelabels.ActionAccepted)
		if kindID == "branch_now" && a.adviceDeps != nil && a.adviceDeps.AutoActBranchNow != nil {
			childID, err := a.adviceDeps.AutoActBranchNow(ctx, sessionID)
			if err != nil {
				return "", fmt.Errorf("advice: branch_now accept: %w", err)
			}
			return childID, nil
		}
		return "", nil
	default:
		return "", fmt.Errorf("advice: unknown action %q, want \"accept\" or \"dismiss\"", action)
	}
}
