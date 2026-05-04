package onboarding

import (
	"context"
	"testing"

	harnessmcp "github.com/sigil-tech/kaneaz-harness/core/mcp/builtin/harness"
	coreonboarding "github.com/sigil-tech/kaneaz-harness/core/onboarding"
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
