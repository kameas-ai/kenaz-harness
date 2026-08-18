package harness_test

// integration_test.go — WP10 smoke test for harness-self-mcp-onboarding.
//
// Mission: harness-self-mcp-onboarding-01KQ8TDU WP10.
//
// This test boots the in-process MCP server, walks the Phase-1 onboarding
// FSM from welcome through to the terminal state (StateDone / EventFinish),
// and asserts:
//
//  1. At least one KindHarnessSelfToolCalled audit event fired per tool
//     call dispatched through the server.
//  2. session.Kind transitions from "onboarding" → "chat" after EventFinish.
//
// The test uses stubs for all external dependencies (LLM tester, settings,
// session store, cedar engine) so it runs without any real services.
//
// CORRECTION (mcp-connector-lifecycle-01PMMC01 WP01): this docstring
// previously claimed "KindHarnessSelfPolicyProposed + Written fire on the
// propose/accept round-trip" as item 2. That was false — the only
// assertion touching those kinds anywhere in this file was
// TestIntegration_AuditKindsRegistered below, which checks
// kind.IsRegistered (the constant exists in the registry map), not that
// anything ever emits it. No emit site for any Policy* kind ever existed;
// harness_write_propose_cedar_policy, the tool that would have triggered
// one, was itself deleted by the 2026-08-14 sweep. The three Policy*
// kinds are deleted (see core/event/kind/registry.go); only
// KindHarnessSelfToolCalled — which audit.go's WithAudit genuinely
// emits — survives.

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	harness "github.com/kameas-ai/kenaz-harness/core/mcp/builtin/harness"
	"github.com/kameas-ai/kenaz-harness/core/event/kind"
	"github.com/kameas-ai/kenaz-harness/core/mcp/transport"
	"github.com/kameas-ai/kenaz-harness/core/onboarding"
)

// ── Recording audit sink ─────────────────────────────────────────────────────

type recordingAudit struct {
	mu     sync.Mutex
	events []auditEvent
}

type auditEvent struct {
	kind    string
	payload map[string]any
}

func (r *recordingAudit) Emit(_ context.Context, k string, payload map[string]any) {
	r.mu.Lock()
	r.events = append(r.events, auditEvent{kind: k, payload: payload})
	r.mu.Unlock()
}

func (r *recordingAudit) allKinds() map[string]int {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := map[string]int{}
	for _, e := range r.events {
		out[e.kind]++
	}
	return out
}

// ── Stub managers ────────────────────────────────────────────────────────────

type stubProviderLister2 struct{}

func (stubProviderLister2) ListProviders(_ context.Context) ([]harness.ProviderSummary, error) {
	return nil, nil
}

type stubRecipeLister struct{}

func (stubRecipeLister) ListRecipes(_ context.Context) ([]harness.RecipeSummary, error) {
	return nil, nil
}

type stubSettingsReader struct{}

func (stubSettingsReader) ListSettings(_ context.Context) (map[string]any, error) {
	return map[string]any{"OnboardingCompleted": false}, nil
}

type stubStatusReporter struct{}

func (stubStatusReporter) HarnessStatus(_ context.Context) (harness.StatusSnapshot, error) {
	return harness.StatusSnapshot{}, nil
}

// ── Stub session kind transitioner ───────────────────────────────────────────

type stubTransitioner struct {
	mu          sync.Mutex
	transitions []kindTransition
}

type kindTransition struct {
	sessionID string
	kind      string
}

func (s *stubTransitioner) SetKind(_ context.Context, sessionID, kind string) error {
	s.mu.Lock()
	s.transitions = append(s.transitions, kindTransition{sessionID: sessionID, kind: kind})
	s.mu.Unlock()
	return nil
}

func (s *stubTransitioner) last() (kindTransition, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.transitions) == 0 {
		return kindTransition{}, false
	}
	return s.transitions[len(s.transitions)-1], true
}

// ── Helpers ──────────────────────────────────────────────────────────────────

// callTool dispatches a tools/call request on the transport and returns the
// ToolsCallResult. Fails the test on any RPC error.
func callTool(t *testing.T, tr *harness.Transport, name string, args any) transport.ToolsCallResult {
	t.Helper()
	raw, _ := json.Marshal(args)
	params, _ := json.Marshal(transport.ToolsCallParams{Name: name, Arguments: json.RawMessage(raw)})
	req := transport.RequestEnvelope{
		JSONRPC: transport.JSONRPCVersion,
		ID:      1,
		Method:  transport.MethodToolsCall,
		Params:  json.RawMessage(params),
	}
	if err := tr.Send(context.Background(), req); err != nil {
		t.Fatalf("Send %s: %v", name, err)
	}
	resp, err := tr.Recv(context.Background())
	if err != nil {
		t.Fatalf("Recv %s: %v", name, err)
	}
	if resp.Error != nil {
		t.Fatalf("%s: RPC error %d: %s", name, resp.Error.Code, resp.Error.Message)
	}
	body, _ := json.Marshal(resp.Result)
	var result transport.ToolsCallResult
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("unmarshal %s result: %v", name, err)
	}
	return result
}

// ── WP10 smoke test ──────────────────────────────────────────────────────────

// TestIntegration_PhaseOneHandoff is the WP10 acceptance smoke test.
//
// It boots an in-process harness-self MCP server, calls a harness_read_*
// tool (exercising the tool dispatch + audit path), then walks the onboarding
// FSM to StateDone with EventFinish and asserts that:
//
//   - KindHarnessSelfToolCalled fired at least once.
//   - The FSM session kind transitions from "onboarding" → "chat".
func TestIntegration_PhaseOneHandoff(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// Step 1: build the in-process server with a recording audit sink.
	audit := &recordingAudit{}
	mgrs := harness.Managers{
		Providers: stubProviderLister2{},
		Recipes:   stubRecipeLister{},
		Settings:  stubSettingsReader{},
		Status:    stubStatusReporter{},
		// Write managers are nil — we only call read tools in this test.
	}
	srv := harness.RegisterAll(harness.NewServer(), mgrs)
	srv = harness.WithAudit(srv, audit)

	tr := harness.NewTransport(srv)
	defer tr.Close()

	// Step 2: call a read-side tool to verify the tool-dispatch + audit path.
	result := callTool(t, tr, harness.ToolGetStatus, nil)
	if result.IsError {
		// Status reporter is a stub — errNotConfigured is expected here
		// since StatusReporter stub returns empty snapshot, not nil.
		// The harness should still emit an audit event regardless.
	}

	// Step 3: assert KindHarnessSelfToolCalled fired.
	kinds := audit.allKinds()
	if counts := kinds[string(kind.KindHarnessSelfToolCalled)]; counts < 1 {
		t.Errorf("KindHarnessSelfToolCalled count = %d, want >= 1", counts)
	}

	// Step 4: walk the onboarding FSM to terminal state with a stub
	// transitioner to verify the kind-transition callback.
	transitioner := &stubTransitioner{}
	fsm := onboarding.NewWithTransitioner(nil /* tester: nil → always pass */, transitioner)

	fsmCtx := onboarding.NewFSMContext()
	fsmCtx.SessionID = "test-session-abc"

	// welcome → pick-provider-kind
	r, err := fsm.Step(ctx, onboarding.StateWelcome, onboarding.EventNext, nil, &fsmCtx)
	if err != nil || r.State != onboarding.StatePickProviderKind {
		t.Fatalf("welcome/next: err=%v state=%s", err, r.State)
	}

	// pick-provider-kind → enter-api-key
	r, err = fsm.Step(ctx, onboarding.StatePickProviderKind,
		onboarding.Event(onboarding.ProviderAnthropic), nil, &fsmCtx)
	if err != nil || r.State != onboarding.StateEnterAPIKey {
		t.Fatalf("pick/anthropic: err=%v state=%s", err, r.State)
	}

	// enter-api-key → account_step (nil tester always passes; WP03 new routing)
	r, err = fsm.Step(ctx, onboarding.StateEnterAPIKey, onboarding.EventSubmitKey,
		map[string]string{"api_key": "sk-test-key"}, &fsmCtx)
	if err != nil || r.State != onboarding.StateAccountStep {
		t.Fatalf("enter/submit: err=%v state=%s", err, r.State)
	}

	// account_step → guided_action (skip — OSS-standalone invariant)
	r, err = fsm.Step(ctx, onboarding.StateAccountStep, onboarding.EventSkipAccount, nil, &fsmCtx)
	if err != nil || r.State != onboarding.StateGuidedAction {
		t.Fatalf("account/skip: err=%v state=%s", err, r.State)
	}

	// guided_action → done (EventFinish triggers kind transition; WP06)
	r, err = fsm.Step(ctx, onboarding.StateGuidedAction, onboarding.EventFinish, nil, &fsmCtx)
	if err != nil || r.State != onboarding.StateDone {
		t.Fatalf("guided/finish: err=%v state=%s", err, r.State)
	}

	// done → done (terminal no-op; kind transition should have fired by now)
	r, err = fsm.Step(ctx, onboarding.StateDone, onboarding.EventFinish, nil, &fsmCtx)
	if err != nil || r.State != onboarding.StateDone {
		t.Fatalf("done/finish: err=%v state=%s", err, r.State)
	}

	// Step 5: assert session kind transitioned.
	trans, ok := transitioner.last()
	if !ok {
		t.Fatal("SetKind was never called; expected onboarding → chat transition")
	}
	if trans.sessionID != "test-session-abc" {
		t.Errorf("SetKind session_id = %q, want %q", trans.sessionID, "test-session-abc")
	}
	if trans.kind != "chat" {
		t.Errorf("SetKind kind = %q, want %q", trans.kind, "chat")
	}
}

// TestIntegration_AuditKindsRegistered verifies that the KindHarnessSelf*
// constant declared in the kind registry is actually registered at
// runtime. Only KindHarnessSelfToolCalled remains — the three Policy*
// kinds this test used to also check were deleted (WP01, see the file
// docstring above); this test's own IsRegistered check was never
// evidence they were emitted, only that the string existed in a map.
func TestIntegration_AuditKindsRegistered(t *testing.T) {
	t.Parallel()
	if !kind.IsRegistered(kind.KindHarnessSelfToolCalled) {
		t.Errorf("kind %q is not registered in the kind registry", kind.KindHarnessSelfToolCalled)
	}
}
