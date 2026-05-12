// dry_run.go — DryRun fires a single hook against a synthetic payload and
// returns the HookOutput for preview in the Settings → Hooks UI.
//
// The caller supplies a hookID and a JSON-encoded payload. DryRun looks up
// the hook from the registry, resolves the synthetic payload as the event
// body, and dispatches it via the shell exec path (kind=shell) or returns a
// stub output (kind=builtin, kind=mcp) so the drawer shows something useful
// without executing real memory-store or MCP-tool operations.
//
// DryRunResult carries both the raw per-hook output and a MergedOutput so the
// drawer can show how the hook's decision would compose with peers.
package hooks

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"time"

	corehooks "github.com/sigil-tech/kaneaz-harness/core/hooks"
)

// HookOutput is the wire shape of core/hooks.HookOutput. JSON tags are
// camelCase to match the chassis-wide convention.
type HookOutput struct {
	Decision                 string          `json:"decision,omitempty"`
	Reason                   string          `json:"reason,omitempty"`
	AdditionalContext        string          `json:"additionalContext,omitempty"`
	UpdatedInput             json.RawMessage `json:"updatedInput,omitempty"`
	UpdatedMCPOutput         json.RawMessage `json:"updatedMCPOutput,omitempty"`
	PermissionDecision       string          `json:"permissionDecision,omitempty"`
	PermissionDecisionReason string          `json:"permissionDecisionReason,omitempty"`
	WatchPaths               []string        `json:"watchPaths,omitempty"`
	Async                    bool            `json:"async,omitempty"`
	AsyncTimeoutMs           int             `json:"asyncTimeoutMs,omitempty"`
}

// MergedOutput is the wire shape of core/hooks.MergedOutput.
type MergedOutput struct {
	Blocked           bool            `json:"blocked"`
	BlockReason       string          `json:"blockReason,omitempty"`
	AdditionalContext string          `json:"additionalContext,omitempty"`
	UpdatedInput      json.RawMessage `json:"updatedInput,omitempty"`
	UpdatedMCPOutput  json.RawMessage `json:"updatedMCPOutput,omitempty"`
	PermissionDenied  bool            `json:"permissionDenied"`
	PermissionAllowed bool            `json:"permissionAllowed"`
	PermissionReason  string          `json:"permissionReason,omitempty"`
	WatchPaths        []string        `json:"watchPaths,omitempty"`
}

// DryRunResult is the wire shape returned by DryRun.
type DryRunResult struct {
	// Output is the per-hook output from this single dry-run execution.
	Output HookOutput `json:"output"`
	// Merged is how a single-hook merge resolves (useful for the drawer's
	// decision summary when no peer hooks are bound).
	Merged MergedOutput `json:"merged"`
	// Stdout is the raw stdout captured from a shell hook. Empty for
	// builtin / mcp kinds.
	Stdout string `json:"stdout,omitempty"`
	// Stderr is the raw stderr captured from a shell hook.
	Stderr string `json:"stderr,omitempty"`
	// ExitCode is the process exit code from a shell hook. 0 for non-shell.
	ExitCode int `json:"exitCode"`
	// LatencyMs is the wall-clock duration of the dispatch in milliseconds.
	LatencyMs int64 `json:"latencyMs"`
}

// DryRun fires the hook identified by hookID against a synthetic payload
// encoded as a JSON object string. Returns the raw output + merged view.
func (a *API) DryRun(ctx context.Context, hookID string, syntheticPayload string) (DryRunResult, error) {
	if a == nil || a.reg == nil {
		return DryRunResult{}, ErrUnavailable
	}
	h, err := a.reg.Get(hookID)
	if err != nil {
		return DryRunResult{}, err
	}
	start := time.Now()
	result, dispatchErr := dispatchDryRun(ctx, h, syntheticPayload)
	result.LatencyMs = time.Since(start).Milliseconds()
	if dispatchErr != nil {
		// Surface dispatch errors as a synthetic "block" output so the UI
		// renders the error inline rather than crashing.
		result.Output = HookOutput{
			Decision: "block",
			Reason:   fmt.Sprintf("dry-run error: %v", dispatchErr),
		}
	}
	result.Merged = toWireMerged(corehooks.MergeOutputs([]corehooks.HookOutput{
		toCoreOutput(result.Output),
	}))
	return result, nil
}

// dispatchDryRun selects the dispatch path by hook kind.
func dispatchDryRun(ctx context.Context, h corehooks.Hook, payload string) (DryRunResult, error) {
	switch h.Kind {
	case corehooks.KindShell:
		return shellDryRun(ctx, h, payload)
	case corehooks.KindBuiltin:
		// Return a stub output so the drawer shows the hook identity without
		// executing real memory or side-effecting operations.
		return DryRunResult{
			Output: HookOutput{
				AdditionalContext: fmt.Sprintf(
					"[dry-run stub] builtin %q on event %q — no real execution in dry-run mode.",
					h.Builtin, h.Event,
				),
			},
		}, nil
	case corehooks.KindMCP:
		return DryRunResult{
			Output: HookOutput{
				AdditionalContext: fmt.Sprintf(
					"[dry-run stub] mcp tool %q on event %q — MCP dispatch is stubbed in dry-run mode.",
					h.MCPTool, h.Event,
				),
			},
		}, nil
	default:
		return DryRunResult{}, fmt.Errorf("unknown hook kind %q", h.Kind)
	}
}

// shellDryRun execs the shell hook command with the synthetic payload piped
// on stdin and captures stdout + stderr. The JSON output is decoded into a
// HookOutput following the same protocol as the runner.
func shellDryRun(ctx context.Context, h corehooks.Hook, payload string) (DryRunResult, error) {
	timeout := time.Duration(h.EffectiveTimeoutMs()) * time.Millisecond
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Wrap the payload in the standard {"input": ...} envelope.
	var rawPayload json.RawMessage
	if payload == "" {
		rawPayload = json.RawMessage("{}")
	} else {
		rawPayload = json.RawMessage(payload)
	}
	envelope := map[string]json.RawMessage{"input": rawPayload}
	body, err := json.Marshal(envelope)
	if err != nil {
		return DryRunResult{}, fmt.Errorf("marshal envelope: %w", err)
	}

	cmd := exec.CommandContext(cctx, "/bin/sh", "-c", h.Command)
	cmd.Stdin = bytes.NewReader(body)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.WaitDelay = 500 * time.Millisecond

	runErr := cmd.Run()
	exitCode := 0
	if runErr != nil {
		if exitErr, ok := runErr.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			return DryRunResult{
				Stdout:   stdout.String(),
				Stderr:   stderr.String(),
				ExitCode: 1,
			}, fmt.Errorf("run %q: %w", h.Command, runErr)
		}
	}

	var out HookOutput
	if stdout.Len() > 0 {
		if jsonErr := json.Unmarshal(stdout.Bytes(), &out); jsonErr != nil {
			// Non-fatal: show the raw stdout even when it's not valid JSON.
			return DryRunResult{
				Stdout:   stdout.String(),
				Stderr:   stderr.String(),
				ExitCode: exitCode,
			}, nil
		}
	}
	return DryRunResult{
		Output:   out,
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		ExitCode: exitCode,
	}, nil
}

// toWireMerged converts a core MergedOutput to the wire shape.
func toWireMerged(m corehooks.MergedOutput) MergedOutput {
	return MergedOutput{
		Blocked:           m.Blocked,
		BlockReason:       m.BlockReason,
		AdditionalContext: m.AdditionalContext,
		UpdatedInput:      m.UpdatedInput,
		UpdatedMCPOutput:  m.UpdatedMCPOutput,
		PermissionDenied:  m.PermissionDenied,
		PermissionAllowed: m.PermissionAllowed,
		PermissionReason:  m.PermissionReason,
		WatchPaths:        m.WatchPaths,
	}
}

// toCoreOutput converts a wire HookOutput back to a core HookOutput for
// merging. Only the fields that MergeOutputs consumes are populated.
func toCoreOutput(w HookOutput) corehooks.HookOutput {
	return corehooks.HookOutput{
		Decision:                 w.Decision,
		Reason:                   w.Reason,
		AdditionalContext:        w.AdditionalContext,
		UpdatedInput:             w.UpdatedInput,
		UpdatedMCPOutput:         w.UpdatedMCPOutput,
		PermissionDecision:       w.PermissionDecision,
		PermissionDecisionReason: w.PermissionDecisionReason,
		WatchPaths:               w.WatchPaths,
		Async:                    w.Async,
		AsyncTimeoutMs:           w.AsyncTimeoutMs,
	}
}

// Compile-time assertion: API satisfies the DryRun method contract.
var _ interface {
	DryRun(ctx context.Context, hookID string, syntheticPayload string) (DryRunResult, error)
} = (*API)(nil)
