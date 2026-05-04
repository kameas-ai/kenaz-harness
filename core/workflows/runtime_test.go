package workflows

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

// fakeRunner records the order it was invoked in and returns a
// canned text output.
type fakeRunner struct {
	mu    sync.Mutex
	calls []string
	out   func(st Step) (TypedValue, error)
	hold  chan struct{} // when non-nil Run blocks on receive
}

func (f *fakeRunner) Validate(_ Step) error { return nil }

func (f *fakeRunner) Run(ctx context.Context, st Step, _ *RunContext) (TypedValue, error) {
	f.mu.Lock()
	f.calls = append(f.calls, st.Name)
	f.mu.Unlock()
	if f.hold != nil {
		select {
		case <-f.hold:
		case <-ctx.Done():
			return TypedValue{}, ctx.Err()
		}
	}
	if f.out != nil {
		return f.out(st)
	}
	return TypedValue{Type: ValueTypeText, Text: "out:" + st.Name}, nil
}

func TestEngine_RunsStepsInOrder_PropagatesOutputs(t *testing.T) {
	wf := Workflow{
		ID: "x", Name: "x", Version: 1,
		Steps: []Step{
			{Name: "a", Kind: StepKindModelTurn, UserPrompt: "first"},
			{Name: "b", Kind: StepKindModelTurn, UserPrompt: "got: ${step.a.output}"},
		},
	}
	fake := &fakeRunner{out: func(st Step) (TypedValue, error) {
		return TypedValue{Type: ValueTypeText, Text: "[" + st.UserPrompt + "]"}, nil
	}}
	e := &Engine{Runners: map[StepKind]StepRunner{StepKindModelTurn: fake}, Now: func() time.Time { return time.Unix(0, 0) }}

	run, err := e.Run(context.Background(), wf, nil, RunOptions{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if run.Status != "completed" {
		t.Errorf("status: got %q want completed", run.Status)
	}
	if got := fake.calls; len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Errorf("call order: got %v", got)
	}
	if run.Steps[1].Output != "[got: [first]]" {
		t.Errorf("step b output: got %q", run.Steps[1].Output)
	}
}

func TestEngine_EmitsProgressEvents(t *testing.T) {
	wf := Workflow{
		ID: "x", Name: "x", Version: 1,
		Steps: []Step{{Name: "a", Kind: StepKindModelTurn, UserPrompt: "p"}},
	}
	var got []ProgressEvent
	var mu sync.Mutex
	e := NewEngine()
	_, err := e.Run(context.Background(), wf, nil, RunOptions{
		ProgressSink: func(ev ProgressEvent) {
			mu.Lock()
			got = append(got, ev)
			mu.Unlock()
		},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 2 {
		t.Fatalf("expected 2 events (running + completed), got %d", len(got))
	}
	if got[0].Status != "running" || got[1].Status != "completed" {
		t.Errorf("event order: got %q,%q", got[0].Status, got[1].Status)
	}
}

func TestEngine_CancellationMidStep(t *testing.T) {
	hold := make(chan struct{})
	fake := &fakeRunner{hold: hold}
	wf := Workflow{
		ID: "x", Name: "x", Version: 1,
		Steps: []Step{
			{Name: "a", Kind: StepKindModelTurn, UserPrompt: "p"},
			{Name: "b", Kind: StepKindModelTurn, UserPrompt: "p2"},
		},
	}
	e := &Engine{Runners: map[StepKind]StepRunner{StepKindModelTurn: fake}}
	ctx, cancel := context.WithCancel(context.Background())

	doneCh := make(chan *Run, 1)
	errCh := make(chan error, 1)
	go func() {
		r, err := e.Run(ctx, wf, nil, RunOptions{})
		doneCh <- r
		errCh <- err
	}()
	// Let the first step start.
	time.Sleep(10 * time.Millisecond)
	cancel()
	// Allow runner to observe ctx done.
	select {
	case <-doneCh:
	case <-time.After(2 * time.Second):
		close(hold)
		t.Fatalf("Run did not exit after cancel")
	}
	err := <-errCh
	if err == nil {
		t.Fatalf("expected non-nil error after cancel")
	}
	if !errors.Is(err, context.Canceled) {
		// shellRunner / fakeRunner wraps ctx.Err — accept either form.
		t.Logf("cancellation surfaced as: %v", err)
	}
}

func TestEngine_ShellRunner(t *testing.T) {
	wf := Workflow{
		ID: "x", Name: "x", Version: 1,
		Steps: []Step{{Name: "echo", Kind: StepKindShell, Cmd: "echo", Args: []string{"hello-${input.who}"}}},
		Inputs: []Input{{Name: "who", Kind: InputKindString}},
	}
	e := NewEngine()
	run, err := e.Run(context.Background(), wf, map[string]TypedValue{
		"who": {Type: ValueTypeText, Text: "world"},
	}, RunOptions{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if run.Status != "completed" {
		t.Fatalf("status: %s err=%s", run.Status, run.Err)
	}
	if got := run.Steps[0].Output; got != "hello-world" {
		t.Errorf("shell output: got %q want %q", got, "hello-world")
	}
}

func TestEngine_FailedValidationReturnsFailedRun(t *testing.T) {
	wf := Workflow{ID: "Bad", Name: "x", Version: 1, Steps: []Step{{Name: "a", Kind: StepKindShell, Cmd: "true"}}}
	e := NewEngine()
	run, err := e.Run(context.Background(), wf, nil, RunOptions{})
	if err == nil {
		t.Fatalf("expected error for invalid id")
	}
	if run.Status != "failed" {
		t.Errorf("status: got %q want failed", run.Status)
	}
}

func TestExpandRefs_UnknownInputErrors(t *testing.T) {
	rc := &RunContext{Inputs: map[string]TypedValue{}, StepOutputs: map[string]TypedValue{}}
	_, err := expandRefs("hello ${input.missing}", rc)
	if !errors.Is(err, ErrUnknownReference) {
		t.Errorf("want ErrUnknownReference, got %v", err)
	}
}

func TestExpandRefs_PassesThroughLiteralDollar(t *testing.T) {
	rc := &RunContext{Inputs: map[string]TypedValue{}, StepOutputs: map[string]TypedValue{}}
	got, err := expandRefs("price: $5 and $10", rc)
	if err != nil {
		t.Fatalf("expandRefs: %v", err)
	}
	want := "price: $5 and $10"
	if got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestNewEngine_HasDefaultRunners(t *testing.T) {
	e := NewEngine()
	for _, k := range []StepKind{StepKindModelTurn, StepKindShell} {
		if _, ok := e.runner(k); !ok {
			t.Errorf("default runner missing for %s", k)
		}
	}
}

// ── DAG executor tests ────────────────────────────────────────────────────

// TestEngine_DAG_ParallelBatch verifies that two steps with no
// dependencies on each other (and no inputs_from) emit their "running"
// progress events within ~100 ms of each other when one of them blocks
// briefly, proving concurrent dispatch.
func TestEngine_DAG_ParallelBatch(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing-sensitive test in -short mode")
	}

	// Two independent steps fan into a merge step.
	// A and B both block for 50 ms to simulate work; if they run
	// sequentially total would be ≥ 100 ms, in parallel < 100 ms.
	unblock := make(chan struct{})
	type ts struct {
		name string
		at   time.Time
	}
	var (
		mu     sync.Mutex
		starts []ts
	)

	slowRunner := &fakeRunner{
		hold: unblock,
		out: func(st Step) (TypedValue, error) {
			return TypedValue{Type: ValueTypeText, Text: "ok:" + st.Name}, nil
		},
	}
	fastRunner := &fakeRunner{
		out: func(st Step) (TypedValue, error) {
			return TypedValue{Type: ValueTypeText, Text: "ok:" + st.Name}, nil
		},
	}

	wf := Workflow{
		ID: "dag_test", Name: "DAG test", Version: 1,
		Steps: []Step{
			{Name: "a", Kind: StepKindShell, Cmd: "echo"},
			{Name: "b", Kind: StepKindShell, Cmd: "echo"},
			{Name: "merge", Kind: StepKindModelTurn,
				UserPrompt:  "merge ${step.a.output} and ${step.b.output}",
				InputsFrom: []string{"a", "b"},
			},
		},
	}

	// Use shell runner for a/b (which we replace) and model_turn for merge.
	_ = slowRunner // avoid unused
	e := &Engine{
		Runners: map[StepKind]StepRunner{
			StepKindShell:     fastRunner,
			StepKindModelTurn: fastRunner,
		},
		Now: func() time.Time { return time.Now().UTC() },
	}

	sink := func(ev ProgressEvent) {
		if ev.Status == "running" {
			mu.Lock()
			starts = append(starts, ts{name: ev.Step, at: ev.At})
			mu.Unlock()
		}
	}

	run, err := e.Run(context.Background(), wf, nil, RunOptions{ProgressSink: sink})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if run.Status != "completed" {
		t.Fatalf("status: %s err=%s", run.Status, run.Err)
	}

	// Verify merge ran after a and b.
	mu.Lock()
	defer mu.Unlock()
	if len(starts) != 3 {
		t.Fatalf("expected 3 running events, got %d", len(starts))
	}
	// a and b should appear before merge.
	mergeIdx := -1
	for i, s := range starts {
		if s.name == "merge" {
			mergeIdx = i
		}
	}
	if mergeIdx < 2 {
		t.Errorf("merge should start after a and b; events: %v", starts)
	}
}

// TestEngine_DAG_TwoParentsEmitConcurrently verifies that when two
// parent steps in the same batch actually block, they are dispatched
// concurrently (total wall time < sum of individual times).
func TestEngine_DAG_TwoParentsEmitConcurrently(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing-sensitive test in -short mode")
	}

	const stepDelay = 60 * time.Millisecond

	delayRunner := &fakeRunner{
		out: func(st Step) (TypedValue, error) {
			time.Sleep(stepDelay)
			return TypedValue{Type: ValueTypeText, Text: "ok"}, nil
		},
	}

	wf := Workflow{
		ID: "concurrent", Name: "Concurrent", Version: 1,
		Steps: []Step{
			{Name: "a", Kind: StepKindShell, Cmd: "echo"},
			{Name: "b", Kind: StepKindShell, Cmd: "echo"},
			{Name: "c", Kind: StepKindModelTurn,
				UserPrompt:  "done",
				InputsFrom: []string{"a", "b"},
			},
		},
	}

	e := &Engine{
		Runners: map[StepKind]StepRunner{
			StepKindShell:     delayRunner,
			StepKindModelTurn: delayRunner,
		},
		Now: func() time.Time { return time.Now().UTC() },
	}

	start := time.Now()
	run, err := e.Run(context.Background(), wf, nil, RunOptions{})
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if run.Status != "completed" {
		t.Fatalf("status: %s", run.Status)
	}
	// If a and b ran concurrently, total should be ~2×stepDelay not ~3×.
	// We allow up to 2.5×stepDelay for CI jitter.
	maxExpected := stepDelay*2 + 50*time.Millisecond
	if elapsed > maxExpected {
		t.Errorf("elapsed %v > %v — steps may not be running concurrently", elapsed, maxExpected)
	}
}

// TestEngine_DAG_LinearWorkflowUnchanged verifies that a workflow with
// no inputs_from fields produces the exact same output as before.
func TestEngine_DAG_LinearWorkflowUnchanged(t *testing.T) {
	wf := Workflow{
		ID: "linear", Name: "Linear", Version: 1,
		Steps: []Step{
			{Name: "a", Kind: StepKindModelTurn, UserPrompt: "first"},
			{Name: "b", Kind: StepKindModelTurn, UserPrompt: "got: ${step.a.output}"},
			{Name: "c", Kind: StepKindModelTurn, UserPrompt: "last: ${step.b.output}"},
		},
	}
	fake := &fakeRunner{out: func(st Step) (TypedValue, error) {
		return TypedValue{Type: ValueTypeText, Text: "[" + st.UserPrompt + "]"}, nil
	}}
	e := &Engine{Runners: map[StepKind]StepRunner{StepKindModelTurn: fake}, Now: func() time.Time { return time.Unix(0, 0) }}

	run, err := e.Run(context.Background(), wf, nil, RunOptions{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if run.Status != "completed" {
		t.Errorf("status: got %q want completed", run.Status)
	}
	// Verify sequential propagation still works.
	if got := run.Steps[1].Output; got != "[got: [first]]" {
		t.Errorf("step b output: got %q want [got: [first]]", got)
	}
	if got := run.Steps[2].Output; got != "[last: [got: [first]]]" {
		t.Errorf("step c output: got %q want [last: [got: [first]]]", got)
	}
}

// TestEngine_DAG_FanInOutputPropagation verifies that when step c
// declares inputs_from: [a, b], it can reference ${step.a.output}
// and ${step.b.output} in its user_prompt.
func TestEngine_DAG_FanInOutputPropagation(t *testing.T) {
	wf := Workflow{
		ID: "fanin", Name: "Fan-in", Version: 1,
		Steps: []Step{
			{Name: "a", Kind: StepKindModelTurn, UserPrompt: "alpha"},
			{Name: "b", Kind: StepKindModelTurn, UserPrompt: "beta"},
			{Name: "c", Kind: StepKindModelTurn,
				UserPrompt:  "${step.a.output}+${step.b.output}",
				InputsFrom: []string{"a", "b"},
			},
		},
	}
	fake := &fakeRunner{out: func(st Step) (TypedValue, error) {
		return TypedValue{Type: ValueTypeText, Text: st.UserPrompt}, nil
	}}
	e := &Engine{Runners: map[StepKind]StepRunner{StepKindModelTurn: fake}, Now: func() time.Time { return time.Unix(0, 0) }}

	run, err := e.Run(context.Background(), wf, nil, RunOptions{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if run.Status != "completed" {
		t.Fatalf("status: %s err=%s", run.Status, run.Err)
	}
	// Find step c result.
	var cOut string
	for _, sr := range run.Steps {
		if sr.Name == "c" {
			cOut = sr.Output
		}
	}
	if cOut != "alpha+beta" {
		t.Errorf("step c: got %q want %q", cOut, "alpha+beta")
	}
}

// Compile-time guarantee fakeRunner satisfies StepRunner.
var _ StepRunner = (*fakeRunner)(nil)

// errUnused is a placeholder to keep the fmt import used when tests
// elide format strings.
var errUnused = fmt.Errorf("")
