package settings

import (
	"context"
	"errors"
	"net"
	"strings"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/fleet"
)

// ── FleetSession: the single fleet-session source of truth ─────────────────
//
// fleet-session-truth-01DOGF0A FR-1. During one ten-minute dogfood session
// (2026-10-04) seven surfaces gave four different answers about the same
// fleet session: the UserMenu popover said signed OUT (it collapsed any
// enroll error into "signed out"), Settings › Account's body said signed IN,
// its header said "Sign in to access…", the rail gates said whatever boot
// AppInfo said, ContextsView said team sync was off, and two log lines said
// things no surface showed at all.
//
// FleetSession is one snapshot every surface reads. It keeps the facts that
// used to be conflated apart:
//
//   - token state (local, network-free)           → signed_out vs. the rest
//   - the outcome of the most recent enroll       → signed_in vs. degraded
//   - the access token's claims                    → claims.hasOrgClaim
//   - the capability poller's set                  → capabilities
//   - background sync lanes                        → sync
//
// A failed enroll while the tokens are usable is DEGRADED, never signed out
// (FR-3). Only token absence, or expiry with no way to refresh, is signed out.

// FleetSession states.
const (
	FleetSessionSignedOut = "signed_out"
	FleetSessionSigningIn = "signing_in"
	FleetSessionSignedIn  = "signed_in"
	FleetSessionDegraded  = "degraded"
	FleetSessionDisabled  = "disabled"
)

// FleetSession reasons. Degraded reasons name why the last identity refresh
// failed; signed-out reasons name why the session ended.
const (
	FleetReasonNetwork         = "network"
	FleetReasonNotProvisioned  = "not_provisioned"
	FleetReasonServerError     = "server_error"
	FleetReasonNotConfigured   = "not_configured"
	FleetReasonSessionExpired  = "session_expired"
	FleetReasonSignInFailed    = "sign_in_failed"
	FleetReasonSignInCancelled = "sign_in_cancelled"
	// FleetReasonNeedsReauth: signed in and enrolled, but the access token
	// lacks the resource-owner (org) claim — it was minted before
	// DefaultOIDCScopes requested urn:zitadel:iam:user:resourceowner, and
	// refresh keeps the old scope set. Telemetry export is off until a fresh
	// sign-in. Never a forced sign-out: everything else keeps working.
	FleetReasonNeedsReauth = "needs_reauth"
)

// FleetSessionClaims reports which identity claims the access token carries.
// HasOrgClaim false while signed in is the B3a "no_resource_owner_claim"
// condition: enroll knows the org, the token does not, telemetry export is
// off. FR-7: a named fact, distinct from "not enrolled".
type FleetSessionClaims struct {
	HasSubject  bool `json:"hasSubject"`
	HasOrgClaim bool `json:"hasOrgClaim"`
}

// FleetSyncSessionView is one session's context-sync breaker state.
type FleetSyncSessionView struct {
	SessionID           string `json:"sessionId"`
	Reason              string `json:"reason,omitempty"`
	LastError           string `json:"lastError,omitempty"`
	ConsecutiveFailures int    `json:"consecutiveFailures"`
	Open                bool   `json:"open"`
	NextRetryAt         string `json:"nextRetryAt,omitempty"`
	Dropped             int    `json:"dropped"`
}

// FleetSyncLaneView is one background lane's status.
type FleetSyncLaneView struct {
	Status              string                 `json:"status"`
	Reason              string                 `json:"reason,omitempty"`
	LastError           string                 `json:"lastError,omitempty"`
	ConsecutiveFailures int                    `json:"consecutiveFailures"`
	LastSuccessAt       string                 `json:"lastSuccessAt,omitempty"`
	NextRetryAt         string                 `json:"nextRetryAt,omitempty"`
	Sessions            []FleetSyncSessionView `json:"sessions,omitempty"`
}

// FleetSyncView groups the lanes FR-6 makes visible.
type FleetSyncView struct {
	ContextSync FleetSyncLaneView `json:"contextSync"`
	UnitPoll    FleetSyncLaneView `json:"unitPoll"`
	Telemetry   FleetSyncLaneView `json:"telemetry"`
}

// FleetSessionView is the wire shape of Settings_FleetSession and the
// payload of the fleet:session-changed event.
type FleetSessionView struct {
	State string `json:"state"`
	// Reason is a machine code (FleetReason*); empty in the happy states.
	Reason string `json:"reason,omitempty"`
	// Message is the raw error text behind Reason, for the UI to humanize.
	Message string `json:"message,omitempty"`
	// AutoRetry is false once automatic identity refresh has stopped (the
	// not-provisioned case, FR-4). The UI then shows "Finish setup / Retry".
	AutoRetry     bool   `json:"autoRetry"`
	NextRetryAt   string `json:"nextRetryAt,omitempty"`
	LastAttemptAt string `json:"lastAttemptAt,omitempty"`
	// Identity is the enrolled identity: from this process's last successful
	// enroll ("enroll"), else the on-disk identity.json cache ("cache").
	// Absent when signed out — the cache is never shown after sign-out.
	Identity       *FleetIdentity `json:"identity,omitempty"`
	IdentitySource string         `json:"identitySource,omitempty"`
	// EmailSource / NameSource say where identity.email / displayName came
	// from when enroll omitted them: "enroll" or "token_claim" (FR-9).
	EmailSource  string             `json:"emailSource,omitempty"`
	NameSource   string             `json:"nameSource,omitempty"`
	Claims       FleetSessionClaims `json:"claims"`
	Capabilities CapabilitiesView   `json:"capabilities"`
	Profile      *FleetProfileInfo  `json:"profile,omitempty"`
	Sync         FleetSyncView      `json:"sync"`
	UpdatedAt    string             `json:"updatedAt"`
}

// sessionTrack is fleetState's record of the session's recent transitions.
// Guarded by fleetState.mu.
type sessionTrack struct {
	// identity is the identity from this process's last successful enroll.
	identity *fleet.Identity
	// lastAttemptAt is when enroll was last attempted (any outcome).
	lastAttemptAt time.Time
	// reason / lastErr describe the most recent enroll FAILURE; both empty
	// once an enroll succeeds.
	reason  string
	lastErr string
	// failures counts consecutive enroll failures (drives backoff).
	failures int
	// autoRetryStopped is set by a not-provisioned failure (FR-4): the
	// backend stops re-enrolling on its own until an explicit retry or a
	// new sign-in.
	autoRetryStopped bool
	// nextRetryAt is when the backend cadence may next attempt enroll.
	nextRetryAt time.Time
	// expired is set when the session died server-side (refresh failed,
	// enroll answered ErrTokenExpired) while tokens are still stored.
	expired bool
	// signingIn is true while a sign-in flow is in flight.
	signingIn bool
	// signInReason / signInErr describe the last failed sign-in attempt
	// while no usable session exists (shown on the signed-out state).
	signInReason string
	signInErr    string
}

// FleetSession returns the current fleet-session snapshot (FR-1). Cheap and
// network-free: it reads the token store, the identity cache, the
// capability poller's in-memory set and the lane board.
func (a *API) FleetSession(_ context.Context) (FleetSessionView, error) {
	return a.fleetSessionSnapshot(), nil
}

// FleetSyncLanes returns the shared lane board producers report into, or
// nil when fleet state is not wired.
func (a *API) FleetSyncLanes() *fleet.SyncLanes {
	if a == nil || a.fleet == nil {
		return nil
	}
	a.fleet.mu.RLock()
	defer a.fleet.mu.RUnlock()
	return a.fleet.lanes
}

func (a *API) fleetSessionSnapshot() FleetSessionView {
	now := time.Now().UTC()
	v := FleetSessionView{
		Capabilities: capabilitiesToView(fleet.DefaultDenyCapabilities()),
		Sync:         emptySyncView(),
		UpdatedAt:    now.Format(time.RFC3339Nano),
	}
	if fleet.Disabled() || a == nil || a.fleet == nil {
		v.State = FleetSessionDisabled
		return v
	}
	a.fleet.mu.RLock()
	client := a.fleet.client
	dataDir := a.fleet.dataDir
	poller := a.fleet.poller
	lanes := a.fleet.lanes
	tr := a.fleet.sess
	if tr.identity != nil {
		cp := *tr.identity
		tr.identity = &cp
	}
	a.fleet.mu.RUnlock()
	if client == nil || client.IsNop() {
		v.State = FleetSessionDisabled
		return v
	}

	p := fleet.ResolveProfile()
	v.Profile = &FleetProfileInfo{
		Name:         p.Name,
		BadgeColor:   p.BadgeColor(),
		FleetBaseURL: p.FleetBaseURL,
		Configured:   p.Configured(),
	}
	if !tr.lastAttemptAt.IsZero() {
		v.LastAttemptAt = tr.lastAttemptAt.UTC().Format(time.RFC3339)
	}

	ts := fleet.ReadTokenState()
	v.Claims = FleetSessionClaims{
		HasSubject:  ts.Claims.Subject != "",
		HasOrgClaim: ts.Claims.OrgID != "",
	}
	v.AutoRetry = !tr.autoRetryStopped

	switch {
	case tr.signingIn:
		v.State = FleetSessionSigningIn
	case !ts.Usable():
		v.State = FleetSessionSignedOut
		if ts.Present {
			v.Reason = FleetReasonSessionExpired
		} else if tr.signInReason != "" {
			v.Reason, v.Message = tr.signInReason, tr.signInErr
		}
	case tr.expired:
		v.State = FleetSessionSignedOut
		v.Reason = FleetReasonSessionExpired
		v.Message = tr.lastErr
	case tr.reason != "":
		v.State = FleetSessionDegraded
		v.Reason = tr.reason
		v.Message = tr.lastErr
		if !tr.nextRetryAt.IsZero() && !tr.autoRetryStopped {
			v.NextRetryAt = tr.nextRetryAt.UTC().Format(time.RFC3339)
		}
	case ts.Claims.Subject != "" && ts.Claims.OrgID == "":
		// A JWT with a subject but no org claim (an opaque token has neither
		// and is not this case). See FleetReasonNeedsReauth.
		v.State = FleetSessionDegraded
		v.Reason = FleetReasonNeedsReauth
		v.Message = "Your sign-in predates a permission fleet now needs (org claim). Sign in again to re-enable telemetry export."
	default:
		v.State = FleetSessionSignedIn
	}

	if v.State == FleetSessionSignedOut {
		// No identity and no capabilities after sign-out / expiry: the cache
		// is a fact about a session that no longer exists (spec §6).
		return v
	}
	if !ts.Usable() {
		// signing_in from signed-out: nothing to show yet.
		return v
	}

	// Identity: live enroll result, else the on-disk cache.
	var id *fleet.Identity
	switch {
	case tr.identity != nil:
		id, v.IdentitySource = tr.identity, "enroll"
	case dataDir != "":
		if cached, err := fleet.LoadIdentity(dataDir); err == nil {
			id, v.IdentitySource = &cached, "cache"
		}
	}
	if id != nil {
		view := fleetIdentityToView(*id)
		v.Identity = &view
	}

	if poller != nil {
		v.Capabilities = capabilitiesToView(poller.Current())
	}
	v.Sync = syncViewFromLanes(lanes)
	return v
}

func emptySyncView() FleetSyncView {
	unknown := FleetSyncLaneView{Status: string(fleet.LaneUnknown)}
	return FleetSyncView{ContextSync: unknown, UnitPoll: unknown, Telemetry: unknown}
}

func syncViewFromLanes(l *fleet.SyncLanes) FleetSyncView {
	if l == nil {
		return emptySyncView()
	}
	return FleetSyncView{
		ContextSync: laneToView(l.Snapshot(fleet.LaneContextSync)),
		UnitPoll:    laneToView(l.Snapshot(fleet.LaneUnitPoll)),
		Telemetry:   laneToView(l.Snapshot(fleet.LaneTelemetry)),
	}
}

func laneToView(s fleet.LaneSnapshot) FleetSyncLaneView {
	v := FleetSyncLaneView{
		Status:              string(s.Status),
		Reason:              s.Reason,
		LastError:           s.LastError,
		ConsecutiveFailures: s.ConsecutiveFailures,
		LastSuccessAt:       rfc3339OrEmpty(s.LastSuccessAt),
		NextRetryAt:         rfc3339OrEmpty(s.NextRetryAt),
	}
	for _, ss := range s.Sessions {
		v.Sessions = append(v.Sessions, FleetSyncSessionView{
			SessionID:           ss.SessionID,
			Reason:              ss.Reason,
			LastError:           ss.LastError,
			ConsecutiveFailures: ss.ConsecutiveFailures,
			Open:                ss.Open,
			NextRetryAt:         rfc3339OrEmpty(ss.NextRetryAt),
			Dropped:             ss.Dropped,
		})
	}
	return v
}

func rfc3339OrEmpty(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// classifyEnrollError maps an enroll failure to a FleetSession reason.
// expired=true means the session is dead (signed out), not degraded.
func classifyEnrollError(err error) (reason string, expired bool) {
	switch {
	case err == nil:
		return "", false
	case errors.Is(err, fleet.ErrTokenExpired), errors.Is(err, fleet.ErrNotSignedIn):
		return FleetReasonSessionExpired, true
	case errors.Is(err, fleet.ErrUserNotProvisioned):
		return FleetReasonNotProvisioned, false
	case errors.Is(err, fleet.ErrProfileNotConfigured):
		return FleetReasonNotConfigured, false
	case errors.Is(err, fleet.ErrFleetUnreachable),
		errors.Is(err, context.DeadlineExceeded):
		return FleetReasonNetwork, false
	}
	var ne net.Error
	if errors.As(err, &ne) {
		return FleetReasonNetwork, false
	}
	es := err.Error()
	if strings.Contains(es, "no such host") || strings.Contains(es, "connection refused") ||
		strings.Contains(es, "i/o timeout") || strings.Contains(es, "network is unreachable") {
		return FleetReasonNetwork, false
	}
	return FleetReasonServerError, false
}

// enrollBackoff is the bounded exponential backoff for transient enroll
// failures (FR-4): 1m, 2m, 4m, … capped at 30m.
func enrollBackoff(failures int) time.Duration {
	if failures <= 0 {
		return 0
	}
	d := time.Minute
	for i := 1; i < failures && d < 30*time.Minute; i++ {
		d *= 2
	}
	if d > 30*time.Minute {
		d = 30 * time.Minute
	}
	return d
}

// recordEnrollOutcome folds one enroll attempt into the session track.
func (a *API) recordEnrollOutcome(id *fleet.Identity, err error) {
	if a == nil || a.fleet == nil {
		return
	}
	now := time.Now()
	a.fleet.mu.Lock()
	tr := &a.fleet.sess
	tr.lastAttemptAt = now
	if err == nil {
		if id != nil {
			cp := *id
			tr.identity = &cp
		}
		tr.reason, tr.lastErr = "", ""
		tr.failures = 0
		tr.autoRetryStopped = false
		tr.expired = false
		tr.nextRetryAt = time.Time{}
		tr.signInReason, tr.signInErr = "", ""
		a.fleet.mu.Unlock()
		return
	}
	reason, expired := classifyEnrollError(err)
	tr.lastErr = err.Error()
	tr.failures++
	if expired {
		tr.expired = true
		tr.reason = ""
	} else {
		tr.reason = reason
	}
	if reason == FleetReasonNotProvisioned {
		// Keep the production fix (60+ enroll/fail pairs): stop automatic
		// retries. Recovery is explicit retry, a new sign-in, app focus, or
		// a session change from elsewhere (FR-4).
		tr.autoRetryStopped = true
		tr.nextRetryAt = time.Time{}
	} else {
		tr.nextRetryAt = now.Add(enrollBackoff(tr.failures))
	}
	a.fleet.mu.Unlock()
}

// resetSessionTrack forgets every session transition (sign-out).
func (a *API) resetSessionTrack() {
	if a == nil || a.fleet == nil {
		return
	}
	a.fleet.mu.Lock()
	a.fleet.sess = sessionTrack{}
	a.fleet.mu.Unlock()
}
