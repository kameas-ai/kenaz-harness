package agentgraph

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
)

// stubLLM is a deterministic LLMProvider used across compute tests.
// Each call returns the configured Response (cycled through if a
// list is provided) so tests can drive iterative loops.
type stubLLM struct {
	mu        sync.Mutex
	calls     []LLMRequest
	responses []LLMResponse
	idx       int
	failOn    int
	failErr   error
}

func (s *stubLLM) Generate(_ context.Context, req LLMRequest) (LLMResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, req)
	if s.failErr != nil && len(s.calls) == s.failOn {
		return LLMResponse{}, s.failErr
	}
	if len(s.responses) == 0 {
		return LLMResponse{Content: "ok", FinishReason: "stop"}, nil
	}
	r := s.responses[s.idx%len(s.responses)]
	s.idx++
	return r, nil
}

func (s *stubLLM) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.calls)
}

// stubTools satisfies ToolRegistry with an in-memory map of named tools.
type stubTools struct {
	mu       sync.Mutex
	allowed  map[string]ToolResult
	denied   map[string]bool
	failures map[string]string
	calls    []ToolCall
}

func newStubTools() *stubTools {
	return &stubTools{allowed: map[string]ToolResult{}, denied: map[string]bool{}}
}

func (t *stubTools) allow(name, content string, isErr bool) {
	t.mu.Lock()
	t.allowed[name] = ToolResult{Content: content, IsError: isErr}
	t.mu.Unlock()
}

func (t *stubTools) deny(name string) {
	t.mu.Lock()
	t.denied[name] = true
	t.mu.Unlock()
}

// failWith makes Call return a genuine (non-nil) error with the given
// message for name, rather than a ToolResult with IsError set. This
// exercises the callErr wrapping path in exec_dispatch.go (as opposed to
// the allow(..., isErr=true) path, which returns a well-formed
// ToolResult straight through with no error). Used by WP02's
// environment-drift classifier tests
// (tool-error-legibility-01PMDL02).
func (t *stubTools) failWith(name, errMsg string) {
	t.mu.Lock()
	if t.failures == nil {
		t.failures = map[string]string{}
	}
	t.failures[name] = errMsg
	t.mu.Unlock()
}

func (t *stubTools) Has(name string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	_, ok := t.allowed[name]
	return ok
}

func (t *stubTools) Call(_ context.Context, c ToolCall) (ToolResult, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.calls = append(t.calls, c)
	if msg, ok := t.failures[c.Name]; ok {
		return ToolResult{}, errors.New(msg)
	}
	if t.denied[c.Name] {
		return ToolResult{}, errors.New("denied: " + c.Name)
	}
	r, ok := t.allowed[c.Name]
	if !ok {
		return ToolResult{}, errors.New("unknown tool: " + c.Name)
	}
	return r, nil
}

// stubMemory is an in-memory MemoryStore for tests.
type stubMemory struct {
	mu     sync.Mutex
	writes []MemoryWrite
	reads  []MemoryReadFilter
	hits   []MemoryHit
	dedup  map[string]string // contentHash → existing chunk_id
}

func newStubMemory() *stubMemory {
	return &stubMemory{dedup: make(map[string]string)}
}

func (s *stubMemory) Write(_ context.Context, w MemoryWrite) (string, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	hash := contentHash(w.Content)
	if id, ok := s.dedup[hash+":"+w.Scope]; ok {
		return id, true, nil
	}
	id := "chunk-" + hash[:8]
	s.dedup[hash+":"+w.Scope] = id
	s.writes = append(s.writes, w)
	return id, false, nil
}

func (s *stubMemory) Read(_ context.Context, f MemoryReadFilter) ([]MemoryHit, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reads = append(s.reads, f)
	return append([]MemoryHit(nil), s.hits...), nil
}

func (s *stubMemory) writeCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.writes)
}

func newTestEnv(g *Graph) *Env {
	env := &Env{
		RunID:     "test-run",
		SessionID: "test-session",
		Graph:     g,
	}
	applyEnvDefaults(env)
	return env
}

// ---- LLMNode ----

func TestLLMExecutor_BasicCall(t *testing.T) {
	t.Parallel()
	llm := &stubLLM{responses: []LLMResponse{{Content: "hi", TokensUsed: 10, FinishReason: "stop"}}}
	mem := newStubMemory()
	env := &Env{RunID: "r", SessionID: "s", LLM: llm, Memory: mem}
	applyEnvDefaults(env)
	ex := modelExecutor{}
	node := &Node{ID: "n1", Kind: NodeKindModel, Attrs: ModelAttrs{Model: "x", MaxTokens: 100}}
	r, err := ex.Execute(context.Background(), env, node, PortValues{
		"messages": []Message{{Role: "user", Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if r.Outputs["finish_reason"] != "stop" {
		t.Errorf("finish_reason = %v", r.Outputs["finish_reason"])
	}
	if env.Counters.LLMCallsMade != 1 || env.Counters.LLMTokensUsed != 10 {
		t.Errorf("counters: %+v", env.Counters)
	}
	// Two hook writes now: pre-llm (WP06 chat-migration) + post-llm.
	if mem.writeCount() != 2 {
		t.Errorf("expected 2 hook writes (pre+post-llm), got %d", mem.writeCount())
	}
	// Verify LLMCall + HookFired events.
	var sawLLMCall, sawHook bool
	for _, e := range r.Events.Events {
		switch e.Kind {
		case EventLLMCall:
			sawLLMCall = true
		case EventHookFired:
			sawHook = true
		}
	}
	if !sawLLMCall || !sawHook {
		t.Errorf("missing events; llm=%v hook=%v", sawLLMCall, sawHook)
	}
}

// TestLLMExecutor_CarriesExpandedKnobSurface is WP05 of
// model-request-path-live-01PMDL01: asserts modelExecutor.Execute
// threads the rest of the ModelAttrs knob surface (TopP, TopK,
// FrequencyPenalty, PresencePenalty, Seed, ParallelToolCalls,
// StopSequences) onto the LLMRequest seam handed to LLMProvider.Generate.
// Also covers WP06b (ReasoningBudgetTokens) and WP07 (FallbackChainId),
// which follow the same node-attr -> seam threading pattern.
func TestLLMExecutor_CarriesExpandedKnobSurface(t *testing.T) {
	t.Parallel()
	llm := &stubLLM{}
	env := &Env{RunID: "r", SessionID: "s", LLM: llm}
	applyEnvDefaults(env)
	ex := modelExecutor{}
	node := &Node{ID: "n1", Kind: NodeKindModel, Attrs: ModelAttrs{
		Model:                 "x",
		TopP:                  0.9,
		TopK:                  40,
		FrequencyPenalty:      0.25,
		PresencePenalty:       -0.1,
		Seed:                  12345,
		ParallelToolCalls:     true,
		StopSequences:         []string{"STOP", "END"},
		ReasoningBudgetTokens: 4096,
		FallbackChainId:       "anthropic-with-openrouter-fallback",
	}}
	if _, err := ex.Execute(context.Background(), env, node, PortValues{
		"messages": []Message{{Role: "user", Content: "hello"}},
	}); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	req, ok := llm.lastRequest()
	if !ok {
		t.Fatal("expected a captured LLMRequest")
	}
	if req.TopP == nil || *req.TopP != 0.9 {
		t.Errorf("TopP = %v, want 0.9", req.TopP)
	}
	if req.TopK == nil || *req.TopK != 40 {
		t.Errorf("TopK = %v, want 40", req.TopK)
	}
	if req.FrequencyPenalty == nil || *req.FrequencyPenalty != 0.25 {
		t.Errorf("FrequencyPenalty = %v, want 0.25", req.FrequencyPenalty)
	}
	if req.PresencePenalty == nil || *req.PresencePenalty != -0.1 {
		t.Errorf("PresencePenalty = %v, want -0.1", req.PresencePenalty)
	}
	if req.Seed == nil || *req.Seed != 12345 {
		t.Errorf("Seed = %v, want 12345", req.Seed)
	}
	if req.ParallelToolCalls == nil || !*req.ParallelToolCalls {
		t.Errorf("ParallelToolCalls = %v, want true", req.ParallelToolCalls)
	}
	if len(req.StopSequences) != 2 || req.StopSequences[0] != "STOP" || req.StopSequences[1] != "END" {
		t.Errorf("StopSequences = %#v, want [STOP END]", req.StopSequences)
	}
	if req.ReasoningBudgetTokens == nil || *req.ReasoningBudgetTokens != 4096 {
		t.Errorf("ReasoningBudgetTokens = %v, want 4096", req.ReasoningBudgetTokens)
	}
	if req.FallbackChainId != "anthropic-with-openrouter-fallback" {
		t.Errorf("FallbackChainId = %q, want anthropic-with-openrouter-fallback", req.FallbackChainId)
	}
}

// TestLLMExecutor_OmittedKnobsProduceNoOverride verifies the zero-value-
// means-unset convention: a node with none of the WP05 knobs set must
// produce nil pointers on the seam, not forced zero overrides that would
// clobber a provider/profile default.
func TestLLMExecutor_OmittedKnobsProduceNoOverride(t *testing.T) {
	t.Parallel()
	llm := &stubLLM{}
	env := &Env{RunID: "r", SessionID: "s", LLM: llm}
	applyEnvDefaults(env)
	ex := modelExecutor{}
	node := &Node{ID: "n1", Kind: NodeKindModel, Attrs: ModelAttrs{Model: "x"}}
	if _, err := ex.Execute(context.Background(), env, node, PortValues{
		"messages": []Message{{Role: "user", Content: "hello"}},
	}); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	req, ok := llm.lastRequest()
	if !ok {
		t.Fatal("expected a captured LLMRequest")
	}
	if req.TopP != nil || req.TopK != nil || req.FrequencyPenalty != nil ||
		req.PresencePenalty != nil || req.Seed != nil || req.ParallelToolCalls != nil {
		t.Errorf("expected all-nil knob pointers, got: TopP=%v TopK=%v FreqPenalty=%v PresPenalty=%v Seed=%v ParallelToolCalls=%v",
			req.TopP, req.TopK, req.FrequencyPenalty, req.PresencePenalty, req.Seed, req.ParallelToolCalls)
	}
	if len(req.StopSequences) != 0 {
		t.Errorf("StopSequences = %#v, want empty", req.StopSequences)
	}
	if req.ReasoningBudgetTokens != nil {
		t.Errorf("ReasoningBudgetTokens = %v, want nil", req.ReasoningBudgetTokens)
	}
	if req.FallbackChainId != "" {
		t.Errorf("FallbackChainId = %q, want empty", req.FallbackChainId)
	}
}

func TestLLMExecutor_BudgetCap(t *testing.T) {
	t.Parallel()
	llm := &stubLLM{}
	env := &Env{RunID: "r", LLM: llm, Budget: Budget{MaxLLMCallsPerRun: 1}}
	applyEnvDefaults(env)
	env.Counters.LLMCallsMade = 1 // pretend a previous call already happened
	ex := modelExecutor{}
	node := &Node{ID: "n", Kind: NodeKindModel, Attrs: ModelAttrs{Model: "x"}}
	if _, err := ex.Execute(context.Background(), env, node, nil); !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("err = %v, want ErrBudgetExceeded", err)
	}
}

func TestLLMExecutor_PropagatesProviderError(t *testing.T) {
	t.Parallel()
	llm := &stubLLM{failOn: 1, failErr: errors.New("boom")}
	env := &Env{RunID: "r", LLM: llm}
	applyEnvDefaults(env)
	ex := modelExecutor{}
	node := &Node{ID: "n", Kind: NodeKindModel, Attrs: ModelAttrs{Model: "x"}}
	if _, err := ex.Execute(context.Background(), env, node, nil); err == nil {
		t.Fatalf("expected error")
	}
}

// ---- ToolNode ----

func TestToolExecutor_Allowed(t *testing.T) {
	t.Parallel()
	tools := newStubTools()
	tools.allow("greet", "hello", false)
	mem := newStubMemory()
	env := &Env{RunID: "r", Tools: tools, Memory: mem}
	applyEnvDefaults(env)
	ex := toolExecutor{}
	node := &Node{ID: "t", Kind: NodeKindTool, Attrs: ToolAttrs{Name: "greet"}}
	r, err := ex.Execute(context.Background(), env, node, PortValues{
		"args": map[string]any{"who": "world"},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	tr := r.Outputs["result"].(ToolResult)
	if tr.Content != "hello" {
		t.Errorf("content = %q", tr.Content)
	}
	if env.Counters.ToolCallsMade != 1 {
		t.Errorf("ToolCallsMade = %d", env.Counters.ToolCallsMade)
	}
	// Two hook writes now: pre-tool (WP06 chat-migration) + post-tool.
	if mem.writeCount() != 2 {
		t.Errorf("expected 2 hook writes (pre+post-tool), got %d", mem.writeCount())
	}
}

func TestToolExecutor_UnknownTool(t *testing.T) {
	t.Parallel()
	tools := newStubTools()
	env := &Env{RunID: "r", Tools: tools}
	applyEnvDefaults(env)
	ex := toolExecutor{}
	node := &Node{ID: "t", Kind: NodeKindTool, Attrs: ToolAttrs{Name: "nope"}}
	if _, err := ex.Execute(context.Background(), env, node, nil); err == nil {
		t.Fatalf("expected error")
	}
}

func TestToolExecutor_DenyError(t *testing.T) {
	t.Parallel()
	tools := newStubTools()
	tools.allow("badtool", "", false)
	tools.deny("badtool")
	env := &Env{RunID: "r", Tools: tools}
	applyEnvDefaults(env)
	ex := toolExecutor{}
	node := &Node{ID: "t", Kind: NodeKindTool, Attrs: ToolAttrs{Name: "badtool"}}
	if _, err := ex.Execute(context.Background(), env, node, nil); err == nil {
		t.Fatalf("expected denied error")
	}
}

// ---- TransformNode ----

func TestTransformExecutor_Concat(t *testing.T) {
	t.Parallel()
	env := &Env{RunID: "r"}
	applyEnvDefaults(env)
	ex := transformExecutor{}
	node := &Node{ID: "x", Kind: NodeKindTransform, Attrs: TransformAttrs{
		Name: "concat", Params: map[string]any{"sep": "-"},
	}}
	r, err := ex.Execute(context.Background(), env, node, PortValues{
		"parts": []string{"a", "b", "c"},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if r.Outputs["out"] != "a-b-c" {
		t.Errorf("out = %v", r.Outputs["out"])
	}
}

func TestTransformExecutor_JSONExtract(t *testing.T) {
	t.Parallel()
	env := &Env{}
	applyEnvDefaults(env)
	ex := transformExecutor{}
	node := &Node{ID: "x", Kind: NodeKindTransform, Attrs: TransformAttrs{
		Name: "json_extract", Params: map[string]any{"path": "outer.inner"},
	}}
	r, err := ex.Execute(context.Background(), env, node, PortValues{
		"in": `{"outer":{"inner":"got it"}}`,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if r.Outputs["out"] != "got it" {
		t.Errorf("out = %v", r.Outputs["out"])
	}
}

func TestTransformExecutor_TruncateTokens(t *testing.T) {
	t.Parallel()
	env := &Env{}
	applyEnvDefaults(env)
	ex := transformExecutor{}
	node := &Node{ID: "x", Kind: NodeKindTransform, Attrs: TransformAttrs{
		Name: "truncate_tokens", Params: map[string]any{"max": 5},
	}}
	r, err := ex.Execute(context.Background(), env, node, PortValues{"in": "abcdefghij"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if r.Outputs["out"] != "abcde" {
		t.Errorf("out = %v", r.Outputs["out"])
	}
}

func TestTransformExecutor_Uppercase(t *testing.T) {
	t.Parallel()
	env := &Env{}
	applyEnvDefaults(env)
	ex := transformExecutor{}
	node := &Node{ID: "x", Kind: NodeKindTransform, Attrs: TransformAttrs{Name: "uppercase"}}
	r, err := ex.Execute(context.Background(), env, node, PortValues{"in": "hello"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if r.Outputs["out"] != "HELLO" {
		t.Errorf("out = %v", r.Outputs["out"])
	}
}

func TestTransformExecutor_UnknownTransform(t *testing.T) {
	t.Parallel()
	env := &Env{}
	applyEnvDefaults(env)
	ex := transformExecutor{}
	node := &Node{ID: "x", Kind: NodeKindTransform, Attrs: TransformAttrs{Name: "nope"}}
	if _, err := ex.Execute(context.Background(), env, node, nil); err == nil {
		t.Fatalf("expected error")
	}
}

// ---- ReflectNode ----

func TestReflectExecutor_ProducesCritique(t *testing.T) {
	t.Parallel()
	llm := &stubLLM{responses: []LLMResponse{{Content: "severity: medium\nsuggest: tighten phrasing"}}}
	env := &Env{RunID: "r", LLM: llm}
	applyEnvDefaults(env)
	ex := reflectExecutor{}
	node := &Node{ID: "ref", Kind: NodeKindReflect, Attrs: ReflectAttrs{MaxIterations: 3}}
	r, err := ex.Execute(context.Background(), env, node, PortValues{"draft": "draft text"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	c := r.Outputs["critique"].(map[string]any)
	if c["severity"] != "medium" {
		t.Errorf("severity = %v", c["severity"])
	}
}

func TestReflectExecutor_DefaultSeverityLow(t *testing.T) {
	t.Parallel()
	llm := &stubLLM{responses: []LLMResponse{{Content: "looks good"}}}
	env := &Env{RunID: "r", LLM: llm}
	applyEnvDefaults(env)
	ex := reflectExecutor{}
	node := &Node{ID: "ref", Kind: NodeKindReflect, Attrs: ReflectAttrs{}}
	r, err := ex.Execute(context.Background(), env, node, PortValues{"draft": "ok"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	c := r.Outputs["critique"].(map[string]any)
	if c["severity"] != "low" {
		t.Errorf("severity = %v", c["severity"])
	}
}

// ---- ReviewNode ----

func TestReviewExecutor_PassPath(t *testing.T) {
	t.Parallel()
	llm := &stubLLM{responses: []LLMResponse{{Content: "PASS"}}}
	env := &Env{RunID: "r", LLM: llm}
	applyEnvDefaults(env)
	ex := reviewExecutor{}
	node := &Node{ID: "rv", Kind: NodeKindReview, Attrs: ReviewAttrs{
		UpstreamNode: "x", MaxIterations: 3,
	}}
	r, err := ex.Execute(context.Background(), env, node, PortValues{"draft": "..."})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	v := r.Outputs["verdict"].(map[string]any)
	if v["verdict"] != "pass" {
		t.Errorf("verdict = %v", v["verdict"])
	}
	if r.Outputs["should_retry"] == true {
		t.Errorf("should_retry true on pass path")
	}
}

func TestReviewExecutor_CapHitWithHaltErrors(t *testing.T) {
	t.Parallel()
	llm := &stubLLM{responses: []LLMResponse{{Content: "FAIL"}}}
	env := &Env{RunID: "r", LLM: llm}
	applyEnvDefaults(env)
	ex := reviewExecutor{}
	node := &Node{ID: "rv", Kind: NodeKindReview, Attrs: ReviewAttrs{
		UpstreamNode: "x", MaxIterations: 1, OnCapHit: "halt",
	}}
	if _, err := ex.Execute(context.Background(), env, node, PortValues{"draft": "x"}); err == nil {
		t.Fatalf("expected cap-hit error")
	}
}

func TestReviewExecutor_CapHitWithEscalateAck(t *testing.T) {
	t.Parallel()
	llm := &stubLLM{responses: []LLMResponse{{Content: "FAIL"}}}
	env := &Env{RunID: "r", LLM: llm}
	applyEnvDefaults(env)
	ex := reviewExecutor{}
	node := &Node{ID: "rv", Kind: NodeKindReview, Attrs: ReviewAttrs{
		UpstreamNode: "x", MaxIterations: 1, OnCapHit: "escalate",
	}}
	r, err := ex.Execute(context.Background(), env, node, PortValues{"draft": "x"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if r.Outputs["escalated"] != true {
		t.Errorf("expected escalated=true")
	}
}

func TestReviewExecutor_FailRetryUnderCap(t *testing.T) {
	t.Parallel()
	llm := &stubLLM{responses: []LLMResponse{{Content: "FAIL"}}}
	env := &Env{RunID: "r", LLM: llm}
	applyEnvDefaults(env)
	ex := reviewExecutor{}
	node := &Node{ID: "rv", Kind: NodeKindReview, Attrs: ReviewAttrs{
		UpstreamNode: "x", MaxIterations: 3,
	}}
	r, err := ex.Execute(context.Background(), env, node, PortValues{"draft": "x"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if r.Outputs["should_retry"] != true || r.Outputs["retry_target"] != "x" {
		t.Errorf("retry signals wrong: %+v", r.Outputs)
	}
}

// ---- PlanNode ----

func TestPlanExecutor_ProducesPlan(t *testing.T) {
	t.Parallel()
	llm := &stubLLM{responses: []LLMResponse{{Content: "1. step\n2. step\n"}}}
	env := &Env{RunID: "r", LLM: llm}
	applyEnvDefaults(env)
	ex := plannerExecutor{}
	node := &Node{ID: "p", Kind: NodeKindPlanner, Attrs: PlannerAttrs{Verbosity: "terse"}}
	r, err := ex.Execute(context.Background(), env, node, PortValues{"task": "do something"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(r.Outputs["plan_text"].(string), "step") {
		t.Errorf("plan text wrong")
	}
	if r.Outputs["verbosity"] != "terse" {
		t.Errorf("verbosity = %v", r.Outputs["verbosity"])
	}
}

// ---- AskNode ----

func TestAskExecutor_PausesWhenNoAnswer(t *testing.T) {
	t.Parallel()
	bus := NewMemAskBus()
	env := &Env{RunID: "r", Ask: bus}
	applyEnvDefaults(env)
	ex := askExecutor{}
	node := &Node{ID: "ask", Kind: NodeKindAsk, Attrs: AskAttrs{Question: "Why?"}}
	r, err := ex.Execute(context.Background(), env, node, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !r.Pause {
		t.Errorf("expected pause")
	}
	if q, ok := bus.PendingQuestion("r", "ask"); !ok || q != "Why?" {
		t.Errorf("pending question wrong: %q (%v)", q, ok)
	}
}

func TestAskExecutor_ResumesWithAnswer(t *testing.T) {
	t.Parallel()
	bus := NewMemAskBus()
	bus.Answer("r", "ask", "because")
	env := &Env{RunID: "r", Ask: bus}
	applyEnvDefaults(env)
	ex := askExecutor{}
	node := &Node{ID: "ask", Kind: NodeKindAsk, Attrs: AskAttrs{Question: "Why?"}}
	r, err := ex.Execute(context.Background(), env, node, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if r.Pause {
		t.Errorf("should NOT pause when answer is present")
	}
	if r.Outputs["answer"] != "because" {
		t.Errorf("answer = %v", r.Outputs["answer"])
	}
}

// ---- EscalateNode ----

func TestEscalateExecutor_OneEscalationPerLeg(t *testing.T) {
	t.Parallel()
	llm := &stubLLM{responses: []LLMResponse{{Content: "improved"}, {Content: "improved2"}}}
	env := &Env{RunID: "r", LLM: llm}
	applyEnvDefaults(env)
	ex := escalateExecutor{}
	node := &Node{ID: "esc", Kind: NodeKindEscalate, Attrs: EscalateAttrs{
		TargetModel: "big", UpstreamNode: "src", OneEscalationOnly: true,
	}}
	if _, err := ex.Execute(context.Background(), env, node, PortValues{"trigger": "draft"}); err != nil {
		t.Fatalf("first: %v", err)
	}
	if _, err := ex.Execute(context.Background(), env, node, PortValues{"trigger": "draft"}); err == nil {
		t.Fatalf("expected second-call error")
	}
}

func TestEscalateExecutor_AllowsRepeatWhenFlagOff(t *testing.T) {
	t.Parallel()
	llm := &stubLLM{responses: []LLMResponse{{Content: "x"}}}
	env := &Env{RunID: "r", LLM: llm}
	applyEnvDefaults(env)
	ex := escalateExecutor{}
	node := &Node{ID: "esc", Kind: NodeKindEscalate, Attrs: EscalateAttrs{
		TargetModel: "big", UpstreamNode: "src",
	}}
	if _, err := ex.Execute(context.Background(), env, node, PortValues{"trigger": "x"}); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if _, err := ex.Execute(context.Background(), env, node, PortValues{"trigger": "x"}); err != nil {
		t.Fatalf("second call should succeed: %v", err)
	}
}

// ---- composePrompt (WP01: graph-level base system prompt) ----

func (s *stubLLM) lastRequest() (LLMRequest, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.calls) == 0 {
		return LLMRequest{}, false
	}
	return s.calls[len(s.calls)-1], true
}

func TestComposePrompt(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		parts []string
		want  string
	}{
		{"both parts, order preserved", []string{"BASE", "ROLE"}, "BASE\n\nROLE"},
		{"empty base dropped", []string{"", "ROLE"}, "ROLE"},
		{"empty role dropped", []string{"BASE", ""}, "BASE"},
		{"single part as-is", []string{"ONLY"}, "ONLY"},
		{"all empty", []string{"", "   ", "\n"}, ""},
		{"no parts", nil, ""},
		{"trims each part", []string{"  BASE  ", "\tROLE\n"}, "BASE\n\nROLE"},
		{"three parts", []string{"A", "B", "C"}, "A\n\nB\n\nC"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := composePrompt(tc.parts...); got != tc.want {
				t.Errorf("composePrompt(%q) = %q, want %q", tc.parts, got, tc.want)
			}
		})
	}
}

// TestModelExecutor_ComposesGraphBaseWithNodeRole asserts that a model
// node inside a graph whose SystemPrompt is "BASE" and whose own
// system_prompt attr is "ROLE" produces a request SystemPrompt of
// "BASE\n\nROLE".
func TestModelExecutor_ComposesGraphBaseWithNodeRole(t *testing.T) {
	t.Parallel()
	llm := &stubLLM{responses: []LLMResponse{{Content: "ok", FinishReason: "stop"}}}
	g := &Graph{ID: "g", SystemPrompt: "BASE"}
	env := &Env{RunID: "r", SessionID: "s", Graph: g, LLM: llm}
	applyEnvDefaults(env)
	ex := modelExecutor{}
	node := &Node{ID: "n", Kind: NodeKindModel, Attrs: ModelAttrs{Model: "x", SystemPrompt: "ROLE"}}
	if _, err := ex.Execute(context.Background(), env, node, nil); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	req, ok := llm.lastRequest()
	if !ok {
		t.Fatalf("no LLM request captured")
	}
	if req.SystemPrompt != "BASE\n\nROLE" {
		t.Errorf("SystemPrompt = %q, want %q", req.SystemPrompt, "BASE\n\nROLE")
	}
}

// TestModelExecutor_NoGraphBaseYieldsNodeRoleOnly confirms the
// no-behaviour-change path: an empty graph base yields just the node's
// own role prompt.
func TestModelExecutor_NoGraphBaseYieldsNodeRoleOnly(t *testing.T) {
	t.Parallel()
	llm := &stubLLM{responses: []LLMResponse{{Content: "ok", FinishReason: "stop"}}}
	g := &Graph{ID: "g"} // no SystemPrompt
	env := &Env{RunID: "r", SessionID: "s", Graph: g, LLM: llm}
	applyEnvDefaults(env)
	ex := modelExecutor{}
	node := &Node{ID: "n", Kind: NodeKindModel, Attrs: ModelAttrs{Model: "x", SystemPrompt: "ROLE"}}
	if _, err := ex.Execute(context.Background(), env, node, nil); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	req, ok := llm.lastRequest()
	if !ok {
		t.Fatalf("no LLM request captured")
	}
	if req.SystemPrompt != "ROLE" {
		t.Errorf("SystemPrompt = %q, want %q", req.SystemPrompt, "ROLE")
	}
}

// TestModelExecutor_TaskStateReinjectedOnEveryReEntry asserts the WP03
// contract: TaskState (goal / completed-step summary / forbidden
// actions) is re-injected as a pinned system-context block ahead of
// the node's own role prompt on *every* compute re-entry — not just
// once at graph start. Firing the same node twice with different
// TaskState content each time must produce a different SystemPrompt
// each time, dynamically re-grounding the model call.
func TestModelExecutor_TaskStateReinjectedOnEveryReEntry(t *testing.T) {
	t.Parallel()
	llm := &stubLLM{responses: []LLMResponse{
		{Content: "first", FinishReason: "stop"},
		{Content: "second", FinishReason: "stop"},
	}}
	g := &Graph{ID: "g", SystemPrompt: "BASE"}
	env := &Env{RunID: "r", SessionID: "s", Graph: g, LLM: llm}
	applyEnvDefaults(env)
	ex := modelExecutor{}
	node := &Node{ID: "n", Kind: NodeKindModel, Attrs: ModelAttrs{Model: "x", SystemPrompt: "ROLE"}}

	// First fire: no TaskState content set yet.
	if _, err := ex.Execute(context.Background(), env, node, nil); err != nil {
		t.Fatalf("Execute (1st): %v", err)
	}
	first, ok := llm.lastRequest()
	if !ok {
		t.Fatalf("no LLM request captured (1st)")
	}
	if strings.Contains(first.SystemPrompt, "write the report") {
		t.Errorf("1st SystemPrompt unexpectedly contains not-yet-set goal: %q", first.SystemPrompt)
	}

	// Mutate TaskState between fires (as a re-entry after a backtrack
	// or ladder rung would) and fire again.
	env.TaskState.SetGoal("write the report")
	env.TaskState.AddForbidden("delete_file")
	if _, err := ex.Execute(context.Background(), env, node, nil); err != nil {
		t.Fatalf("Execute (2nd): %v", err)
	}
	second, ok := llm.lastRequest()
	if !ok {
		t.Fatalf("no LLM request captured (2nd)")
	}
	if !strings.Contains(second.SystemPrompt, "write the report") ||
		!strings.Contains(second.SystemPrompt, "delete_file") {
		t.Errorf("2nd SystemPrompt = %q, want it to contain the freshly-set goal and forbidden action", second.SystemPrompt)
	}
	if !strings.Contains(second.SystemPrompt, "BASE") || !strings.Contains(second.SystemPrompt, "ROLE") {
		t.Errorf("2nd SystemPrompt = %q, want BASE and ROLE still present", second.SystemPrompt)
	}
}

// TestModelExecutor_ComposesFullTaskStateOrdering asserts goal,
// completed-step summary, forbidden actions, and failed attempts all
// land in the composed SystemPrompt in a stable order ahead of the
// node's own role prompt.
func TestModelExecutor_ComposesFullTaskStateOrdering(t *testing.T) {
	t.Parallel()
	llm := &stubLLM{responses: []LLMResponse{{Content: "ok", FinishReason: "stop"}}}
	g := &Graph{ID: "g", SystemPrompt: "BASE"}
	env := &Env{RunID: "r", SessionID: "s", Graph: g, LLM: llm}
	applyEnvDefaults(env)
	env.TaskState.SetGoal("GOALTEXT")
	env.TaskState.AddCompletedStep("STEPTEXT")
	env.TaskState.AddForbidden("FORBIDDENTEXT")
	env.State.AddFailureAnnotation(FailureAnnotation{Node: "draft", Reason: "ANNOTATIONTEXT", Iteration: 1})

	ex := modelExecutor{}
	node := &Node{ID: "n", Kind: NodeKindModel, Attrs: ModelAttrs{Model: "x", SystemPrompt: "ROLE"}}
	if _, err := ex.Execute(context.Background(), env, node, nil); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	req, ok := llm.lastRequest()
	if !ok {
		t.Fatalf("no LLM request captured")
	}
	sp := req.SystemPrompt
	idx := map[string]int{
		"BASE":           strings.Index(sp, "BASE"),
		"GOALTEXT":       strings.Index(sp, "GOALTEXT"),
		"STEPTEXT":       strings.Index(sp, "STEPTEXT"),
		"FORBIDDENTEXT":  strings.Index(sp, "FORBIDDENTEXT"),
		"ANNOTATIONTEXT": strings.Index(sp, "ANNOTATIONTEXT"),
		"ROLE":           strings.Index(sp, "ROLE"),
	}
	for k, v := range idx {
		if v < 0 {
			t.Fatalf("SystemPrompt missing %q: %q", k, sp)
		}
	}
	if !(idx["BASE"] < idx["GOALTEXT"] && idx["GOALTEXT"] < idx["STEPTEXT"] &&
		idx["STEPTEXT"] < idx["FORBIDDENTEXT"] && idx["FORBIDDENTEXT"] < idx["ANNOTATIONTEXT"] &&
		idx["ANNOTATIONTEXT"] < idx["ROLE"]) {
		t.Errorf("unexpected ordering in SystemPrompt: %q (indices: %v)", sp, idx)
	}
}

// ---- FailureAnnotations rendering (autonomy-recovery-runtime-01PMDL03 WP01) ----

func TestRenderFailureAnnotations(t *testing.T) {
	t.Parallel()
	if got := renderFailureAnnotations(nil); got != "" {
		t.Errorf("empty annotations = %q, want \"\"", got)
	}
	anns := []FailureAnnotation{
		{Node: "draft", Reason: "too vague", RejectedApproach: "v1 text", Iteration: 1},
		{Node: "draft", Reason: "still off-topic", Iteration: 2},
	}
	got := renderFailureAnnotations(anns)
	want := "Prior attempts were rejected and rewound — do not repeat them verbatim:\n" +
		"- [backtrack 1] node \"draft\" was rewound: too vague (rejected approach: v1 text)\n" +
		"- [backtrack 2] node \"draft\" was rewound: still off-topic"
	if got != want {
		t.Errorf("renderFailureAnnotations =\n%q\nwant\n%q", got, want)
	}
}

// TestModelExecutor_ComposesFailureAnnotationsIntoGraphBase asserts
// that once the kernel backtrack primitive has recorded a
// FailureAnnotation on env.State, the next compute-executor fire
// (modelExecutor here) folds the rendered annotation into the
// composed SystemPrompt ahead of the node's own role prompt — the
// re-fired node must see why its prior attempt was rejected, or it is
// free to repeat it verbatim.
func TestModelExecutor_ComposesFailureAnnotationsIntoGraphBase(t *testing.T) {
	t.Parallel()
	llm := &stubLLM{responses: []LLMResponse{{Content: "ok", FinishReason: "stop"}}}
	g := &Graph{ID: "g", SystemPrompt: "BASE"}
	env := &Env{RunID: "r", SessionID: "s", Graph: g, LLM: llm}
	applyEnvDefaults(env)
	env.State.AddFailureAnnotation(FailureAnnotation{
		Node: "draft", Reason: "rejected", RejectedApproach: "v1", Iteration: 1,
	})
	ex := modelExecutor{}
	node := &Node{ID: "n", Kind: NodeKindModel, Attrs: ModelAttrs{Model: "x", SystemPrompt: "ROLE"}}
	if _, err := ex.Execute(context.Background(), env, node, nil); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	req, ok := llm.lastRequest()
	if !ok {
		t.Fatalf("no LLM request captured")
	}
	if !strings.Contains(req.SystemPrompt, "BASE") ||
		!strings.Contains(req.SystemPrompt, "rejected") ||
		!strings.Contains(req.SystemPrompt, "ROLE") {
		t.Errorf("SystemPrompt = %q, want it to contain BASE, the rendered annotation, and ROLE", req.SystemPrompt)
	}
	// BASE / annotation / ROLE must appear in that order.
	base := strings.Index(req.SystemPrompt, "BASE")
	annIdx := strings.Index(req.SystemPrompt, "rejected")
	role := strings.Index(req.SystemPrompt, "ROLE")
	if !(base < annIdx && annIdx < role) {
		t.Errorf("expected BASE < annotation < ROLE ordering in %q", req.SystemPrompt)
	}
}
