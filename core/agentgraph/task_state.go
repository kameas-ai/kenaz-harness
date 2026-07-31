package agentgraph

import (
	"fmt"
	"sort"
	"strings"
	"sync"
)

// TaskState is the structured "what am I doing, what have I tried,
// what's off-limits" object the doc's Part 3 audit calls for
// (autonomy-recovery-runtime-01PMDL03 WP03) — the state RunState
// (executor.go) was missing: RunState is "transcript + completed-nodes
// map + last-error-per-node"; TaskState adds the current goal, a
// running completed-step summary, and a forbidden-actions set.
//
// The fourth dimension the doc asks for — []FailedAttempt{Node,
// Reason, Timestamp} — is deliberately NOT a field here. The kernel
// backtrack primitive (WP01) already appends a FailureAnnotation to
// RunState on every honored rewind (kernel.go AddFailureAnnotation);
// duplicating that into a second TaskState-owned slice would give the
// mission "two competing records" of the same rejection, one of which
// would inevitably drift. FailedAttempt is instead a type alias for
// FailureAnnotation (below), and TaskState's rendering
// (renderTaskState) reads RunState.FailureAnnotations() directly —
// single source of truth, two views.
//
// Every field is capped/summarised as it grows (spec risk: "TaskState
// re-injection must be compaction-exempt but token-bounded — a runaway
// failed-attempts list must be summarised, not appended forever") so
// this can safely ride on every compute re-entry's system prompt
// without the token cost growing unbounded over a long run.
//
// Safe for concurrent use; all methods tolerate a nil receiver
// (mirrors RunState.RecordToolCall's nil-safety) so an Env constructed
// without applyEnvDefaults degrades to "no task state" rather than
// panicking.
type TaskState struct {
	mu sync.RWMutex

	goal string

	// completedSteps is a bounded, human-readable trail of what has
	// been done so far this run. Capped at maxTaskCompletedSteps;
	// elidedCompleted counts entries dropped once the cap is hit so the
	// rendered summary can say "(+N earlier steps elided)" instead of
	// silently truncating with no indication anything was lost.
	completedSteps  []string
	elidedCompleted int

	forbidden map[string]bool
}

// maxTaskCompletedSteps bounds the stored completed-step trail. Once
// exceeded, the oldest entries are dropped (FIFO) and counted in
// elidedCompleted rather than kept forever.
const maxTaskCompletedSteps = 20

// NewTaskState returns an empty TaskState ready for use.
func NewTaskState() *TaskState {
	return &TaskState{forbidden: make(map[string]bool)}
}

// SetGoal replaces the run's current goal. Safe to call repeatedly
// (e.g. after a replan) — only the latest value is kept and rendered.
func (t *TaskState) SetGoal(goal string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	t.goal = goal
	t.mu.Unlock()
}

// Goal returns the current goal ("" if never set or t is nil).
func (t *TaskState) Goal() string {
	if t == nil {
		return ""
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.goal
}

// AddCompletedStep appends a human-readable step-completion summary
// line. Bounded at maxTaskCompletedSteps — see elidedCompleted.
func (t *TaskState) AddCompletedStep(summary string) {
	if t == nil || summary == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.completedSteps = append(t.completedSteps, summary)
	if over := len(t.completedSteps) - maxTaskCompletedSteps; over > 0 {
		t.elidedCompleted += over
		kept := make([]string, maxTaskCompletedSteps)
		copy(kept, t.completedSteps[over:])
		t.completedSteps = kept
	}
}

// CompletedSteps returns a snapshot of the retained completed-step
// trail plus the count of earlier entries that were dropped once the
// cap was hit (0 when nothing has been elided).
func (t *TaskState) CompletedSteps() (steps []string, elided int) {
	if t == nil {
		return nil, 0
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	out := make([]string, len(t.completedSteps))
	copy(out, t.completedSteps)
	return out, t.elidedCompleted
}

// AddForbidden merges one or more action names into the forbidden set
// (e.g. a tool the doom-loop guard or a human reviewer has ruled out
// for the rest of the run). Duplicates and empty strings are ignored.
func (t *TaskState) AddForbidden(actions ...string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, a := range actions {
		if a == "" {
			continue
		}
		t.forbidden[a] = true
	}
}

// ForbiddenActions returns the forbidden-action set as a sorted slice
// (deterministic rendering / testing).
func (t *TaskState) ForbiddenActions() []string {
	if t == nil {
		return nil
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	out := make([]string, 0, len(t.forbidden))
	for a := range t.forbidden {
		out = append(out, a)
	}
	sort.Strings(out)
	return out
}

// FailedAttempt is TaskState's view of a rejected approach, per the
// spec's `[]FailedAttempt{Node, Reason, Timestamp}` shape. It is a
// type alias for FailureAnnotation (kernel.go) rather than a second,
// independently-maintained struct: the kernel backtrack primitive
// already records {Node, Reason, RejectedApproach, Iteration,
// Timestamp} on RunState for every honored rewind, which is a strict
// superset of the fields the spec asks for here. Aliasing means there
// is exactly one record of "why an attempt was rejected", read two
// ways (kernel-internal FailureAnnotation bookkeeping vs. TaskState's
// rendered view).
type FailedAttempt = FailureAnnotation

// maxRenderedFailedAttempts bounds how many FailedAttempt records are
// rendered verbatim into the pinned TaskState block on every compute
// re-entry. Backtracks are already budget-capped (DefaultMaxBacktracksPerRun),
// so the underlying list is bounded in practice, but the render itself
// stays token-bounded independent of that budget: older entries are
// summarised into a single elided-count line rather than appended
// forever (spec risk: "a runaway failed-attempts list must be
// summarised, not appended forever — this rides on every model call").
const maxRenderedFailedAttempts = 5

// renderTaskState formats the accumulated TaskState (goal,
// completed-step summary, forbidden actions) plus the run's
// FailureAnnotations into a single pinned system-context block,
// re-injected ahead of every compute executor's model call via
// graphBaseOf (exec_compute.go). Returns "" when there is nothing to
// say — the common case for a run that never sets a goal, never
// backtracks, and never forbids an action — so the happy path composes
// byte-identically to the pre-WP03 SystemPrompt.
func renderTaskState(ts *TaskState, anns []FailureAnnotation) string {
	if ts == nil && len(anns) == 0 {
		return ""
	}

	var b strings.Builder

	if goal := ts.Goal(); goal != "" {
		fmt.Fprintf(&b, "Goal: %s\n", goal)
	}

	if steps, elided := ts.CompletedSteps(); len(steps) > 0 || elided > 0 {
		b.WriteString("Completed so far")
		if elided > 0 {
			fmt.Fprintf(&b, " (+%d earlier steps elided)", elided)
		}
		b.WriteString(": ")
		b.WriteString(strings.Join(steps, "; "))
		b.WriteString("\n")
	}

	if forbidden := ts.ForbiddenActions(); len(forbidden) > 0 {
		fmt.Fprintf(&b, "Forbidden actions (do not use): %s\n", strings.Join(forbidden, ", "))
	}

	if len(anns) > 0 {
		shown := anns
		elided := 0
		if len(anns) > maxRenderedFailedAttempts {
			elided = len(anns) - maxRenderedFailedAttempts
			shown = anns[elided:] // keep the most recent N, oldest-first within that window
		}
		if elided > 0 {
			fmt.Fprintf(&b, "(+%d earlier rejected attempts elided)\n", elided)
		}
		b.WriteString(renderFailureAnnotations(shown))
		b.WriteString("\n")
	}

	return strings.TrimRight(b.String(), "\n")
}
