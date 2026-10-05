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
	v.Capabilities.FetchedAt = ""
	for _, lane := range []*FleetSyncLaneView{&v.Sync.ContextSync, &v.Sync.UnitPoll, &v.Sync.Telemetry} {
		lane.LastSuccessAt = ""
		lane.NextRetryAt = ""
		if len(lane.Sessions) > 0 {
			ss := append([]FleetSyncSessionView(nil), lane.Sessions...)
			for i := range ss {
				ss[i].NextRetryAt = ""
			}
			lane.Sessions = ss
		}
	}
	b, _ := json.Marshal(v)
	return string(b)
}

// sessionExpiredTap forwards the client's fleet:session:expired event to
// the real broker AND folds it into the session track: the server has
// declared the session dead (refresh failed) even though tokens may still
// be stored, so the snapshot must say signed_out, not signed_in.
type sessionExpiredTap struct {
	api   *API
	inner fleet.BrokerSink
}

func (t sessionExpiredTap) Emit(topic string, payload any) {
	if t.inner != nil {
		t.inner.Emit(topic, payload)
	}
	if topic != fleet.TopicFleetSessionExpired || t.api == nil || t.api.fleet == nil {
		return
	}
	reason := ""
	if p, ok := payload.(fleet.SessionExpiredPayload); ok {
		reason = p.Reason
	}
	t.api.fleet.mu.Lock()
	t.api.fleet.sess.expired = true
	t.api.fleet.sess.lastErr = reason
	t.api.fleet.mu.Unlock()
	t.api.publishFleetSession("session_expired")
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
	if !ts.Usable() || tr.signingIn || tr.expired || tr.autoRetryStopped {
		return false
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
