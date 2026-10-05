package settings

import (
	"context"
	"encoding/json"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/fleet"
	"github.com/kameas-ai/kenaz-harness/core/logging"
)

// ── fleet:session-changed (fleet-session-truth-01DOGF0A FR-2) ───────────────
//
// Push, not polls. Every transition — sign-in, sign-out, enroll ok / fail,
// server-side session death, capability change, claim change, lane health —
// republishes the full FleetSession snapshot. Publication is deduplicated on
// a transition key (the snapshot minus its timestamps), so each transition
// produces exactly one event and a quiet cadence tick produces none.

// publishFleetSession computes the snapshot and emits it on
// fleet.TopicFleetSessionChanged when it differs from the last one
// published. cause is logged only.
func (a *API) publishFleetSession(cause string) {
	if a == nil || a.fleet == nil {
		return
	}
	a.fleet.mu.RLock()
	broker := a.fleet.lockdownBroker
	a.fleet.mu.RUnlock()

	a.fleet.emitMu.Lock()
	defer a.fleet.emitMu.Unlock()
	v := a.fleetSessionSnapshot()
	key := sessionTransitionKey(v)
	if key == a.fleet.lastEmitKey {
		return
	}
	a.fleet.lastEmitKey = key
	if broker == nil {
		return
	}
	logging.L().Debug("fleet.session.changed", "cause", cause, "state", v.State, "reason", v.Reason)
	broker.Emit(fleet.TopicFleetSessionChanged, v)
}

// sessionTransitionKey is the snapshot with every timestamp blanked: an
// attempt time moving, or a lane's last-success time advancing, is not by
// itself a transition worth an event.
func sessionTransitionKey(v FleetSessionView) string {
	v.UpdatedAt = ""
	v.LastAttemptAt = ""
	v.NextRetryAt = ""
	v.Message = "" // per-attempt error text; the reason code is the transition
	v.Capabilities.FetchedAt = ""
	for _, lane := range []*FleetSyncLaneView{&v.Sync.ContextSync, &v.Sync.UnitPoll, &v.Sync.Telemetry} {
		lane.LastSuccessAt = ""
		lane.NextRetryAt = ""
		// Per-attempt detail, not a transition (review F8): the count and
		// the error text move on every failed attempt.
		lane.ConsecutiveFailures = 0
		lane.LastError = ""
		if len(lane.Sessions) > 0 {
			ss := append([]FleetSyncSessionView(nil), lane.Sessions...)
			for i := range ss {
				ss[i].NextRetryAt = ""
				ss[i].Dropped = 0
				ss[i].ConsecutiveFailures = 0
				ss[i].LastError = ""
			}
			lane.Sessions = ss
		}
	}
	b, _ := json.Marshal(v)
	return string(b)
}

// sessionExpiredTap consumes the client's fleet:session:expired IN-PROCESS
// and folds it into the session track: the server has definitely rejected
// the refresh (core/fleet refreshFailed — transport failures no longer fire
// it, review F2), so the snapshot says signed_out/session_expired even
// though tokens may still be stored. Every other topic is forwarded.
//
// fleet:session:expired itself is no longer forwarded to the frontend
// (review F8): SessionExpiredBanner was its second reader and now reads the
// same snapshot as every other surface.
//
// The expiry is NOT sticky: the cadence re-probes after a backoff
// (sessionEnrollDue) and any request fleet accepts clears it (onAuthOK).
type sessionExpiredTap struct {
	api   *API
	inner fleet.BrokerSink
}

func (t sessionExpiredTap) Emit(topic string, payload any) {
	if topic != fleet.TopicFleetSessionExpired {
		if t.inner != nil {
			t.inner.Emit(topic, payload)
		}
		return
	}
	if t.api == nil || t.api.fleet == nil {
		return
	}
	reason := ""
	if p, ok := payload.(fleet.SessionExpiredPayload); ok {
		reason = p.Reason
	}
	t.api.fleet.mu.Lock()
	tr := &t.api.fleet.sess
	tr.expired = true
	tr.lastErr = reason
	if tr.failures < 1 {
		tr.failures = 1
	}
	tr.nextRetryAt = time.Now().Add(enrollBackoff(tr.failures))
	t.api.fleet.mu.Unlock()
	t.api.publishFleetSession("session_expired")
}

// onFleetAuthOK runs when fleet accepts the token (fleet.Client
// SetAuthOKHook): a recorded expiry is evidently wrong, clear it.
func (a *API) onFleetAuthOK() {
	if a == nil || a.fleet == nil {
		return
	}
	a.fleet.mu.RLock()
	expired := a.fleet.sess.expired
	a.fleet.mu.RUnlock()
	if !expired {
		return
	}
	a.fleet.mu.Lock()
	a.fleet.sess.expired = false
	a.fleet.mu.Unlock()
	a.publishFleetSession("auth_ok")
}

// ── backend-owned refresh cadence (FR-2 / FR-4) ─────────────────────────────

// sessionRefreshInterval is how often a healthy session re-enrolls to notice
// server-side drift (tier / role / org changes). It replaces UserMenu.vue's
// IDENTITY_POLL_MS, which ran once per mounted component; this runs once
// per process. Same five minutes the capability and config pollers use.
const sessionRefreshInterval = 5 * time.Minute

// sessionSupervisorTick is how often the supervisor checks whether a
// refresh is due. Cheap: a token-store read and a dedup'd publish.
const sessionSupervisorTick = 30 * time.Second

func (a *API) runSessionSupervisor(ctx context.Context) {
	// First pass soon after boot so a stale identity cache is refreshed
	// without waiting a full interval.
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			a.sessionSupervisorStep(ctx, time.Now())
			timer.Reset(sessionSupervisorTick)
		}
	}
}

// sessionEnrollDue decides whether the cadence should re-enroll now.
// Not-provisioned stops automatic retries (FR-4: kept from the production
// fix — 60+ enroll/fail pairs); transient failures back off exponentially.
func sessionEnrollDue(tr sessionTrack, ts fleet.TokenState, now time.Time) bool {
	if !ts.Usable() || tr.signingIn || tr.autoRetryStopped {
		return false
	}
	if tr.expired {
		// Re-probe a recorded expiry after its backoff: a definite rejection
		// re-records it, anything else clears it (review F2 — it used to be
		// sticky until restart).
		return !now.Before(tr.nextRetryAt)
	}
	if tr.lastAttemptAt.IsZero() {
		return true
	}
	if tr.failures > 0 {
		return !now.Before(tr.nextRetryAt)
	}
	return now.Sub(tr.lastAttemptAt) >= sessionRefreshInterval
}

// sessionSupervisorStep is one cadence tick, split out so tests can drive
// it without a goroutine.
func (a *API) sessionSupervisorStep(ctx context.Context, now time.Time) {
	if a == nil || a.fleet == nil || fleet.Disabled() {
		return
	}
	a.fleet.mu.RLock()
	tr := a.fleet.sess
	client := a.fleet.client
	a.fleet.mu.RUnlock()
	if client == nil || client.IsNop() {
		return
	}
	// Served mode: the host broker owns the session and core/serve's enroll
	// supervisor owns enroll cadence. Publish only.
	if !fleet.ExternalTokenSourceActive() && sessionEnrollDue(tr, fleet.ReadTokenState(), now) {
		cctx, cancel := context.WithTimeout(ctx, 60*time.Second)
		_, _ = a.fleetEnroll(cctx) // outcome is recorded + published inside
		cancel()
	}
	// Token refresh / expiry / claim changes that happened inside other
	// fleet traffic surface here at the latest.
	a.publishFleetSession("tick")
}

// OnFleetSessionReset registers fn to run after a sign-in succeeds and after
// sign-out (fleet-session-truth-01DOGF0A WP06: the context-sync append
// breaker's ResetAll). fn must be quick and must not call back into the
// settings API's fleet lock.
func (a *API) OnFleetSessionReset(fn func()) {
	if a == nil || fn == nil {
		return
	}
	if a.fleet == nil {
		a.fleet = newFleetState()
	}
	a.fleet.mu.Lock()
	a.fleet.sessionResetHooks = append(a.fleet.sessionResetHooks, fn)
	a.fleet.mu.Unlock()
}

func (a *API) runSessionResetHooks() {
	if a == nil || a.fleet == nil {
		return
	}
	a.fleet.mu.RLock()
	hooks := append([]func(){}, a.fleet.sessionResetHooks...)
	a.fleet.mu.RUnlock()
	for _, fn := range hooks {
		fn()
	}
}

// SetFleetClientVersion records the build version enroll sends to Fleet
// (fleet-session-truth-01DOGF0A FR-9). Called from rpc.New with the
// ldflags-stamped version; local builds report "dev".
func (a *API) SetFleetClientVersion(v string) {
	if a == nil {
		return
	}
	if a.fleet == nil {
		a.fleet = newFleetState()
	}
	a.fleet.mu.Lock()
	a.fleet.clientVersion = v
	a.fleet.mu.Unlock()
}

func (a *API) fleetClientVersion() string {
	if a == nil || a.fleet == nil {
		return "dev"
	}
	a.fleet.mu.RLock()
	defer a.fleet.mu.RUnlock()
	if a.fleet.clientVersion == "" {
		return "dev"
	}
	return a.fleet.clientVersion
}
