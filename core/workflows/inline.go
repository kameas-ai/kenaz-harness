// inline.go implements the InlineRun dispatch path.
//
// A workflow that declares `inline_run: true` in its YAML is surfaced
// in the chat composer's slash-command palette. When the user types
// `/wf <name>`, the chat router invokes InlineRun which streams the
// workflow's progress events back into the current chat session
// instead of spawning a new workflow_run session row. The two paths
// (inline vs spawned) differ only in transport: the underlying engine
// is identical.
//
// Validation: the loader (see schema.go) already rejects multi-step
// inline workflows in WP01 (ErrInlineMultiStep). InlineRun re-checks
// the invariant defensively so callers that build Workflow values in
// memory still hit the gate.
package workflows

import (
	"context"
	"errors"
	"fmt"
)

// inlineEventBuffer is the capacity of the channel returned by
// InlineRun. The engine emits at most 2 events per step (running +
// completed/failed); a buffer of 16 covers the single-step inline
// shape with margin to spare.
const inlineEventBuffer = 16

// InlineRun executes w through the standard Engine.Run path but
// returns the progress events on a channel tagged with Inline=true.
// The returned channel is closed once the run reaches a terminal
// state (completed / failed / interrupted).
//
// inputs is a `map[string]any` for caller convenience — strings,
// already-typed TypedValue values, or arbitrary JSON-shaped values
// are all accepted. The dispatcher coerces them to TypedValue before
// handing to the engine.
//
// Validation:
//   - w must satisfy Validate (this is also what Engine.Run runs).
//   - w.InlineRun must be true.
//   - w.Steps must be either single-step OR every step's runner must
//     advertise itself as safe-for-inline. The WP01 loader enforces
//     the single-step+model_turn shape; this guard catches in-memory
//     Workflow values that bypass the loader.
//
// Errors returned synchronously cover validation failures; runtime
// errors surface on the channel as ProgressEvent{Status: "failed"}
// before the channel closes.
func InlineRun(ctx context.Context, e *Engine, w Workflow, inputs map[string]any) (<-chan ProgressEvent, error) {
	if !w.InlineRun {
		return nil, fmt.Errorf("workflows: inline dispatch requires inline_run:true on %q", w.ID)
	}
	if err := Validate(w); err != nil {
		return nil, err
	}
	if err := assertInlineSafe(w); err != nil {
		return nil, err
	}
	if e == nil {
		e = NewEngine()
	}

	out := make(chan ProgressEvent, inlineEventBuffer)
	typed := coerceInlineInputs(inputs)

	go func() {
		defer close(out)
		opts := RunOptions{
			ProgressSink: func(ev ProgressEvent) {
				ev.Inline = true
				select {
				case out <- ev:
				case <-ctx.Done():
				}
			},
		}
		_, _ = e.Run(ctx, w, typed, opts)
	}()

	return out, nil
}

// assertInlineSafe enforces the runtime half of the inline_run gate.
// The loader already blocks multi-step inline workflows declared in
// YAML; this check covers in-memory Workflow values built in tests
// or programmatically.
//
// A multi-step inline workflow is permitted only when every step
// kind is on the inlineSafeKinds allowlist (read-only / pure step
// kinds). Today the allowlist is empty; the single-step path is the
// only legal shape.
func assertInlineSafe(w Workflow) error {
	if len(w.Steps) == 0 {
		return errors.New("workflows: inline_run requires at least one step")
	}
	if len(w.Steps) == 1 {
		// Single-step path is always permitted (matches WP01 invariant).
		return nil
	}
	for _, st := range w.Steps {
		if !inlineSafeKinds[st.Kind] {
			return fmt.Errorf("%w: kind %q not safe-for-inline",
				ErrInlineMultiStep, st.Kind)
		}
	}
	return nil
}

// inlineSafeKinds is the explicit allowlist of step kinds that are
// safe to chain inside an inline_run workflow. WP08 ships the list
// empty (single-step is the only legal shape) — the future
// "pure / read-only" tier (read_artifact, transform) lands once
// those runners declare their side-effect contract.
var inlineSafeKinds = map[StepKind]bool{}

// coerceInlineInputs converts a loose map[string]any into the
// TypedValue map the engine consumes. Recognised shapes:
//
//   - TypedValue: passed through.
//   - string: wrapped as {Type: text}.
//   - []byte: wrapped as {Type: bytes}.
//   - everything else: wrapped as {Type: json}.
//
// This is the inverse of HashInputs and lets the chat composer pass
// strings without per-call type tagging.
func coerceInlineInputs(in map[string]any) map[string]TypedValue {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]TypedValue, len(in))
	for k, v := range in {
		switch t := v.(type) {
		case TypedValue:
			out[k] = t
		case string:
			out[k] = TypedValue{Type: ValueTypeText, Text: t}
		case []byte:
			out[k] = TypedValue{Type: ValueTypeBytes, Bytes: t}
		default:
			out[k] = TypedValue{Type: ValueTypeJSON, JSON: v}
		}
	}
	return out
}
