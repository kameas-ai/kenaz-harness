package main

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	llm "github.com/kameas-ai/kenaz-harness/core/llm"
	"github.com/kameas-ai/kenaz-harness/core/policy/cedar"
)

// clearAgentEnv blanks every env var the executor resolution reads so tests
// are hermetic regardless of the developer/CI machine's environment.
// t.Setenv also registers restoration and forbids t.Parallel.
func clearAgentEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		agentExecEnv, agentProviderEnv, agentModelEnv, agentCredEnvEnv,
		"ANTHROPIC_API_KEY", "OPENROUTER_API_KEY", "OPENAI_API_KEY", "GEMINI_API_KEY",
	} {
		t.Setenv(k, "")
	}
}

// --- Fake provider adapter (no network) ---

// fakeAgentAdapter is a canned llm.ProviderAdapter for real-mode tests. It
// records the request + credential it was handed and replies with fixed text.
type fakeAgentAdapter struct {
	kind    string
	text    string
	gotReq  llm.GenerationRequest
	gotCred []byte
	// block, when set, returns a stream whose events channel never closes so
	// cancellation paths can be exercised.
	block bool
}

func (a *fakeAgentAdapter) Kind() string { return a.kind }
func (a *fakeAgentAdapter) Capabilities(_ string) llm.CapabilityDescriptor {
	return llm.CapabilityDescriptor{}
}
func (a *fakeAgentAdapter) Stream(_ context.Context, req llm.GenerationRequest, _ llm.ProviderProfile, cred []byte) (llm.Stream, error) {
	a.gotReq = req
	a.gotCred = append([]byte(nil), cred...)
	s := &fakeAgentStream{text: a.text}
	if a.block {
		s.events = make(chan llm.StreamEvent) // never closed → drain must not hang
		return s, nil
	}
	s.events = make(chan llm.StreamEvent, 1)
	s.events <- llm.StreamEvent{Kind: llm.StreamText, Text: a.text}
	close(s.events)
	return s, nil
}

type fakeAgentStream struct {
	text      string
	events    chan llm.StreamEvent
	cancelled atomic.Bool
}

func (s *fakeAgentStream) Events() <-chan llm.StreamEvent { return s.events }
func (s *fakeAgentStream) Cancel() error                  { s.cancelled.Store(true); return nil }
func (s *fakeAgentStream) Final() (llm.Response, error) {
	if s.cancelled.Load() {
		return llm.Response{}, errors.New("cancelled")
	}
	return llm.Response{
		Content:      []llm.ContentBlock{llm.ContentBlockFromText(s.text)},
		FinishReason: "stop",
	}, nil
}

// newFakeLLMExecutor builds a real-mode executor whose registry serves the
// fake adapter instead of the real anthropic client. The env credential path
// is real (env → credref → adapter cred bytes); only the wire call is faked.
// The policy guard is nil — registry.New's own nil-substitution applies
// (llm.AllowAllGuard{}), matching every caller here that isn't itself testing
// the guard wiring (see newFakeLLMExecutorWithGuard for those).
func newFakeLLMExecutor(t *testing.T, fake *fakeAgentAdapter) *llmExecutor {
	t.Helper()
	return newFakeLLMExecutorWithGuard(t, fake, nil)
}

// newFakeLLMExecutorWithGuard is newFakeLLMExecutor with an explicit policy
// guard, for the UNIT-1 tests (mission vm-execution-surface-truth-01PMZD14)
// that assert the guard set on registry.Options is actually consulted.
func newFakeLLMExecutorWithGuard(t *testing.T, fake *fakeAgentAdapter, guard llm.PolicyGuard) *llmExecutor {
	t.Helper()
	clearAgentEnv(t)
	t.Setenv("ANTHROPIC_API_KEY", "test-key-bytes")
	exec, err := newLLMExecutor(guard, newTestLogger())
	if err != nil {
		t.Fatalf("newLLMExecutor: %v", err)
	}
	if fake.kind == "" {
		fake.kind = "anthropic"
	}
	exec.reg.RegisterAdapter(fake)
	return exec
}

// --- Mode selection ---

// TestResolveAgentExecutorStubMode verifies KENAZ_AGENT_EXEC=stub yields the
// echo executor (Spec 058 FR-004: echo only behind the explicit flag).
func TestResolveAgentExecutorStubMode(t *testing.T) {
	clearAgentEnv(t)
	t.Setenv(agentExecEnv, "stub")
	exec := resolveAgentExecutor(newTestLogger(), nil)
	if _, ok := exec.(stubExecutor); !ok {
		t.Fatalf("expected stubExecutor, got %T", exec)
	}
	out, err := exec.Generate(context.Background(), "", "echo me")
	if err != nil {
		t.Fatalf("stub Generate: %v", err)
	}
	if out != "echo me" {
		t.Fatalf("stub must echo its input; got %q", out)
	}
}

// TestResolveAgentExecutorRealNoCredential verifies the honest degraded mode:
// real mode (default) with no resolvable credential yields an executor that
// fails with the NAMED error — never an echo (Spec 058 US3 / FR-003).
func TestResolveAgentExecutorRealNoCredential(t *testing.T) {
	for _, mode := range []string{"", "real"} {
		clearAgentEnv(t)
		t.Setenv(agentExecEnv, mode)
		exec := resolveAgentExecutor(newTestLogger(), nil)
		if _, ok := exec.(failingExecutor); !ok {
			t.Fatalf("mode %q: expected failingExecutor, got %T", mode, exec)
		}
		_, err := exec.Generate(context.Background(), "", "prompt")
		if err == nil {
			t.Fatalf("mode %q: expected error, got success", mode)
		}
		if !strings.Contains(err.Error(), "no_model_credential") {
			t.Fatalf("mode %q: error must be named no_model_credential; got %q", mode, err)
		}
		if !strings.Contains(err.Error(), "KENAZ_AGENT_EXEC=stub") {
			t.Fatalf("mode %q: error must be actionable (name the stub escape hatch); got %q", mode, err)
		}
	}
}

// TestResolveAgentExecutorUnknownMode verifies an unrecognised mode value
// fails tasks with a named config error rather than guessing a behaviour.
func TestResolveAgentExecutorUnknownMode(t *testing.T) {
	clearAgentEnv(t)
	t.Setenv(agentExecEnv, "yolo")
	t.Setenv("ANTHROPIC_API_KEY", "irrelevant") // must NOT rescue a bad mode
	exec := resolveAgentExecutor(newTestLogger(), nil)
	_, err := exec.Generate(context.Background(), "", "p")
	if err == nil || !strings.Contains(err.Error(), "bad_agent_exec_mode") {
		t.Fatalf("expected bad_agent_exec_mode error; got %v", err)
	}
}

// --- Provider selection ---

// TestProviderSelection covers the env → (kind, model, credential var)
// resolution table: auto-detection order, explicit provider override, and
// model / cred-env overrides.
func TestProviderSelection(t *testing.T) {
	cases := []struct {
		name        string
		env         map[string]string
		wantKind    string
		wantModel   string
		wantCredEnv string
		wantErr     string // substring of the resolution error; "" = success
	}{
		{
			name:        "anthropic auto-detected",
			env:         map[string]string{"ANTHROPIC_API_KEY": "k"},
			wantKind:    "anthropic",
			wantModel:   defaultAgentModels["anthropic"],
			wantCredEnv: "ANTHROPIC_API_KEY",
		},
		{
			name:        "openrouter auto-detected when anthropic absent",
			env:         map[string]string{"OPENROUTER_API_KEY": "k"},
			wantKind:    "openrouter",
			wantModel:   defaultAgentModels["openrouter"],
			wantCredEnv: "OPENROUTER_API_KEY",
		},
		{
			name:        "anthropic wins detection priority",
			env:         map[string]string{"OPENAI_API_KEY": "k", "ANTHROPIC_API_KEY": "k"},
			wantKind:    "anthropic",
			wantModel:   defaultAgentModels["anthropic"],
			wantCredEnv: "ANTHROPIC_API_KEY",
		},
		{
			name: "explicit provider override",
			env: map[string]string{
				agentProviderEnv: "openai", "OPENAI_API_KEY": "k", "ANTHROPIC_API_KEY": "k",
			},
			wantKind:    "openai",
			wantModel:   defaultAgentModels["openai"],
			wantCredEnv: "OPENAI_API_KEY",
		},
		{
			name: "model override",
			env: map[string]string{
				"ANTHROPIC_API_KEY": "k", agentModelEnv: "claude-haiku-4-5",
			},
			wantKind:    "anthropic",
			wantModel:   "claude-haiku-4-5",
			wantCredEnv: "ANTHROPIC_API_KEY",
		},
		{
			name: "explicit cred env var",
			env: map[string]string{
				agentProviderEnv: "anthropic", agentCredEnvEnv: "MY_GRANTED_KEY", "MY_GRANTED_KEY": "k",
			},
			wantKind:    "anthropic",
			wantModel:   defaultAgentModels["anthropic"],
			wantCredEnv: "MY_GRANTED_KEY",
		},
		{
			name:    "explicit provider with empty credential fails, named",
			env:     map[string]string{agentProviderEnv: "anthropic"},
			wantErr: "no_model_credential",
		},
		{
			name:    "unknown provider without cred env fails, named",
			env:     map[string]string{agentProviderEnv: "mystery"},
			wantErr: "no_model_credential",
		},
		{
			name:    "no credentials at all fails, named",
			env:     map[string]string{},
			wantErr: "no_model_credential",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clearAgentEnv(t)
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			exec, err := newLLMExecutor(nil, newTestLogger())
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("want error containing %q; got %v", tc.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("newLLMExecutor: %v", err)
			}
			if exec.kind != tc.wantKind || exec.model != tc.wantModel || exec.credEnv != tc.wantCredEnv {
				t.Fatalf("resolved (%s, %s, %s); want (%s, %s, %s)",
					exec.kind, exec.model, exec.credEnv, tc.wantKind, tc.wantModel, tc.wantCredEnv)
			}
		})
	}
}

// --- Real-mode generation (fake adapter, no network) ---

// TestGenerateWithFakeAdapter proves the real path end-to-end inside the
// process: env credential → registry pipeline → adapter → model text back,
// with the output NOT an echo of the prompt (Spec 058 US1).
func TestGenerateWithFakeAdapter(t *testing.T) {
	fake := &fakeAgentAdapter{text: "three files: a.go, b.go, c.md"}
	exec := newFakeLLMExecutor(t, fake)

	out, err := exec.Generate(context.Background(), "", "list the files in /workspace")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if out != fake.text {
		t.Fatalf("want model text %q; got %q", fake.text, out)
	}
	if out == "list the files in /workspace" {
		t.Fatalf("output must not echo the prompt")
	}
	if string(fake.gotCred) != "test-key-bytes" {
		t.Fatalf("adapter did not receive the env credential bytes")
	}
	if fake.gotReq.System != "" {
		t.Fatalf("default graph step must not set a system prompt; got %q", fake.gotReq.System)
	}
}

// TestGenerateCarriesPresetStepContext verifies a preset step's structural
// label reaches the model as system context, and the prompt stays user content.
func TestGenerateCarriesPresetStepContext(t *testing.T) {
	fake := &fakeAgentAdapter{text: "reviewed."}
	exec := newFakeLLMExecutor(t, fake)

	if _, err := exec.Generate(context.Background(), "review", "the plan output"); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if !strings.Contains(fake.gotReq.System, `"review"`) {
		t.Fatalf("system prompt must carry the step label; got %q", fake.gotReq.System)
	}
	if len(fake.gotReq.Messages) != 1 || fake.gotReq.Messages[0].Text() != "the plan output" {
		t.Fatalf("user message must carry the upstream input; got %+v", fake.gotReq.Messages)
	}
}

// TestGenerateCancellationAborts verifies FR-006: a cancelled context aborts
// an in-flight provider call promptly (well inside the 5 s budget) and
// Cancel() is propagated to the upstream stream.
func TestGenerateCancellationAborts(t *testing.T) {
	fake := &fakeAgentAdapter{text: "never delivered", block: true}
	exec := newFakeLLMExecutor(t, fake)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	done := make(chan error, 1)
	go func() {
		_, err := exec.Generate(ctx, "", "long prompt")
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("want context.Canceled; got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Generate did not return after cancellation")
	}
}

// --- Wire-level behaviour (:7881 protocol unchanged) ---

// startTestServerExec is startTestServer with an explicit agent executor,
// for tests that exercise non-stub execution over the wire.
func startTestServerExec(t *testing.T, exec agentExecutor) (func(), string) {
	t.Helper()
	srv, addr := startTestServerWith(t, "", exec)
	return func() { _ = srv.Close() }, addr
}

// TestStubModeEchoesOverWire pins the offline-CI contract: with the stub
// executor the task.running chunks echo the prompt and the task completes
// (Spec 058 SC-003 — kernel/ledger/wire paths still exercised offline).
func TestStubModeEchoesOverWire(t *testing.T) {
	srv, addr := startTestServer(t, "") // helper wires stubExecutor{}
	defer srv.Close()

	conn := dialAndAuth(t, addr, "")
	defer conn.Close()
	msgs, mu, stop := readMessages(t, conn)
	defer stop()

	sendMsg(t, conn, map[string]any{"kind": "task.start", "task_id": "t-echo", "prompt": "ping"})
	waitForKind(t, msgs, mu, "task.complete", 3*time.Second)

	mu.Lock()
	defer mu.Unlock()
	running := findKind(*msgs, "task.running")
	if running == nil {
		t.Fatalf("no task.running chunk; got %v", *msgs)
	}
	if running["text"] != "ping" {
		t.Fatalf("stub mode must echo the prompt; got %v", running["text"])
	}
}

// TestRealModeNoCredentialErrorsOverWire pins US3 AC-1 at the wire: a task
// dispatched to a real-mode process with no credential lands in the errored
// state with the named cause — it never completes with echoed text.
func TestRealModeNoCredentialErrorsOverWire(t *testing.T) {
	clearAgentEnv(t)
	closeSrv, addr := startTestServerExec(t, resolveAgentExecutor(newTestLogger(), nil))
	defer closeSrv()

	conn := dialAndAuth(t, addr, "")
	defer conn.Close()
	msgs, mu, stop := readMessages(t, conn)
	defer stop()

	sendMsg(t, conn, map[string]any{"kind": "task.start", "task_id": "t-nocred", "prompt": "do work"})
	errMsg := waitForKind(t, msgs, mu, "task.error", 3*time.Second)

	if errMsg["code"] != "graph_run_failed" {
		t.Fatalf("unexpected error code: %v", errMsg["code"])
	}
	if mt, _ := errMsg["message_truncated"].(string); !strings.HasPrefix(mt, "no_model_credential") {
		t.Fatalf("error must name the missing credential; got %q", mt)
	}
	mu.Lock()
	defer mu.Unlock()
	if findKind(*msgs, "task.complete") != nil {
		t.Fatalf("task must not complete without a credential (silent echo)")
	}
	if findKind(*msgs, "task.running") != nil {
		t.Fatalf("no output chunks expected on the no-credential path")
	}
}

// TestRealModeStreamsModelOutputOverWire drives a full task.start →
// task.running → task.complete cycle where the run node's chunk is MODEL
// output (fake adapter), not the echoed prompt — the C4 regression pin.
func TestRealModeStreamsModelOutputOverWire(t *testing.T) {
	fake := &fakeAgentAdapter{text: "the workspace contains a Go module"}
	exec := newFakeLLMExecutor(t, fake)
	closeSrv, addr := startTestServerExec(t, exec)
	defer closeSrv()

	conn := dialAndAuth(t, addr, "")
	defer conn.Close()
	msgs, mu, stop := readMessages(t, conn)
	defer stop()

	const prompt = "list the files in /workspace and summarize the project"
	sendMsg(t, conn, map[string]any{"kind": "task.start", "task_id": "t-real", "prompt": prompt})
	waitForKind(t, msgs, mu, "task.complete", 3*time.Second)

	mu.Lock()
	defer mu.Unlock()
	var texts []string
	for _, m := range *msgs {
		if m["kind"] == "task.running" {
			if s, _ := m["text"].(string); s != "" {
				texts = append(texts, s)
			}
		}
	}
	if len(texts) == 0 {
		t.Fatalf("no task.running chunks; got %v", *msgs)
	}
	// The run node's terminal chunk must be the model output, byte-unequal to
	// the prompt (Spec 058 SC-001).
	last := texts[len(texts)-1]
	if last != fake.text {
		t.Fatalf("want model output %q as the run chunk; got %q", fake.text, last)
	}
	if last == prompt {
		t.Fatalf("run chunk must not be the echoed prompt")
	}
}

// --- UNIT-1 policy guard tests (mission vm-execution-surface-truth-01PMZD14,
// HV-03 / 01PMZD13 V-2). Before this WP, cmd/harness-vm/agentexec.go's
// registry.Options literal left Policy unset, which registry.New silently
// substitutes with llm.AllowAllGuard{} — a guard that can never refuse. Every
// test below drives newLLMExecutor, the production construction path (AC-001:
// a test that hand-builds registry.Options{Policy: ...} itself cannot see a
// missing assignment, which is the defect this AC exists to catch).

// denyModelSelectEngine builds a real *cedar.Engine (never a private one in
// production code — see newLLMExecutor's doc comment and
// scripts/ci/check-cedar-engine-singleton.sh) whose only policy denies
// Action::"model_select" unconditionally, for every principal and resource.
func denyModelSelectEngine(t *testing.T) *cedar.Engine {
	t.Helper()
	e, err := cedar.NewEngine(cedar.Options{LoadFromDisk: false, IncludeEmbedded: false})
	if err != nil {
		t.Fatalf("cedar.NewEngine: %v", err)
	}
	const src = `forbid (principal, action == Action::"model_select", resource);`
	if err := e.SetPolicyText("deny-all.cedar", []byte(src)); err != nil {
		t.Fatalf("SetPolicyText: %v", err)
	}
	return e
}

// TestNewLLMExecutorPolicyGuardCanDeny is AC-002: with a Cedar policy that
// denies ActionModelSelect, a dispatched generation terminates with a
// policy-denial error, not a completed run.
//
// No adapter method other than RegisterAdapter is ever reached: the registry
// pipeline evaluates PolicyGuard.Allow (step 3) before it dials the adapter
// (step 4+, core/llm/registry/registry.go's documented pipeline order), so a
// fake adapter that would fail loudly if invoked is deliberately NOT wired —
// reaching it at all would already falsify this test.
func TestNewLLMExecutorPolicyGuardCanDeny(t *testing.T) {
	clearAgentEnv(t)
	t.Setenv("ANTHROPIC_API_KEY", "test-key-bytes")

	guard := cedar.NewLLMPolicyGuard(denyModelSelectEngine(t))
	exec, err := newLLMExecutor(guard, newTestLogger())
	if err != nil {
		t.Fatalf("newLLMExecutor: %v", err)
	}

	_, err = exec.Generate(context.Background(), "", "prompt")
	if err == nil {
		t.Fatal("expected a policy denial, got success")
	}
	var pd *llm.ErrPolicyDenied
	if !errors.As(err, &pd) {
		t.Fatalf("expected *llm.ErrPolicyDenied (the registry pipeline's policy-denial type); got %T: %v", err, err)
	}
	if pd.Reason == "" {
		t.Fatal("expected a non-empty denial reason")
	}
}

// TestNewLLMExecutorPolicyGuardAllowsWhenNotApplicable is AC-003: the
// permissive path is unchanged. A real Cedar engine with no matching policy
// returns NotApplicable, and the run completes exactly as it did before this
// WP (registry construction is DefaultDeny: false at the production call
// site — this test does not change that posture, it only proves the guard
// that can now refuse does not refuse everything).
func TestNewLLMExecutorPolicyGuardAllowsWhenNotApplicable(t *testing.T) {
	e, err := cedar.NewEngine(cedar.Options{IncludeEmbedded: true})
	if err != nil {
		t.Fatalf("cedar.NewEngine: %v", err)
	}
	guard := cedar.NewLLMPolicyGuard(e)
	fake := &fakeAgentAdapter{text: "model output under a real, non-denying policy"}
	exec := newFakeLLMExecutorWithGuard(t, fake, guard)

	out, err := exec.Generate(context.Background(), "", "prompt")
	if err != nil {
		t.Fatalf("expected success under a NotApplicable policy; got %v", err)
	}
	if out != fake.text {
		t.Fatalf("want %q; got %q", fake.text, out)
	}
}

// TestResolveCostReducerLoadsEmbeddedTable is AC-004, adapted to this tree's
// observed state: WP01 found no Cost: field on the registry.Options literal
// at the v0.66.0 merge base (model-settings-reach-the-model-01PMZ101 WP05's
// harness-vm half had not landed), so this WP absorbed that mission's own
// prescription rather than re-adding a field that was never there. This test
// pins that the absorption still resolves a usable reducer from the embedded
// starter table — the regression AC-004 exists to catch (a future edit
// silently dropping the Cost wiring) would show up here as a nil return.
func TestResolveCostReducerLoadsEmbeddedTable(t *testing.T) {
	r := resolveCostReducer(newTestLogger())
	if r == nil {
		t.Fatal("expected a non-nil CostReducer from the embedded starter cost table")
	}
}
