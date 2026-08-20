// Package runposture carries the per-run "unattended" posture (mission
// model-scheduled-jobs-01PMSJ01 WP05, FR-003) across the call chain a
// scheduled chat run passes through: the dispatcher (core/rpc), the LLM
// view and ChatRunner (core/rpc/views/llm, core/rpc/views/agentgraph/
// chat), the kernel's tool dispatch (kernel_tool_adapter.go) and the
// Cedar interactive-prompt registry (core/policy/cedar).
//
// A context value, not a struct field, because the value has to survive
// ChatRunner.StartStream's own `context.WithCancel(context.Background())`
// re-derivation (the run outlives the inbound RPC call by design — see
// spec.md §2 leg 1) and reach call sites in packages that do not share a
// struct with the dispatcher or with each other. This package has no
// dependencies beyond the standard library so every consumer — including
// core/policy/cedar, which core/toolloop and core/rpc/views/agentgraph/
// chat both sit above — can import it with zero cycle risk.
//
// Every one of the three hazards this posture closes (spec.md §2 H-1/H-2/
// H-3) is a park-forever or stall-then-forget failure mode for a run
// nobody is watching. The posture's only job is to make each of those
// gates resolve to an immediate, audited/logged deny instead.
package runposture

import "context"

type ctxKey struct{}

// Unattended returns a derived context marking the run as unattended:
// nothing on the other end of an interactive prompt or confirmation for
// this run. Idempotent — marking an already-marked context is a no-op
// value-wise.
func Unattended(ctx context.Context) context.Context {
	return context.WithValue(ctx, ctxKey{}, true)
}

// IsUnattended reports whether ctx (or an ancestor) was marked Unattended.
// A nil ctx is not unattended (fail toward the existing interactive
// behaviour, never toward silently widening it).
func IsUnattended(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	v, _ := ctx.Value(ctxKey{}).(bool)
	return v
}
