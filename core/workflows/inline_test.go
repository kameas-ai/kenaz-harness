package workflows

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestInlineRun_HappyPath drives a single-step inline_run:true
// workflow through InlineRun and asserts every emitted event carries
// Inline=true and arrives in the canonical running→completed order.
func TestInlineRun_HappyPath(t *testing.T) {
	t.Parallel()
	wf := Workflow{
		ID:        "inline-x",
		Name:      "inline x",
		Version:   1,
		InlineRun: true,
		Steps: []Step{
			{Name: "hello", Kind: StepKindModelTurn, UserPrompt: "hi ${input.who}"},
		},
		Inputs: []Input{{Name: "who", Kind: InputKindString}},
	}
	e := NewEngine()

	ch, err := InlineRun(context.Background(), e, wf, map[string]any{"who": "world"})
	if err != nil {
		t.Fatalf("InlineRun: %v", err)
	}
	var events []ProgressEvent
	for ev := range ch {
		events = append(events, ev)
	}
	if len(events) != 2 {
		t.Fatalf("events: got %d want 2 (running+completed)", len(events))
	}
	for i, ev := range events {
		if !ev.Inline {
			t.Errorf("event[%d].Inline = false, want true", i)
		}
		if ev.WorkflowID != "inline-x" {
			t.Errorf("event[%d].WorkflowID = %q", i, ev.WorkflowID)
		}
	}
	if events[0].Status != "running" || events[1].Status != "completed" {
		t.Errorf("transitions: %q,%q", events[0].Status, events[1].Status)
	}
}

// TestInlineRun_RejectsMultiStep verifies the loader-level guard fires
// for an in-memory Workflow that declares inline_run:true with more
// than one step. The loader (Validate) catches this and InlineRun
// surfaces ErrInlineMultiStep.
func TestInlineRun_RejectsMultiStep(t *testing.T) {
	t.Parallel()
	wf := Workflow{
		ID:        "inline-multi",
		Name:      "multi",
		Version:   1,
		InlineRun: true,
		Steps: []Step{
			{Name: "a", Kind: StepKindModelTurn, UserPrompt: "first"},
			{Name: "b", Kind: StepKindShell, Cmd: "echo", Args: []string{"second"}},
		},
	}
	_, err := InlineRun(context.Background(), NewEngine(), wf, nil)
	if !errors.Is(err, ErrInlineMultiStep) {
		t.Fatalf("want ErrInlineMultiStep, got %v", err)
	}
}

// TestInlineRun_AssertInlineSafe_DirectGuard covers the runtime guard
// directly: a multi-step workflow that bypasses the loader still fails
// at assertInlineSafe entry.
func TestInlineRun_AssertInlineSafe_DirectGuard(t *testing.T) {
	t.Parallel()
	wf := Workflow{
		ID:        "inline-direct",
		Name:      "direct",
		Version:   1,
		InlineRun: true,
		Steps: []Step{
			{Name: "a", Kind: StepKindModelTurn, UserPrompt: "first"},
			{Name: "b", Kind: StepKindModelTurn, UserPrompt: "second"},
		},
	}
	if err := assertInlineSafe(wf); err == nil {
		t.Fatal("assertInlineSafe accepted multi-step non-safe workflow")
	}
}

// TestInlineRun_RejectsNonInlineWorkflow asserts the dispatcher won't
// accept a workflow that lacks the inline_run:true declaration.
func TestInlineRun_RejectsNonInlineWorkflow(t *testing.T) {
	t.Parallel()
	wf := Workflow{
		ID:      "spawned-only",
		Name:    "x",
		Version: 1,
		Steps:   []Step{{Name: "a", Kind: StepKindModelTurn, UserPrompt: "hi"}},
	}
	_, err := InlineRun(context.Background(), NewEngine(), wf, nil)
	if err == nil {
		t.Fatal("InlineRun accepted non-inline workflow")
	}
}

// TestInlineRun_CoercesInputs covers the loose-typed input adapter:
// strings, []byte, TypedValue, and arbitrary JSON values all reach
// the engine in the right TypedValue shape.
func TestInlineRun_CoercesInputs(t *testing.T) {
	t.Parallel()
	got := coerceInlineInputs(map[string]any{
		"s": "text",
		"b": []byte{1, 2, 3},
		"t": TypedValue{Type: ValueTypeText, Text: "pre-typed"},
		"j": map[string]int{"k": 7},
	})
	if got["s"].Type != ValueTypeText || got["s"].Text != "text" {
		t.Errorf("string coercion: %+v", got["s"])
	}
	if got["b"].Type != ValueTypeBytes || len(got["b"].Bytes) != 3 {
		t.Errorf("bytes coercion: %+v", got["b"])
	}
	if got["t"].Type != ValueTypeText || got["t"].Text != "pre-typed" {
		t.Errorf("typedvalue passthrough: %+v", got["t"])
	}
	if got["j"].Type != ValueTypeJSON {
		t.Errorf("json coercion: %+v", got["j"])
	}
}

// TestInlineRun_ContextCancel ensures a cancel during the goroutine
// drains the channel cleanly without deadlocking the test.
func TestInlineRun_ContextCancel(t *testing.T) {
	t.Parallel()
	wf := Workflow{
		ID:        "inline-cancel",
		Name:      "x",
		Version:   1,
		InlineRun: true,
		Steps:     []Step{{Name: "a", Kind: StepKindModelTurn, UserPrompt: "hi"}},
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	ch, err := InlineRun(ctx, NewEngine(), wf, nil)
	if err != nil {
		t.Fatalf("InlineRun: %v", err)
	}
	timeout := time.After(2 * time.Second)
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				return
			}
		case <-timeout:
			t.Fatal("InlineRun channel never closed after cancel")
		}
	}
}
