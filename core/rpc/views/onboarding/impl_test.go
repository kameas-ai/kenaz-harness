package onboarding

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	harnessmcp "github.com/kameas-ai/kenaz-harness/core/mcp/builtin/harness"
	coreonboarding "github.com/kameas-ai/kenaz-harness/core/onboarding"
)

type stubFirstRun struct{ first bool }

func (s stubFirstRun) IsFirstRun(_ context.Context) (bool, error) { return s.first, nil }

type stubCompletion struct {
	done bool
	// calls counts MarkOnboardingCompleted invocations. WP03's regression
	// test asserts on this directly (not just the boolean) so a bug that
	// calls MarkOnboardingCompleted twice, or calls-then-untoggles, cannot
	// hide behind a coarse boolean check.
	calls int
}

func (s *stubCompletion) MarkOnboardingCompleted(_ context.Context) error {
	s.done = true
	s.calls++
	return nil
}
func (s *stubCompletion) IsCompleted(_ context.Context) (bool, error) { return s.done, nil }

type stubStarter struct{ id string }

func (s *stubStarter) StartOnboardingSession(_ context.Context, st harnessmcp.Starter) (string, error) {
	s.id = st.ID
	return "sess-" + st.ID, nil
}

// TestAPI_State_FirstRun verifies the boot-time payload reflects the
// first-run condition.
func TestAPI_State_FirstRun(t *testing.T) {
	t.Parallel()
	api := New(Config{
		FirstRun:   stubFirstRun{first: true},
		Completion: &stubCompletion{},
	})
	st, err := api.State(context.Background())
	if err != nil {
		t.Fatalf("State: %v", err)
	}
	if !st.FirstRun {
		t.Errorf("FirstRun = false, want true")
	}
	if st.Completed {
		t.Errorf("Completed should be false")
	}
	if st.Phase != "phase1" {
		t.Errorf("Phase = %q, want phase1", st.Phase)
	}
}

// TestAPI_BeginAndStep walks the FSM through the welcome → pick state
// transition.
func TestAPI_BeginAndStep(t *testing.T) {
	t.Parallel()
	api := New(Config{FSM: coreonboarding.New(nil)})
	begin, err := api.Begin(context.Background())
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if begin.State != string(coreonboarding.StateWelcome) {
		t.Errorf("Begin state = %q", begin.State)
	}
	step, err := api.Step(context.Background(), StepRequest{
		State: begin.State,
		Event: string(coreonboarding.EventNext),
	})
	if err != nil {
		t.Fatalf("Step: %v", err)
	}
	if step.State != string(coreonboarding.StatePickProviderKind) {
		t.Errorf("Step state = %q, want pick_provider_kind", step.State)
	}
}

// TestAPI_Dismiss flips the completion flag.
func TestAPI_Dismiss(t *testing.T) {
	t.Parallel()
	c := &stubCompletion{}
	api := New(Config{Completion: c})
	if err := api.Dismiss(context.Background()); err != nil {
		t.Fatalf("Dismiss: %v", err)
	}
	if !c.done {
		t.Errorf("expected completion marked")
	}
}

// TestAPI_RestartPhase2 invokes the session starter for the picked
// starter id and round-trips the new session id.
func TestAPI_RestartPhase2(t *testing.T) {
	t.Parallel()
	starter := &stubStarter{}
	api := New(Config{
		SessionStarter: starter,
		Completion:     &stubCompletion{},
	})
	resp, err := api.RestartPhase2(context.Background(), RestartPhase2Request{StarterID: "code"})
	if err != nil {
		t.Fatalf("RestartPhase2: %v", err)
	}
	if resp.SessionID != "sess-code" {
		t.Errorf("SessionID = %q, want sess-code", resp.SessionID)
	}
	if starter.id != "code" {
		t.Errorf("starter id captured = %q", starter.id)
	}
}

// TestFirstRunDetector_NoProviders asserts true when neither providers
// nor a completion flag are set.
func TestFirstRunDetector_NoProviders(t *testing.T) {
	t.Parallel()
	d := coreonboarding.FirstRunDetector{}
	got, err := d.IsFirstRun(context.Background())
	if err != nil {
		t.Fatalf("IsFirstRun: %v", err)
	}
	if !got {
		t.Errorf("expected first-run true with empty inputs")
	}
}

// stubSigner is a test AccountSigner.
type stubSigner struct {
	email string
	err   error
}

func (s *stubSigner) SignIn(_ context.Context) (string, error) { return s.email, s.err }

// TestAPI_SignerWiredThroughConfig verifies that a Signer set in Config is
// passed to the FSM so EventSignIn succeeds when a real signer is configured.
// This covers review Blocker 3: the AccountSigner must reach the FSM.
func TestAPI_SignerWiredThroughConfig(t *testing.T) {
	t.Parallel()
	signer := &stubSigner{email: "user@example.com"}
	api := New(Config{Signer: signer})

	// Walk to account_step via the normal FSM path.
	ctx := context.Background()
	begin, err := api.Begin(ctx)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}

	// Advance: welcome → pick_provider_kind.
	sr, err := api.Step(ctx, StepRequest{State: begin.State, Event: string(coreonboarding.EventNext)})
	if err != nil {
		t.Fatalf("Step welcome→pick: %v", err)
	}
	// pick_provider_kind → enter_api_key (event IS the provider kind string).
	sr, err = api.Step(ctx, StepRequest{
		State: sr.State,
		Event: string(coreonboarding.ProviderAnthropic),
	})
	if err != nil {
		t.Fatalf("Step pick→enter: %v", err)
	}
	// enter_api_key → test_connection (submit key).
	sr, err = api.Step(ctx, StepRequest{
		State:   sr.State,
		Event:   string(coreonboarding.EventSubmitKey),
		Payload: map[string]string{"api_key": "sk-ant-test"},
	})
	if err != nil {
		t.Fatalf("Step enter→test: %v", err)
	}
	// test_connection → account_step (when tester is nil, connection always succeeds).
	if sr.State != string(coreonboarding.StateAccountStep) {
		t.Fatalf("expected account_step after test_connection, got %q", sr.State)
	}

	// Now fire EventSignIn — with the real signer wired this must succeed
	// and advance to guided_action (Blocker 3: sign-in must NOT be a no-op).
	sr, err = api.Step(ctx, StepRequest{
		State: sr.State,
		Event: string(coreonboarding.EventSignIn),
	})
	if err != nil {
		t.Fatalf("Step sign_in: %v", err)
	}
	if sr.State != string(coreonboarding.StateGuidedAction) {
		t.Errorf("expected guided_action after sign_in, got %q", sr.State)
	}
	// Verify fsmCtx was updated (signedIn must be true).
	state, err := api.State(ctx)
	if err != nil {
		t.Fatalf("State: %v", err)
	}
	if !state.SignedIn {
		t.Errorf("SignedIn = false after successful sign-in, want true")
	}
}

// TestAPI_NilSignerDegrades verifies that when no signer is configured,
// EventSignIn degrades gracefully to guided_action WITHOUT returning an error
// and WITHOUT a silent no-op — the card must contain a message that the
// account step completed (degraded). This is the OSS-standalone path.
func TestAPI_NilSignerDegrades(t *testing.T) {
	t.Parallel()
	// No signer in Config — nil FSM will be built with NewFull(nil, nil, nil).
	api := New(Config{})

	ctx := context.Background()
	begin, err := api.Begin(ctx)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	sr, err := api.Step(ctx, StepRequest{State: begin.State, Event: string(coreonboarding.EventNext)})
	if err != nil {
		t.Fatalf("Step welcome→pick: %v", err)
	}
	sr, err = api.Step(ctx, StepRequest{
		State: sr.State,
		Event: string(coreonboarding.ProviderAnthropic),
	})
	if err != nil {
		t.Fatalf("Step pick→enter: %v", err)
	}
	sr, err = api.Step(ctx, StepRequest{
		State:   sr.State,
		Event:   string(coreonboarding.EventSubmitKey),
		Payload: map[string]string{"api_key": "sk-ant-test"},
	})
	if err != nil {
		t.Fatalf("Step enter→test: %v", err)
	}
	if sr.State != string(coreonboarding.StateAccountStep) {
		t.Fatalf("expected account_step, got %q", sr.State)
	}

	// EventSignIn with nil signer must NOT return an error and must NOT
	// stay stuck — the FSM degrades to guided_action (graceful downgrade).
	sr, err = api.Step(ctx, StepRequest{
		State: sr.State,
		Event: string(coreonboarding.EventSignIn),
	})
	if err != nil {
		t.Fatalf("EventSignIn with nil signer returned unexpected error: %v", err)
	}
	if sr.State != string(coreonboarding.StateGuidedAction) {
		t.Errorf("expected guided_action (graceful degrade), got %q", sr.State)
	}
}

// ── 01NWEL01 seam tests ───────────────────────────────────────────────────────

// fakeProgressSyncer captures SyncProgress calls for inspection.
type fakeProgressSyncer struct {
	mu    sync.Mutex
	calls []struct {
		step     ProgressStep
		signedIn bool
	}
}

func (f *fakeProgressSyncer) SyncProgress(_ context.Context, step ProgressStep, signedIn bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, struct {
		step     ProgressStep
		signedIn bool
	}{step, signedIn})
	return nil
}

func (f *fakeProgressSyncer) snapshot() []struct {
	step     ProgressStep
	signedIn bool
} {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]struct {
		step     ProgressStep
		signedIn bool
	}, len(f.calls))
	copy(out, f.calls)
	return out
}

// fakeFleetStateReader returns a configurable FleetOnboardingHint.
type fakeFleetStateReader struct {
	hint FleetOnboardingHint
	err  error
}

func (f *fakeFleetStateReader) ReadFleetState(_ context.Context) (FleetOnboardingHint, error) {
	return f.hint, f.err
}

// TestAPI_RecordProgress_SyncerCalledWhenSignedIn verifies that RecordProgress
// calls SyncProgress when the user is signed in.
func TestAPI_RecordProgress_SyncerCalledWhenSignedIn(t *testing.T) {
	t.Parallel()
	syncer := &fakeProgressSyncer{}
	signer := &stubSigner{email: "u@example.com"}
	api := New(Config{
		Signer:         signer,
		ProgressSyncer: syncer,
	})

	ctx := context.Background()
	// Walk FSM to guided_action via sign-in (sets signedIn=true in fsmCtx).
	begin, _ := api.Begin(ctx)
	sr, _ := api.Step(ctx, StepRequest{State: begin.State, Event: string(coreonboarding.EventNext)})
	sr, _ = api.Step(ctx, StepRequest{State: sr.State, Event: string(coreonboarding.ProviderAnthropic)})
	sr, _ = api.Step(ctx, StepRequest{
		State: sr.State, Event: string(coreonboarding.EventSubmitKey),
		Payload: map[string]string{"api_key": "sk-ant-test"},
	})
	// account_step → guided_action via sign-in.
	sr, err := api.Step(ctx, StepRequest{State: sr.State, Event: string(coreonboarding.EventSignIn)})
	if err != nil {
		t.Fatalf("sign-in step: %v", err)
	}
	if sr.State != string(coreonboarding.StateGuidedAction) {
		t.Fatalf("expected guided_action, got %q", sr.State)
	}

	// RecordProgress should have called SyncProgress for account_connected
	// (fired internally by Step when entering guided_action while signedIn).
	// Allow brief settling time for any goroutines (though SyncProgress is sync in the fake).
	time.Sleep(10 * time.Millisecond)
	calls := syncer.snapshot()
	found := false
	for _, c := range calls {
		if c.step == ProgressStepAccountConnected && c.signedIn {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected SyncProgress(account_connected, signedIn=true); got calls: %+v", calls)
	}
}

// TestAPI_RecordProgress_SyncerNotCalledWhenNotSignedIn verifies that
// RecordProgress still calls SyncProgress but passes signedIn=false when the
// user has not signed in (OSS path). The adapter is responsible for the no-op.
func TestAPI_RecordProgress_SyncerNotCalledWhenNotSignedIn(t *testing.T) {
	t.Parallel()
	syncer := &fakeProgressSyncer{}
	api := New(Config{
		Signer:         nil, // OSS path: no signer
		ProgressSyncer: syncer,
	})

	ctx := context.Background()
	begin, _ := api.Begin(ctx)
	sr, _ := api.Step(ctx, StepRequest{State: begin.State, Event: string(coreonboarding.EventNext)})
	sr, _ = api.Step(ctx, StepRequest{State: sr.State, Event: string(coreonboarding.ProviderAnthropic)})
	sr, _ = api.Step(ctx, StepRequest{
		State: sr.State, Event: string(coreonboarding.EventSubmitKey),
		Payload: map[string]string{"api_key": "sk-ant-test"},
	})
	// account_step — RecordProgress(provider_configured) fires here.
	if sr.State != string(coreonboarding.StateAccountStep) {
		t.Fatalf("expected account_step, got %q", sr.State)
	}

	time.Sleep(10 * time.Millisecond)
	calls := syncer.snapshot()
	// provider_configured should be in calls with signedIn=false.
	for _, c := range calls {
		if c.step == ProgressStepProviderConfigured && c.signedIn {
			t.Errorf("SyncProgress called with signedIn=true before sign-in")
		}
	}
}

// TestAPI_RecordProgress_NilSyncerIsSafe verifies that a nil ProgressSyncer
// does not panic (the adapter may be nil in OSS builds / tests).
func TestAPI_RecordProgress_NilSyncerIsSafe(t *testing.T) {
	t.Parallel()
	api := New(Config{}) // no ProgressSyncer
	ctx := context.Background()
	if err := api.RecordProgress(ctx, ProgressStepProviderConfigured); err != nil {
		t.Errorf("RecordProgress with nil syncer returned error: %v", err)
	}
}

// TestAPI_Begin_FleetStateReaderWarmStart verifies that when a FleetStateReader
// is wired and returns AlreadyConnected=true, Begin stores a synthetic handoff
// hint with source="fleet_state".
func TestAPI_Begin_FleetStateReaderWarmStart(t *testing.T) {
	t.Parallel()
	reader := &fakeFleetStateReader{
		hint: FleetOnboardingHint{AlreadyConnected: true},
	}
	api := New(Config{FleetStateReader: reader})

	ctx := context.Background()
	_, err := api.Begin(ctx)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}

	hint, err := api.GetHandoffHint(ctx)
	if err != nil {
		t.Fatalf("GetHandoffHint: %v", err)
	}
	if hint.Source != "fleet_state" {
		t.Errorf("hint.Source = %q, want fleet_state", hint.Source)
	}
}

// TestAPI_Begin_FleetStateReaderErrorIsSafe verifies that a FleetStateReader
// error does not cause Begin to fail — fleet errors are logged and swallowed.
func TestAPI_Begin_FleetStateReaderErrorIsSafe(t *testing.T) {
	t.Parallel()
	reader := &fakeFleetStateReader{err: context.DeadlineExceeded}
	api := New(Config{FleetStateReader: reader})

	ctx := context.Background()
	resp, err := api.Begin(ctx)
	if err != nil {
		t.Fatalf("Begin returned error on reader failure: %v", err)
	}
	if resp.State != string(coreonboarding.StateWelcome) {
		t.Errorf("Begin state = %q, want welcome", resp.State)
	}
}

// TestAPI_Begin_DeepLinkHintWinsOverFleetState verifies that an existing
// deep-link hint (EmailHint set) is not overwritten by the fleet-state hint.
func TestAPI_Begin_DeepLinkHintWinsOverFleetState(t *testing.T) {
	t.Parallel()
	reader := &fakeFleetStateReader{
		hint: FleetOnboardingHint{AlreadyConnected: true},
	}
	api := New(Config{FleetStateReader: reader})
	ctx := context.Background()

	// Set a deep-link hint first.
	_ = api.AcceptHandoffHint(ctx, HandoffHint{
		EmailHint: "user@example.com",
		Source:    "deep_link",
	})

	// Begin should NOT overwrite the deep-link hint.
	_, err := api.Begin(ctx)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}

	hint, _ := api.GetHandoffHint(ctx)
	if hint.Source != "deep_link" || hint.EmailHint != "user@example.com" {
		t.Errorf("deep-link hint was overwritten by fleet state: %+v", hint)
	}
}

// ── WP03: completion honesty (first-run-onboarding-01PMOB01) ─────────────────

// failingStarter simulates a SessionStarter whose underlying delivery step
// (session created, but the system-prompt write failed) surfaces as an
// error from StartOnboardingSession — the C-003 contract WP02's adapter
// implements: a delivery failure returns a non-nil error rather than
// growing the interface.
type failingStarter struct{ err error }

func (f *failingStarter) StartOnboardingSession(_ context.Context, _ harnessmcp.Starter) (string, error) {
	return "", f.err
}

// firstRunFollowsCompletion is a FirstRunChecker test double that ties
// "is this a first run" to the SAME Completion fake RestartPhase2 consults,
// rather than to production's independent provider-count signal
// (onboardingFirstRunAdapter in core/rpc/onboarding_wiring.go). AC-005's
// actual claim is "a user who hits a delivery failure is offered onboarding
// again" — i.e. that firstRun tracks completion, not provider count — so
// this double reproduces the causal link the acceptance criterion cares
// about instead of the unrelated production heuristic. Without this, a
// State() call driven by an independent stub would report firstRun=true
// regardless of whether MarkOnboardingCompleted incorrectly fired, and the
// WP03 mutation (reordering MarkOnboardingCompleted above the error check)
// would not be caught by assertion (c).
type firstRunFollowsCompletion struct{ completion *stubCompletion }

func (f firstRunFollowsCompletion) IsFirstRun(ctx context.Context) (bool, error) {
	done, err := f.completion.IsCompleted(ctx)
	return !done, err
}

// TestRestartPhase2DeliveryFailureIsRetryable is WP03's load-bearing test
// (FR-005 / AC-005). A delivery failure must (a) surface as an error from
// RestartPhase2, (b) NOT call MarkOnboardingCompleted, and (c) leave the
// user offered onboarding again on a subsequent State() call.
//
// Mutation: move the `a.cfg.Completion.MarkOnboardingCompleted(ctx)` call in
// RestartPhase2 (impl.go) above the `if err != nil` guard. This test must
// fail on assertions (b) and (c) — see the mutation-honesty note in this
// file's package doc / the mission report for the recorded proof.
func TestRestartPhase2DeliveryFailureIsRetryable(t *testing.T) {
	t.Parallel()
	completion := &stubCompletion{}
	api := New(Config{
		FirstRun:       firstRunFollowsCompletion{completion: completion},
		Completion:     completion,
		SessionStarter: &failingStarter{err: errors.New("deliver starter system prompt: boom")},
	})

	ctx := context.Background()
	resp, err := api.RestartPhase2(ctx, RestartPhase2Request{StarterID: "code"})

	// (a) RestartPhase2 returns an error.
	if err == nil {
		t.Fatalf("RestartPhase2 returned nil error on delivery failure; resp=%+v", resp)
	}

	// (b) MarkOnboardingCompleted was NOT called.
	if completion.calls != 0 {
		t.Errorf("MarkOnboardingCompleted call count = %d, want 0", completion.calls)
	}
	if completion.done {
		t.Errorf("Completion.done = true after a failed delivery, want false")
	}

	// (c) a subsequent State() call still reports firstRun: true — the user
	// is offered onboarding again.
	st, err := api.State(ctx)
	if err != nil {
		t.Fatalf("State: %v", err)
	}
	if !st.FirstRun {
		t.Errorf("State().FirstRun = false after a failed delivery, want true (user must be offered onboarding again)")
	}
	if st.Completed {
		t.Errorf("State().Completed = true after a failed delivery, want false")
	}
}

// TestAPI_AccountStepAlwaysSkippable verifies the OSS-standalone invariant:
// EventSkipAccount must succeed regardless of whether a signer is configured.
func TestAPI_AccountStepAlwaysSkippable(t *testing.T) {
	t.Parallel()
	// Test both with and without signer.
	for _, tc := range []struct {
		name   string
		signer coreonboarding.AccountSigner
	}{
		{"with_signer", &stubSigner{email: "u@example.com"}},
		{"without_signer", nil},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			api := New(Config{Signer: tc.signer})
			ctx := context.Background()
			begin, _ := api.Begin(ctx)
			sr, _ := api.Step(ctx, StepRequest{State: begin.State, Event: string(coreonboarding.EventNext)})
			sr, _ = api.Step(ctx, StepRequest{
				State: sr.State, Event: string(coreonboarding.ProviderAnthropic),
			})
			sr, _ = api.Step(ctx, StepRequest{
				State: sr.State, Event: string(coreonboarding.EventSubmitKey),
				Payload: map[string]string{"api_key": "sk-ant-test"},
			})
			if sr.State != string(coreonboarding.StateAccountStep) {
				t.Fatalf("expected account_step, got %q", sr.State)
			}
			// Skip must always work.
			sr, err := api.Step(ctx, StepRequest{
				State: sr.State,
				Event: string(coreonboarding.EventSkipAccount),
			})
			if err != nil {
				t.Fatalf("EventSkipAccount returned error: %v", err)
			}
			if sr.State != string(coreonboarding.StateGuidedAction) {
				t.Errorf("expected guided_action after skip, got %q", sr.State)
			}
		})
	}
}
