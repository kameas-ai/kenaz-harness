// Package sleep implements the kenaz__sleep builtin tool
// (builtin-tools-search-and-elicitation-01KZNP3D, WP04).
//
// kenaz__sleep lets the model intentionally yield a turn without burning
// token budget or consuming a KnobMaxIterations slot. The canonical use
// case is "I need to wait N seconds before polling the next result" in
// __monitor watch patterns.
//
// Spec requirements (FR-009 .. FR-011):
//
//   - Input:  { "seconds": int }  — clamped to [1, 600].
//   - Output: { "slept_s": int }
//   - Does NOT count against KnobMaxIterations. The toolloop's IterGate
//     recognises kenaz__sleep by its constant tool name and skips the
//     iteration increment (see core/toolloop/loop.go).
//   - Cedar action: tool.passive. Default-allow; never gated by a Settings
//     toggle because this is a passive no-side-effect primitive.
//
// Sleep is interruptible: if the caller's context is cancelled before the
// timer fires, the sleep returns the context error immediately. This
// satisfies the WP04 acceptance criterion "session abort interrupts sleep
// within 100ms".
//
// DIRECTIVE_001: backend-only; no CGo, no GUI imports.
package sleep

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

const (
	// ToolName is the namespaced identifier surfaced to the model.
	// The "kenaz__" prefix is reserved for builtins.
	ToolName = "kenaz__sleep"

	// ToolDescription is the user-facing description sent to the model.
	ToolDescription = "Yield for the specified number of seconds without consuming an iteration slot. " +
		"Use this when you need to wait before polling an async result or external resource. " +
		"Seconds is clamped to [1, 600]."

	// MinSeconds is the lower bound on the sleep duration.
	MinSeconds = 1

	// MaxSeconds is the upper bound on the sleep duration (10 minutes).
	MaxSeconds = 600
)

// inputSchema is the JSON Schema for kenaz__sleep's argument shape.
const inputSchema = `{
  "type": "object",
  "properties": {
    "seconds": {
      "type": "integer",
      "description": "Number of seconds to sleep. Clamped to [1, 600].",
      "minimum": 1,
      "maximum": 600
    }
  },
  "required": ["seconds"]
}`

// sleepArgs is the wire shape parsed from the model's args.
type sleepArgs struct {
	Seconds int `json:"seconds"`
}

// sleepResult is the JSON returned to the model after a successful sleep.
type sleepResult struct {
	SleptS int `json:"slept_s"`
}

// errorResult is the JSON returned to the model when args are invalid.
type errorResult struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}

// Tool implements the kenaz__sleep builtin. Safe for concurrent use;
// zero-value is valid after construction via New.
type Tool struct{}

// New constructs a Tool. There are currently no options (the tool is
// stateless), but accepting an Options struct future-proofs the
// constructor signature against additions (e.g. an OTel tracer).
func New() *Tool { return &Tool{} }

// Name returns the namespaced tool identifier.
func (t *Tool) Name() string { return ToolName }

// Description returns the user-facing description.
func (t *Tool) Description() string { return ToolDescription }

// InputSchema returns the JSON Schema for the tool's args.
func (t *Tool) InputSchema() json.RawMessage { return json.RawMessage(inputSchema) }

// Call executes a sleep invocation: parse args → clamp → wait → return.
//
// The Go-error return is reserved for "couldn't produce a tool result" (e.g.
// JSON marshal failure). Validation failures are returned as IsError=false
// JSON payloads so the model can self-correct its next call.
func (t *Tool) Call(ctx context.Context, rawArgs json.RawMessage) (json.RawMessage, error) {
	var args sleepArgs
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return marshalError("invalid_args",
			fmt.Sprintf("could not parse sleep args: %v", err))
	}

	// Clamp to [MinSeconds, MaxSeconds].
	secs := args.Seconds
	if secs < MinSeconds {
		secs = MinSeconds
	}
	if secs > MaxSeconds {
		secs = MaxSeconds
	}

	timer := time.NewTimer(time.Duration(secs) * time.Second)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		// Session abort or context cancellation — stop early. Return
		// a result reflecting how long we actually waited (zero, from
		// the model's perspective we were interrupted immediately).
		return marshalResult(0)
	case <-timer.C:
		return marshalResult(secs)
	}
}

// marshalResult marshals a successful sleep result.
func marshalResult(sleptS int) (json.RawMessage, error) {
	b, err := json.Marshal(sleepResult{SleptS: sleptS})
	if err != nil {
		return nil, fmt.Errorf("sleep: marshal result: %w", err)
	}
	return b, nil
}

// marshalError marshals a structured error result. The error kind and
// message mirror the save_artifact convention so model-side error handling
// stays uniform across builtins.
func marshalError(kind, msg string) (json.RawMessage, error) {
	b, err := json.Marshal(errorResult{Error: kind, Message: msg})
	if err != nil {
		return nil, fmt.Errorf("sleep: marshal error result: %w", err)
	}
	return b, nil
}
