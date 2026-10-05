package settings

// fleet_session_sync_test.go — fleet-session-truth-01DOGF0A WP06 (settings
// half) + the owner's B3a root-cause addendum.
//
//   P-8b  token without the org claim → session degraded/needs_reauth,
//         telemetry lane degraded with the named reason, pipeline inactive,
//         and the reconcile log is ONE INFO at the transition — no WARN,
//         no per-tick repeat.
//   P-8c  token with the claim → pipeline active, lane ok, nothing logged
//         about the claim.
//   P-8d  (cited, not re-implemented) kameas.org.id comes from the token's
//         resource-owner claim, never enroll's org_id:
//         TestReconcile_EnrolledAndConsented_ExportsAsTheTokenIdentity in
//         fleet_telemetry_reconcile_test.go asserts exactly that on the
//         wire; ReconcileTelemetry builds IdentityAttrs from
//         TokenIdentityFromAccessToken (fleet.go), untouched here.

import (
	"context"
	"log/slog"
	"sync"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/fleet"
	"github.com/kameas-ai/kenaz-harness/core/logging"
)

type settingsLogCapture struct {
	mu   sync.Mutex
	recs []slog.Record
}

func (c *settingsLogCapture) Enabled(context.Context, slog.Level) bool { return true }
func (c *settingsLogCapture) Handle(_ context.Context, r slog.Record) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.recs = append(c.recs, r)
	return nil
}
func (c *settingsLogCapture) WithAttrs([]slog.Attr) slog.Handler { return c }
func (c *settingsLogCapture) WithGroup(string) slog.Handler      { return c }
func (c *settingsLogCapture) count(level slog.Level, msg string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, r := range c.recs {
		if r.Level == level && r.Message == msg {
			n++
		}
	}
	return n
}

func captureSettingsLogs(t *testing.T) *settingsLogCapture {
	t.Helper()
	prev := logging.Handler()
	c := &settingsLogCapture{}
	logging.Replace(c)
	t.Cleanup(func() { logging.Replace(prev) })
	return c
}

func TestReconcile_NoOrgClaim_NeedsReauth_SurfacedOnce(t *testing.T) {
	r := newReconcileRig(t, fleet.ConsentNone)
	ctx := context.Background()
	r.setToken(jwtFor("sub-alice", "")) // minted before the resource-owner scope
	if _, err := r.api.FleetRefreshIdentity(ctx); err != nil {
		t.Fatalf("enroll: %v", err)
	}
	if err := r.consent.SetLevel(fleet.ConsentFull); err != nil {
		t.Fatalf("SetLevel: %v", err)
	}
	r.api.AdoptTelemetryOptIns(fleet.TierOptInUpdates(fleet.ConsentFull))

	logs := captureSettingsLogs(t)
	for i := 0; i < 5; i++ { // five reconcile ticks
		r.api.ReconcileTelemetry(ctx)
	}
	if r.pipeline.Active() {
		t.Fatal("pipeline active without a resource-owner claim; Fleet would 401 every batch")
	}
	const msg = "fleet.otlp.reconcile.no_resource_owner_claim"
	if w := logs.count(slog.LevelWarn, msg); w != 0 {
		t.Fatalf("WARN %s logged %d times; once surfaced it must not be a WARN", msg, w)
	}
	if i := logs.count(slog.LevelInfo, msg); i != 1 {
		t.Fatalf("INFO %s logged %d times over 5 ticks, want exactly once (the transition)", msg, i)
	}

	v := snap(t, r.api)
	if v.State != FleetSessionDegraded || v.Reason != FleetReasonNeedsReauth {
		t.Fatalf("session = %q/%q, want degraded/needs_reauth", v.State, v.Reason)
	}
	if v.Sync.Telemetry.Status != string(fleet.LaneDegraded) || v.Sync.Telemetry.Reason != telemetryReasonNoOrgClaim {
		t.Fatalf("telemetry lane = %+v, want degraded/%s", v.Sync.Telemetry, telemetryReasonNoOrgClaim)
	}
	if v.Claims.HasOrgClaim {
		t.Fatalf("claims.hasOrgClaim = true for a token without the claim")
	}

	// A fresh sign-in yields a token WITH the claim: the next reconcile
	// activates and the session recovers — no restart, no forced logout.
	r.setToken(jwtFor("sub-alice", "zitadel-org-111"))
	r.api.ReconcileTelemetry(ctx)
	if !r.pipeline.Active() {
		t.Fatal("pipeline did not activate once the token carries the org claim")
	}
	if v := snap(t, r.api); v.State != FleetSessionSignedIn || v.Sync.Telemetry.Status != string(fleet.LaneOK) {
		t.Fatalf("after re-auth session=%q telemetry=%+v, want signed_in + lane ok", v.State, v.Sync.Telemetry)
	}
}

func TestReconcile_WithOrgClaim_ActivatesQuietly(t *testing.T) {
	logs := captureSettingsLogs(t)
	r := newReconcileRig(t, fleet.ConsentFull)
	r.api.ReconcileTelemetry(context.Background())
	if !r.pipeline.Active() {
		t.Fatal("enrolled + full consent + org claim, but the pipeline is not active")
	}
	const msg = "fleet.otlp.reconcile.no_resource_owner_claim"
	if n := logs.count(slog.LevelWarn, msg) + logs.count(slog.LevelInfo, msg); n != 0 {
		t.Fatalf("%s logged %d times with the claim present", msg, n)
	}
	v := snap(t, r.api)
	if v.State != FleetSessionSignedIn || v.Sync.Telemetry.Status != string(fleet.LaneOK) || !v.Claims.HasOrgClaim {
		t.Fatalf("session=%q telemetry=%+v claims=%+v, want signed_in, lane ok, org claim", v.State, v.Sync.Telemetry, v.Claims)
	}
}

func TestReconcile_ConsentNone_TelemetryLaneOffNotDegraded(t *testing.T) {
	r := newReconcileRig(t, fleet.ConsentNone)
	r.api.ReconcileTelemetry(context.Background())
	v := snap(t, r.api)
	if v.Sync.Telemetry.Status != string(fleet.LaneOff) || v.Sync.Telemetry.Reason != "consent_none" {
		t.Fatalf("telemetry lane = %+v, want off/consent_none (a choice, not a failure)", v.Sync.Telemetry)
	}
}
