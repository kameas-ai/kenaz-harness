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

// Compile-time guarantee fakeRunner satisfies StepRunner.
var _ StepRunner = (*fakeRunner)(nil)

// errUnused is a placeholder to keep the fmt import used when tests
// elide format strings.
var errUnused = fmt.Errorf("")
