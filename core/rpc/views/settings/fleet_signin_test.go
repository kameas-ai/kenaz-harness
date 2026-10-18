package settings

// fleet_signin_test.go — fleet-session-truth-01DOGF0A WP07 (dogfood B3).
//
//   P-5  the flow window is ≥ 5 min; Settings_FleetSignInCancel ends the
//        in-flight flow with context.Canceled and logs no ERROR.
//   P-6  two concurrent sign-ins → ONE browser flow; both callers get its
//        result.
//
// The browser flow is injected (fleetState.signInFlow) — no test here opens
// a browser or touches a keychain (tokens come from the external source).

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/fleet"
)

func newSignInRig(t *testing.T, flow func(context.Context, fleet.EnvProfile) (fleet.TokenSet, error)) (*sessionRig, *fakeSessionBroker) {
	t.Helper()
	t.Setenv("KENAZ_HARNESS_ENV", "local") // a configured profile
	r, b := newEventRig(t)
	r.api.fleet.mu.Lock()
	r.api.fleet.signInFlow = flow
	r.api.fleet.mu.Unlock()
	return r, b
}

func waitState(t *testing.T, a *API, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if snap(t, a).State == want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("snapshot never reached %q (last %q)", want, snap(t, a).State)
}

func TestSignInFlowTimeout_IsRealistic(t *testing.T) {
	if fleet.SignInFlowTimeout < 5*time.Minute || fleet.SignInFlowTimeout > 15*time.Minute {
		t.Fatalf("SignInFlowTimeout = %v, want within the owner's 5–15 min range (was 60s: dogfood B3)", fleet.SignInFlowTimeout)
	}
	if want := "(" + fleet.SignInFlowTimeout.String() + ")"; !strings.Contains(fleet.ErrAuthTimeout.Error(), want) {
		t.Fatalf("ErrAuthTimeout = %q, want it derived from the constant (%s)", fleet.ErrAuthTimeout, want)
	}
}

// P-5
func TestFleetSignInCancel_EndsFlowWithCanceled_NoErrorLog(t *testing.T) {
	logs := captureSettingsLogs(t)
	r, b := newSignInRig(t, func(ctx context.Context, _ fleet.EnvProfile) (fleet.TokenSet, error) {
		<-ctx.Done() // the user never finishes in the browser
		return fleet.TokenSet{}, ctx.Err()
	})

	errCh := make(chan error, 1)
	go func() {
		_, err := r.api.FleetSignIn(context.Background())
		errCh <- err
	}()
	waitState(t, r.api, FleetSessionSigningIn)
	// The broker event is emitted AFTER the state becomes snapshot-visible
	// (publishFleetSession runs on the transition's tail), so poll for it
	// instead of asserting the instant waitState returns (CI flake,
	// 2026-10-05).
	sawSigningIn := false
	for i := 0; i < 200 && !sawSigningIn; i++ {
		if ev := last(b.sessionEvents()); ev != nil && ev.State == FleetSessionSigningIn {
			sawSigningIn = true
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !sawSigningIn {
		t.Fatalf("no signing_in event pushed (UI cannot show 'Waiting for browser… Cancel')")
	}

	if err := r.api.FleetSignInCancel(context.Background()); err != nil {
		t.Fatalf("FleetSignInCancel: %v", err)
	}
	select {
	case err := <-errCh:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("FleetSignIn err = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancel did not end the in-flight flow")
	}
	if n := logs.count(slog.LevelError, "fleet.rpc.sign_in.device_code_flow_failed"); n != 0 {
		t.Fatalf("a user cancel logged %d ERROR(s); it is an outcome, not a failure", n)
	}
	v := snap(t, r.api)
	if v.State != FleetSessionSignedOut || v.Reason != FleetReasonSignInCancelled {
		t.Fatalf("after cancel state=%q reason=%q, want signed_out/sign_in_cancelled", v.State, v.Reason)
	}
	// Cancel with nothing in flight is a no-op.
	if err := r.api.FleetSignInCancel(context.Background()); err != nil {
		t.Fatalf("idle cancel: %v", err)
	}
}

// P-6
func TestFleetSignIn_ConcurrentCallsJoinOneFlow(t *testing.T) {
	var opens atomic.Int32
	release := make(chan struct{})
	var r *sessionRig
	r, _ = newSignInRig(t, func(ctx context.Context, _ fleet.EnvProfile) (fleet.TokenSet, error) {
		opens.Add(1) // one call == one browser window
		select {
		case <-release:
		case <-ctx.Done():
			return fleet.TokenSet{}, ctx.Err()
		}
		r.setToken(jwtFor("sub-alice", "zitadel-org-1"))
		return fleet.TokenSet{AccessToken: jwtFor("sub-alice", "zitadel-org-1")}, nil
	})

	var wg sync.WaitGroup
	results := make([]error, 2)
	ids := make([]FleetIdentity, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ids[i], results[i] = r.api.FleetSignIn(context.Background())
		}(i)
	}
	waitState(t, r.api, FleetSessionSigningIn)
	time.Sleep(50 * time.Millisecond) // let the second caller arrive and join
	close(release)
	wg.Wait()

	if n := opens.Load(); n != 1 {
		t.Fatalf("%d browser flows opened for two concurrent sign-ins, want 1", n)
	}
	for i := range results {
		if results[i] != nil || ids[i].OrgName != "Kameas Dogfood" {
			t.Fatalf("caller %d: id=%+v err=%v, want the shared successful result", i, ids[i], results[i])
		}
	}
	if v := snap(t, r.api); v.State != FleetSessionSignedIn {
		t.Fatalf("after sign-in state = %q, want signed_in", v.State)
	}
}
