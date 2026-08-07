// approvalgate.go — the ENGINE side of the :7881 approval surface (spec 074
// task 4.C2). approvals.go carries the wire; this file carries the thing that
// actually blocks.
//
// "The task blocks at an approval point" is, concretely, a goroutine parked in
// cedar.Registry.RequestInteractive with a deadline and exactly one exit. The
// registry already implements every semantic 4.C2 asks for — fail-closed deny
// on timeout, deny on ctx cancellation, first-decision-wins under a mutex plus
// sync.Once — so this file does not reimplement any of it. It does two things
// the registry cannot: it makes the gate REACHABLE from a task-path call site,
// and it binds an approval to the task_id that :7881 speaks.
//
// PAUSE IS NOT `paused`. Approval-pause is engine-internal, not durable, and
// not addressable — there is no id an operator can pause or resume against. It
// surfaces as `waiting_for_input` on kenaz.agent.run, which goes live with this
// change. RUN-DEBT-1's `paused` stays reserved and no pause/resume verb is
// added to any wire; conflating the two would put a Resume button in front of
// a state that has no resume.
package main

import (
	"context"

	"github.com/kameas-ai/kenaz-harness/core/policy/cedar"
)

// approvalGate is the ENGINE seam (task 4.C2): the one method a task-path call
// site needs to park on an approval. *cedar.Registry satisfies it via
// RequestInteractive, which is the goroutine park that "the task blocks at an
// approval point" actually means.
//
// It reaches call sites through the run context (withApprovalGate) rather than
// a parameter, because agentgraph transforms have a fixed
// func(ctx, PortValues, params) signature — the context is the only channel
// that reaches an arbitrary nested gate site, and a parameter would only reach
// the transforms declared in graphrun.go.
type approvalGate interface {
	RequestInteractive(ctx context.Context, surface cedar.PromptSurface) (cedar.Resolution, error)
}

type approvalGateKey struct{}

// withApprovalGate returns ctx carrying gate. A nil gate is stored as absent so
// approvalGateFrom's ok result is a truthful "is there a gate here".
func withApprovalGate(ctx context.Context, gate approvalGate) context.Context {
	if gate == nil {
		return ctx
	}
	return context.WithValue(ctx, approvalGateKey{}, gate)
}

// approvalGateFrom returns the run's approval gate, if the task path was wired
// with one. A call site that finds no gate MUST decide its own fail direction
// explicitly — this function deliberately does not synthesise a default,
// because "allow because nobody was listening" and "deny because nobody was
// listening" are both defensible and the choice belongs to the call site.
func approvalGateFrom(ctx context.Context) (approvalGate, bool) {
	g, ok := ctx.Value(approvalGateKey{}).(approvalGate)
	return g, ok && g != nil
}

// --- bridge methods: task binding and derived run status ---

// beginTask binds taskID to the bridge and returns the engine gate for the run
// plus an end func the task goroutine defers.
//
// The returned gate is `reg` itself: the task parks on the SAME registry the
// bridge listens to, which is what makes first-decision-wins hold across the
// served modal, the desktop panel, and a paired device without any of them
// knowing about each other.
//
// end() unbinds the task. It deliberately does NOT resolve approvals still
// pending: those are owned by the registry, and the task's own ctx
// cancellation is what denies them (source `cancelled`). Resolving here would
// be the second serializer this design forbids.
func (b *approvalBridge) beginTask(taskID string, reg approvalGate) (approvalGate, func()) {
	if b == nil {
		return reg, func() {}
	}
	b.mu.Lock()
	b.taskID = taskID
	b.mu.Unlock()
	return reg, func() {
		b.mu.Lock()
		if b.taskID == taskID {
			b.taskID = ""
		}
		b.mu.Unlock()
	}
}

// runStatus reports the kenaz.agent.run status this connection's task is in,
// derived purely from approval state: a run with an unresolved approval is
// `waiting_for_input`, and it returns to `running` on resolution.
//
// This is a DERIVED status, not a new wire field — the host computes the same
// value from the task.approval_requested / task.approval_resolved pair, and
// adding a status field would put a second, race-prone authority on the wire.
//
// It is emphatically NOT `paused`. Approval-pause is a parked goroutine with a
// deadline and exactly one exit: engine-internal, not durable, not addressable.
// RUN-DEBT-1's `paused` means an operator pausing a run at an arbitrary point
// and resuming later; the harness has a different mechanism for that
// (agentgraph AskBus + Kernel.Resume) and it is exposed nowhere on :7881.
// Conflating them would put a Resume button in front of a state that has no
// resume.
func (b *approvalBridge) runStatus() string {
	if b == nil {
		return "running"
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.taskID != "" && len(b.pending) > 0 {
		return "waiting_for_input"
	}
	return "running"
}
