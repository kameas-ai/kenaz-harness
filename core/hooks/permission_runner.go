// permission_runner.go — PermissionRunnerAdapter wires core/hooks.Runner
// onto the cedar.PermissionHookRunner seam.
//
// The adapter implements cedar.PermissionHookRunner by importing
// core/policy/cedar. Production wiring injects this adapter into the
// cedar.Registry via WithPermissionHookRunner.
package hooks

import (
	"context"
	"log/slog"

	"github.com/sigil-tech/kaneaz-harness/core/policy/cedar"
)

// Compile-time assertion: PermissionRunnerAdapter implements
// cedar.PermissionHookRunner.
var _ cedar.PermissionHookRunner = (*PermissionRunnerAdapter)(nil)

// PermissionRunnerAdapter wraps a *Runner and implements
// cedar.PermissionHookRunner.
type PermissionRunnerAdapter struct {
	Runner *Runner
	Logger *slog.Logger
}

// FirePermissionRequest fires permission_request hooks.
// Returns (allow, deny, reason).
func (a *PermissionRunnerAdapter) FirePermissionRequest(
	ctx context.Context,
	sessionID, actionKind, resource, argsSummary string,
) (allow, deny bool, reason string) {
	if a == nil || a.Runner == nil {
		return false, false, ""
	}
	outputs, err := a.Runner.Fire(ctx, EventPermissionRequest, PermissionRequestEvent{
		SessionID:   sessionID,
		ActionKind:  actionKind,
		Resource:    resource,
		ArgsSummary: argsSummary,
	})
	if err != nil {
		logger := a.Logger
		if logger == nil {
			logger = slog.Default()
		}
		logger.Warn("hooks.permission_request.fire_failed",
			"session_id", sessionID, "action_kind", actionKind, "err", err.Error())
		return false, false, ""
	}
	merged := MergeOutputs(outputs)
	switch {
	case merged.PermissionDenied:
		return false, true, merged.PermissionReason
	case merged.PermissionAllowed:
		return true, false, merged.PermissionReason
	}
	return false, false, ""
}

// FirePermissionDenied fires permission_denied hooks (fire-and-forget).
func (a *PermissionRunnerAdapter) FirePermissionDenied(
	ctx context.Context,
	sessionID, actionKind, resource, reason string,
) {
	if a == nil || a.Runner == nil {
		return
	}
	a.Runner.FireAsync(ctx, EventPermissionDenied, PermissionDeniedEvent{
		SessionID:  sessionID,
		ActionKind: actionKind,
		Resource:   resource,
		Reason:     reason,
	})
}
