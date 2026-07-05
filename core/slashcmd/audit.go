// audit.go — audit-emission helpers for user-defined slash commands.
//
// EmitRun is called by Dispatch.Run after every execution (success or
// failure). The audit.Emitter is optional; nil is a no-op.
//
// Privacy invariant (FR-006, DIRECTIVE_001):
//   - ArgNames carries only the NAMES of the supplied args, never their
//     resolved values.
//   - DispatchedTool is the tool identifier (e.g. "bash"), not the
//     rendered command string or any substituted arguments.
//   - Session / project IDs are stable opaque identifiers; no session
//     names, message bodies, or template bodies are included.
//
// user-slash-commands-01KQ8TD9 WP08.
package slashcmd

import (
	"context"
	"strings"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/context/audit"
)

// EmitRun emits a slashcmd.run audit event.
//
// cmd is the command that was dispatched. result is the RunResult
// returned by Dispatch.Run. runErr is the error returned by Run (nil
// on success). sc is the session context used for the dispatch.
//
// If em is nil the call is a silent no-op.
func EmitRun(
	ctx context.Context,
	em audit.Emitter,
	cmd UserCommand,
	result RunResult,
	runErr error,
	sc SessionContext,
) {
	if em == nil {
		return
	}

	// Collect arg names only — never values.
	argNames := make([]string, 0, len(sc.UserArgs))
	for k := range sc.UserArgs {
		argNames = append(argNames, k)
	}
	// Deterministic order (small list: insertion sort is fine).
	for i := 1; i < len(argNames); i++ {
		for j := i; j > 0 && argNames[j-1] > argNames[j]; j-- {
			argNames[j-1], argNames[j] = argNames[j], argNames[j-1]
		}
	}

	payload := audit.SlashCommandRunPayload{
		Name:           cmd.Name,
		Scope:          string(cmd.Scope),
		Kind:           string(cmd.Kind),
		ModelInvokable: cmd.ModelInvokable,
		ArgNames:       argNames,
		SessionID:      sc.SessionID,
		ProjectID:      sc.ProjectID,
		Success:        runErr == nil,
	}
	if cmd.Kind == KindTool {
		payload.DispatchedTool = cmd.Tool
	}
	if runErr != nil {
		payload.ErrorClass = classifyRunError(runErr)
	}
	audit.MustEmit(ctx, em, audit.KindSlashCommandRun, payload, time.Now().UTC())
}

// classifyRunError maps a run-time error onto a stable class label so
// the audit payload stays free of user-input bytes (FR-006).
func classifyRunError(err error) string {
	if err == nil {
		return ""
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "cancel") || strings.Contains(msg, "context"):
		return "context_canceled"
	case strings.Contains(msg, "timeout") || strings.Contains(msg, "deadline"):
		return "deadline_exceeded"
	case strings.Contains(msg, "not found") || strings.Contains(msg, "notfound"):
		return "command_not_found"
	case strings.Contains(msg, "validate") || strings.Contains(msg, "required"):
		return "validation_error"
	case strings.Contains(msg, "tool") && strings.Contains(msg, "dispatch"):
		return "tool_dispatch_error"
	default:
		return "run_error"
	}
}
