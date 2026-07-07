package onboarding

import (
	"context"
	"testing"

	harnessmcp "github.com/kameas-ai/kenaz-harness/core/mcp/builtin/harness"
	coreonboarding "github.com/kameas-ai/kenaz-harness/core/onboarding"
)

type stubFirstRun struct{ first bool }

func (s stubFirstRun) IsFirstRun(_ context.Context) (bool, error) { return s.first, nil }

type stubCompletion struct{ done bool }

func (s *stubCompletion) MarkOnboardingCompleted(_ context.Context) error { s.done = true; return nil }
func (s *stubCompletion) IsCompleted(_ context.Context) (bool, error)     { return s.done, nil }

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
