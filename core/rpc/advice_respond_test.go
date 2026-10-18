package rpc

import (
	"context"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/advice"
	"github.com/kameas-ai/kenaz-harness/core/rpc/views/agentgraph/chat"
)

type respondTestFeatures struct {
	A int `json:"a"`
}

func respondTestKind(id string, safety advice.SafetyClass) advice.AdviceKind {
	return advice.AdviceKind{
		ID:            id,
		PromptVersion: "v1",
		SafetyClass:   safety,
		Extract:       func(input any) (advice.Features, error) { return input, nil },
		RenderPrompt:  func(advice.Features) (string, string) { return "sys", "usr" },
	}
}

// registerRespondTestKind registers k for the duration of the test,
// tolerating an already-registered production kind of the same id (the
// real branch_now/compact_now/escalate_model kinds are registered
// process-wide by their own packages' init(), which this test binary
// does not import — so registration here is expected to succeed for a
// fresh id and is a no-op-with-cleanup otherwise).
func registerRespondTestKind(t *testing.T, k advice.AdviceKind) {
	t.Helper()
	if _, ok := advice.Get(k.ID); ok {
		return
	}
	if err := advice.Register(k); err != nil {
		t.Fatalf("advice.Register(%s): %v", k.ID, err)
	}
}

func TestAdviceRespond_Dismiss_CallsAdvisorDismiss(t *testing.T) {
	kind := respondTestKind("advice_respond_test_dismiss", advice.SafetyReversible)
	registerRespondTestKind(t, kind)

	fake := advice.NewFakeAdvisor()
	a := &API{chatAdvisor: fake}

	const sessionID = "advice-respond-sess-dismiss"
	adviceShown.note(sessionID, kind.ID, respondTestFeatures{A: 1})

	childID, err := a.Advice_Respond(context.Background(), sessionID, kind.ID, "dismiss")
	if err != nil {
		t.Fatalf("Advice_Respond(dismiss): %v", err)
	}
	if childID != "" {
		t.Errorf("childID = %q, want empty for dismiss", childID)
	}

	// Dismiss must have landed on the FAKE advisor's own tombstone —
	// verified by confirming a subsequent Recommend for the same
	// features is suppressed (AC-03's cache contract).
	fake.ScriptFor(kind.ID, advice.Recommendation{Decision: true, Confidence: 99})
	sess := advice.SessionContext{SessionID: sessionID}
	if _, err := fake.Recommend(context.Background(), kind, respondTestFeatures{A: 1}, sess); err == nil {
		t.Error("Recommend after Advice_Respond(dismiss): want ErrNoAdvice (dismissed), got a recommendation")
	}
}

func TestAdviceRespond_Accept_BranchNow_CallsAutoActExecutor(t *testing.T) {
	kind := respondTestKind("branch_now", advice.SafetyReversible)
	registerRespondTestKind(t, kind)

	fake := advice.NewFakeAdvisor()
	var executed bool
	deps := &chat.AdviceDeps{
		AutoActBranchNow: func(_ context.Context, sessionID string) (string, error) {
			executed = true
			return "child-session-99", nil
		},
	}
	a := &API{chatAdvisor: fake, adviceDeps: deps}

	const sessionID = "advice-respond-sess-accept-branch"
	adviceShown.note(sessionID, "branch_now", respondTestFeatures{A: 2})

	childID, err := a.Advice_Respond(context.Background(), sessionID, "branch_now", "accept")
	if err != nil {
		t.Fatalf("Advice_Respond(accept): %v", err)
	}
	if !executed {
		t.Error("AutoActBranchNow was not called for a branch_now accept")
	}
	if childID != "child-session-99" {
		t.Errorf("childID = %q, want %q", childID, "child-session-99")
	}
}

func TestAdviceRespond_Accept_CompactNow_DoesNotCallBranchExecutor(t *testing.T) {
	kind := respondTestKind("advice_respond_test_compact", advice.SafetySuggestOnly)
	registerRespondTestKind(t, kind)

	fake := advice.NewFakeAdvisor()
	var executed bool
	deps := &chat.AdviceDeps{
		AutoActBranchNow: func(context.Context, string) (string, error) { executed = true; return "x", nil },
	}
	a := &API{chatAdvisor: fake, adviceDeps: deps}

	const sessionID = "advice-respond-sess-accept-compact"
	adviceShown.note(sessionID, kind.ID, respondTestFeatures{A: 3})

	childID, err := a.Advice_Respond(context.Background(), sessionID, kind.ID, "accept")
	if err != nil {
		t.Fatalf("Advice_Respond(accept): %v", err)
	}
	if executed {
		t.Error("AutoActBranchNow was called for a non-branch_now kind's accept")
	}
	if childID != "" {
		t.Errorf("childID = %q, want empty for a non-branch_now accept", childID)
	}
}

func TestAdviceRespond_NoShownEntry_IsBenignNoop(t *testing.T) {
	a := &API{chatAdvisor: advice.NewFakeAdvisor()}
	childID, err := a.Advice_Respond(context.Background(), "no-such-session", "branch_now", "accept")
	if err != nil {
		t.Fatalf("Advice_Respond with no shown entry: want nil error, got %v", err)
	}
	if childID != "" {
		t.Errorf("childID = %q, want empty", childID)
	}
}

func TestAdviceRespond_UnknownAction_Errors(t *testing.T) {
	kind := respondTestKind("advice_respond_test_unknown_action", advice.SafetyReversible)
	registerRespondTestKind(t, kind)
	a := &API{chatAdvisor: advice.NewFakeAdvisor()}
	const sessionID = "advice-respond-sess-unknown-action"
	adviceShown.note(sessionID, kind.ID, respondTestFeatures{A: 4})

	if _, err := a.Advice_Respond(context.Background(), sessionID, kind.ID, "frobnicate"); err == nil {
		t.Error("Advice_Respond with an unknown action: want error, got nil")
	}
}

func TestAdviceRespond_NilAdvisor_Errors(t *testing.T) {
	a := &API{}
	if _, err := a.Advice_Respond(context.Background(), "s", "branch_now", "accept"); err == nil {
		t.Error("Advice_Respond with nil chatAdvisor: want error, got nil")
	}
}
