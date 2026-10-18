package fleet

// impl_telemetry_consent_test.go — SetTelemetryConsent wiring
// (fleet telemetry tier -> per-class opt-ins fix).
//
// Exercises the wire point at Impl.SetTelemetryConsent with a fake OptIns
// pusher (no real fleet HTTP server needed here — core/fleet's own
// telemetry_optins_pusher_test.go covers TelemetryOptInPusher against a
// real httptest fleet server end to end).

import (
	"context"
	"errors"
	"sync"
	"testing"

	corefleet "github.com/kameas-ai/kenaz-harness/core/fleet"
)

// fakeOptInPusher is a race-safe fake of optInPusher.
type fakeOptInPusher struct {
	mu    sync.Mutex
	calls []corefleet.ConsentLevel
	err   error
}

func (f *fakeOptInPusher) Push(_ context.Context, level corefleet.ConsentLevel) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, level)
	return f.err
}

func (f *fakeOptInPusher) snapshot() []corefleet.ConsentLevel {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]corefleet.ConsentLevel, len(f.calls))
	copy(out, f.calls)
	return out
}

func newEnterpriseConsent(t *testing.T) *corefleet.TelemetryConsent {
	t.Helper()
	tc, err := corefleet.NewTelemetryConsent(t.TempDir(), corefleet.TierReaderFunc(func() string { return "enterprise" }))
	if err != nil {
		t.Fatalf("NewTelemetryConsent: %v", err)
	}
	return tc
}

// TestImpl_SetTelemetryConsent_PushesImpliedOptIns is the wire-point proof:
// picking a tier calls OptIns.Push with that same tier so the implied
// per-class opt-in vector reaches the fleet store.
func TestImpl_SetTelemetryConsent_PushesImpliedOptIns(t *testing.T) {
	tc := newEnterpriseConsent(t)
	fake := &fakeOptInPusher{}
	impl := &Impl{Consent: tc, OptIns: fake}

	if err := impl.SetTelemetryConsent(context.Background(), "full"); err != nil {
		t.Fatalf("SetTelemetryConsent: %v", err)
	}
	if calls := fake.snapshot(); len(calls) != 1 || calls[0] != corefleet.ConsentFull {
		t.Fatalf("OptIns.Push calls = %v, want [full]", calls)
	}
	if got := tc.Level(); got != corefleet.ConsentFull {
		t.Errorf("local tier = %q, want full", got)
	}
}

// TestImpl_SetTelemetryConsent_PushFailureStillPersistsLocalTier is the
// offline-first half of the failure-semantics contract: a failed fleet push
// must not roll back (or block) the local tier save, and the failure must
// be surfaced as a returned error.
func TestImpl_SetTelemetryConsent_PushFailureStillPersistsLocalTier(t *testing.T) {
	tc := newEnterpriseConsent(t)
	fake := &fakeOptInPusher{err: errors.New("fleet unreachable")}
	impl := &Impl{Consent: tc, OptIns: fake}

	err := impl.SetTelemetryConsent(context.Background(), "full")
	if err == nil {
		t.Fatal("SetTelemetryConsent: want the push failure surfaced as an error")
	}
	if got := tc.Level(); got != corefleet.ConsentFull {
		t.Errorf("local tier = %q, want full (must persist even though the fleet push failed)", got)
	}
}

// TestImpl_SetTelemetryConsent_FleetDisabled_NotSurfacedAsError asserts
// ErrFleetDisabled (signed out / OSS build) is treated as "nothing to push
// right now", not a failure — the tier still saves and the RPC still
// succeeds.
func TestImpl_SetTelemetryConsent_FleetDisabled_NotSurfacedAsError(t *testing.T) {
	tc, err := corefleet.NewTelemetryConsent(t.TempDir(), corefleet.StaticTierReader{})
	if err != nil {
		t.Fatalf("NewTelemetryConsent: %v", err)
	}
	fake := &fakeOptInPusher{err: corefleet.ErrFleetDisabled}
	impl := &Impl{Consent: tc, OptIns: fake}

	if err := impl.SetTelemetryConsent(context.Background(), "none"); err != nil {
		t.Fatalf("SetTelemetryConsent: %v, want nil (ErrFleetDisabled is not a failure to surface)", err)
	}
	if got := tc.Level(); got != corefleet.ConsentNone {
		t.Errorf("local tier = %q, want none", got)
	}
}

// TestImpl_SetTelemetryConsent_TierGatingErrorSkipsPush asserts a
// capability-gating failure (free tier requesting full) short-circuits
// before any push attempt.
func TestImpl_SetTelemetryConsent_TierGatingErrorSkipsPush(t *testing.T) {
	tc, err := corefleet.NewTelemetryConsent(t.TempDir(), corefleet.StaticTierReader{}) // free tier
	if err != nil {
		t.Fatalf("NewTelemetryConsent: %v", err)
	}
	fake := &fakeOptInPusher{}
	impl := &Impl{Consent: tc, OptIns: fake}

	if err := impl.SetTelemetryConsent(context.Background(), "full"); err == nil {
		t.Fatal("SetTelemetryConsent: want ErrTierInsufficient")
	}
	if calls := fake.snapshot(); len(calls) != 0 {
		t.Errorf("OptIns.Push called %d times, want 0 (tier gate must short-circuit before any push)", len(calls))
	}
}

// TestImpl_SetTelemetryConsent_NilOptIns_LocalOnly asserts the pre-fix
// behaviour is preserved when OptIns is nil (test chassis / OSS build):
// the tier saves locally and no push is attempted.
func TestImpl_SetTelemetryConsent_NilOptIns_LocalOnly(t *testing.T) {
	tc := newEnterpriseConsent(t)
	impl := &Impl{Consent: tc} // OptIns left nil

	if err := impl.SetTelemetryConsent(context.Background(), "full"); err != nil {
		t.Fatalf("SetTelemetryConsent: %v", err)
	}
	if got := tc.Level(); got != corefleet.ConsentFull {
		t.Errorf("local tier = %q, want full", got)
	}
}

// TestImpl_SetTelemetryConsent_InvalidLevel_RejectedBeforeAnything asserts
// an unknown level string is rejected before touching Consent or OptIns.
func TestImpl_SetTelemetryConsent_InvalidLevel_RejectedBeforeAnything(t *testing.T) {
	tc := newEnterpriseConsent(t)
	fake := &fakeOptInPusher{}
	impl := &Impl{Consent: tc, OptIns: fake}

	if err := impl.SetTelemetryConsent(context.Background(), "bogus"); err == nil {
		t.Fatal("SetTelemetryConsent: want error for an unknown level")
	}
	if calls := fake.snapshot(); len(calls) != 0 {
		t.Errorf("OptIns.Push called %d times, want 0", len(calls))
	}
	if got := tc.Level(); got != corefleet.ConsentNone {
		t.Errorf("local tier = %q, want unchanged (none, the default)", got)
	}
}
