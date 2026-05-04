package workflows

import (
	"context"
	"errors"
	"testing"
)

// stubRunner returns a canned text output for any step. We use it
// instead of NewEngine() so the rerun tests don't depend on the
// shell/model_turn defaults' actual behaviour.
type rerunStubRunner struct{}

func (rerunStubRunner) Validate(_ Step) error { return nil }
func (rerunStubRunner) Run(_ context.Context, st Step, _ *RunContext) (TypedValue, error) {
	return TypedValue{Type: ValueTypeText, Text: "stub:" + st.Name}, nil
}

func newRerunEngine(c Cache) *Engine {
	return &Engine{
		Runners: map[StepKind]StepRunner{
			StepKindModelTurn: rerunStubRunner{},
			StepKindShell:     rerunStubRunner{},
		},
		Cache: c,
	}
}

func policyWorkflow(id, policy string) Workflow {
	return Workflow{
		ID:          id,
		Name:        "x",
		Version:     1,
		RerunPolicy: policy,
		Steps:       []Step{{Name: "step", Kind: StepKindModelTurn, UserPrompt: "hi"}},
	}
}

// TestNormalizeRerunPolicy verifies the alias mapping table.
func TestNormalizeRerunPolicy(t *testing.T) {
	t.Parallel()
	cases := map[string]RerunPolicy{
		"":         RerunPolicyAlways,
		"always":   RerunPolicyAlways,
		"fresh":    RerunPolicyAlways,
		"skip":     RerunPolicySkip,
		"continue": RerunPolicySkip,
		"prompt":   RerunPolicyPrompt,
		"ask":      RerunPolicyPrompt,
		"unknown":  RerunPolicyAlways,
	}
	for in, want := range cases {
		if got := NormalizeRerunPolicy(in); got != want {
			t.Errorf("NormalizeRerunPolicy(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestHashInputs_StableAcrossOrders proves the hash is independent
// of map iteration order.
func TestHashInputs_StableAcrossOrders(t *testing.T) {
	t.Parallel()
	a := HashInputs(map[string]any{"a": "1", "b": "2", "c": "3"})
	b := HashInputs(map[string]any{"c": "3", "a": "1", "b": "2"})
	if a != b {
		t.Errorf("hash differs across orders: %s vs %s", a, b)
	}
	if HashInputs(nil) == HashInputs(map[string]any{"a": "1"}) {
		t.Error("nil and non-empty inputs collide")
	}
}

// TestResolveRerun_NilCache returns (nil,false,nil) so the engine
// proceeds with a fresh dispatch.
func TestResolveRerun_NilCache(t *testing.T) {
	t.Parallel()
	cached, prompt, err := ResolveRerun(context.Background(), nil, "wf", nil, RerunPolicySkip)
	if err != nil || cached != nil || prompt {
		t.Errorf("nil cache: got (%v,%v,%v)", cached, prompt, err)
	}
}

// TestResolveRerun_PolicyAlways_IgnoresCache asserts the always
// policy never touches the cache.
func TestResolveRerun_PolicyAlways_IgnoresCache(t *testing.T) {
	t.Parallel()
	c := NewMemoryCache()
	_ = c.Put(context.Background(), "wf", HashInputs(nil), RunResult{Status: "completed"})
	cached, prompt, err := ResolveRerun(context.Background(), c, "wf", nil, RerunPolicyAlways)
	if err != nil || cached != nil || prompt {
		t.Errorf("always policy: got (%v,%v,%v)", cached, prompt, err)
	}
}

// TestResolveRerun_PolicySkip_HitReturnsCached covers the cache-hit
// path under policy=skip.
func TestResolveRerun_PolicySkip_HitReturnsCached(t *testing.T) {
	t.Parallel()
	c := NewMemoryCache()
	want := RunResult{RunID: "r1", WorkflowID: "wf", Status: "completed"}
	if err := c.Put(context.Background(), "wf", HashInputs(nil), want); err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, prompt, err := ResolveRerun(context.Background(), c, "wf", nil, RerunPolicySkip)
	if err != nil || prompt {
		t.Fatalf("skip hit: err=%v prompt=%v", err, prompt)
	}
	if got == nil || got.RunID != "r1" {
		t.Errorf("cached miss: %+v", got)
	}
}

// TestResolveRerun_PolicySkip_Miss returns (nil, false, nil).
func TestResolveRerun_PolicySkip_Miss(t *testing.T) {
	t.Parallel()
	c := NewMemoryCache()
	got, prompt, err := ResolveRerun(context.Background(), c, "wf", nil, RerunPolicySkip)
	if err != nil || prompt || got != nil {
		t.Errorf("skip miss: got (%v,%v,%v)", got, prompt, err)
	}
}

// TestResolveRerun_PolicyPrompt_HitSurfacesError covers the gating
// behaviour of policy=prompt when a cache row exists.
func TestResolveRerun_PolicyPrompt_HitSurfacesError(t *testing.T) {
	t.Parallel()
	c := NewMemoryCache()
	if err := c.Put(context.Background(), "wf", HashInputs(nil),
		RunResult{Status: "completed"}); err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, prompt, err := ResolveRerun(context.Background(), c, "wf", nil, RerunPolicyPrompt)
	if got != nil {
		t.Errorf("prompt hit returned cached: %+v", got)
	}
	if !prompt {
		t.Error("prompt flag should be true")
	}
	if !errors.Is(err, ErrRerunPolicyAsk) {
		t.Errorf("err: want ErrRerunPolicyAsk, got %v", err)
	}
}

// TestResolveRerun_PolicyPrompt_Miss returns (nil, false, nil) so the
// engine proceeds with a fresh dispatch.
func TestResolveRerun_PolicyPrompt_Miss(t *testing.T) {
	t.Parallel()
	c := NewMemoryCache()
	got, prompt, err := ResolveRerun(context.Background(), c, "wf", nil, RerunPolicyPrompt)
	if got != nil || prompt || err != nil {
		t.Errorf("prompt miss: got (%v,%v,%v)", got, prompt, err)
	}
}

// TestEngine_RerunPolicy_AlwaysDispatches verifies the wired-into-
// runtime path: even if the cache is populated, policy=always
// re-runs every step.
func TestEngine_RerunPolicy_AlwaysDispatches(t *testing.T) {
	t.Parallel()
	c := NewMemoryCache()
	_ = c.Put(context.Background(), "wf-always", HashInputs(nil),
		RunResult{Status: "completed", Steps: []StepResult{{Name: "cached", Status: "completed", Output: "from-cache"}}})
	e := newRerunEngine(c)
	wf := policyWorkflow("wf-always", "always")
	run, err := e.Run(context.Background(), wf, nil, RunOptions{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if run.Steps[0].Output != "stub:step" {
		t.Errorf("expected fresh dispatch, got cached output %q", run.Steps[0].Output)
	}
}

// TestEngine_RerunPolicy_SkipReturnsCached verifies the wired-into-
// runtime cache-hit path: policy=skip returns cached steps.
func TestEngine_RerunPolicy_SkipReturnsCached(t *testing.T) {
	t.Parallel()
	c := NewMemoryCache()
	_ = c.Put(context.Background(), "wf-skip", HashInputs(nil),
		RunResult{Status: "completed", Steps: []StepResult{{Name: "cached", Status: "completed", Output: "from-cache"}}})
	e := newRerunEngine(c)
	wf := policyWorkflow("wf-skip", "skip")
	run, err := e.Run(context.Background(), wf, nil, RunOptions{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(run.Steps) != 1 || run.Steps[0].Output != "from-cache" {
		t.Errorf("expected cached output, got %+v", run.Steps)
	}
}

// TestEngine_RerunPolicy_PromptSurfacesAskError verifies the
// prompt+hit gate at the runtime level.
func TestEngine_RerunPolicy_PromptSurfacesAskError(t *testing.T) {
	t.Parallel()
	c := NewMemoryCache()
	_ = c.Put(context.Background(), "wf-prompt", HashInputs(nil),
		RunResult{Status: "completed"})
	e := newRerunEngine(c)
	wf := policyWorkflow("wf-prompt", "prompt")
	run, err := e.Run(context.Background(), wf, nil, RunOptions{})
	if !errors.Is(err, ErrRerunPolicyAsk) {
		t.Fatalf("want ErrRerunPolicyAsk, got %v", err)
	}
	if run.Status != "needs_confirmation" {
		t.Errorf("status: got %q want needs_confirmation", run.Status)
	}
}

// TestEngine_RerunPolicy_SkipCacheBypassesGate verifies SkipCache=true
// bypasses the prompt gate so a confirmed re-run dispatches fresh.
func TestEngine_RerunPolicy_SkipCacheBypassesGate(t *testing.T) {
	t.Parallel()
	c := NewMemoryCache()
	_ = c.Put(context.Background(), "wf-skipcache", HashInputs(nil),
		RunResult{Status: "completed"})
	e := newRerunEngine(c)
	wf := policyWorkflow("wf-skipcache", "prompt")
	run, err := e.Run(context.Background(), wf, nil, RunOptions{SkipCache: true})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if run.Status != "completed" {
		t.Errorf("status: got %q want completed", run.Status)
	}
	if run.Steps[0].Output != "stub:step" {
		t.Errorf("expected fresh dispatch under SkipCache, got %q", run.Steps[0].Output)
	}
}

// TestEngine_RerunPolicy_WriteBack confirms a successful dispatch
// updates the cache so the next call hits.
func TestEngine_RerunPolicy_WriteBack(t *testing.T) {
	t.Parallel()
	c := NewMemoryCache()
	e := newRerunEngine(c)
	wf := policyWorkflow("wf-writeback", "skip")

	// First run: cache miss, dispatches fresh, writes back.
	first, err := e.Run(context.Background(), wf, nil, RunOptions{})
	if err != nil {
		t.Fatalf("first Run: %v", err)
	}
	if first.Steps[0].Output != "stub:step" {
		t.Errorf("first run: got %q", first.Steps[0].Output)
	}
	cached, _ := c.Get(context.Background(), "wf-writeback", HashInputs(nil))
	if cached == nil {
		t.Fatal("cache not populated after successful run")
	}

	// Second run: cache hit — output should match the write-back.
	second, err := e.Run(context.Background(), wf, nil, RunOptions{})
	if err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if len(second.Steps) != 1 || second.Steps[0].Output != "stub:step" {
		t.Errorf("second run did not return cached steps: %+v", second.Steps)
	}
}
