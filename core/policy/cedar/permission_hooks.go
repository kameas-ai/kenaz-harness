// permission_hooks.go — PermissionHookRunner seam for
// hooks-event-surface-expansion-01KZNP3A WP06.
//
// When a PermissionHookRunner is wired onto the Registry, it fires
// permission_request before the interactive modal is raised. A hook
// returning permission_decision="allow" short-circuits the prompt;
// permission_decision="deny" short-circuits with a deny result.
//
// Cedar always has final-deny authority: a hook allow does NOT widen a
// Cedar explicit deny (that is enforced at the call site, which only
// calls RequestInteractive on NotApplicable).
package cedar

import "context"

// PermissionHookRunner fires permission lifecycle hooks. Defined here to
// keep core/policy/cedar self-contained (no import of core/hooks).
// Production wiring binds a *hooks.PermissionRunnerAdapter.
type PermissionHookRunner interface {
	// FirePermissionRequest fires permission_request before the modal is shown.
	// Returns allow=true to skip the modal, deny=true to auto-deny, and an
	// optional reason. Both false means "no opinion; continue normal flow."
	FirePermissionRequest(ctx context.Context, sessionID, actionKind, resource, argsSummary string) (allow, deny bool, reason string)

	// FirePermissionDenied fires permission_denied after a final Cedar deny.
	FirePermissionDenied(ctx context.Context, sessionID, actionKind, resource, reason string)
}

// WithPermissionHookRunner is a RegistryOption that wires a
// PermissionHookRunner onto the Registry.
func WithPermissionHookRunner(h PermissionHookRunner) RegistryOption {
	return func(r *Registry) {
		r.permHooks = h
	}
}
