// Iteration accounting: does dispatching this tool cost the caller an
// iteration? (WP04 — kenaz__sleep.)
//
// FR-010 requires that kenaz__sleep does NOT count against
// KnobMaxIterations. Iteration itself belongs to the kernel — the
// `agent_loop` node in chat_default.yaml is what actually repeats, and
// core/agentgraph's toolDispatchExecutor is what charges the budget.
// This file owns only the POLICY question that dispatch path asks
// before charging: is this tool passive?
//
// # Surface
//
//  1. IsPassiveTool(name) — true for tools whose Cedar action is
//     tool.passive (currently kenaz__sleep only). agentgraph's
//     toolDispatchExecutor consults this before calling
//     env.Counters.AddTool(), so passive tools never increment the
//     run-wide ToolCallsMade counter (which maps onto KnobMaxIterations
//     through the budget gates).
//
//  2. ShouldCountIteration(name) — the affirmative form, and the
//     pre_tool_use hook surface: a dispatch path calls this inside its
//     pre-dispatch logic to decide whether to charge the budget.
//
//  3. IterCounter — a thin, thread-safe counter used by this package's
//     own tests to verify the invariant (calling kenaz__sleep through a
//     BuiltinPool leaves the counter at its pre-call value) without
//     depending on the full agentgraph Counters struct.
//
// # Why the passive set lives here and not in agentgraph
//
//   - This package owns BuiltinPool + BuiltinRegistry — the layer that
//     maps namespaced names to concrete implementations. Passive-tool
//     identity is a property of that mapping.
//   - agentgraph already imports this package for the permission
//     verdict; keeping the check here means the dependency stays
//     one-way and no import cycle is possible.
//   - Future passive tools (e.g. __yield) register through
//     RegisterPassiveTool without touching agentgraph.
package toolloop

import (
	"sync/atomic"

	"github.com/kameas-ai/kenaz-harness/core/tools/sleep"
)

// passiveToolNames is the set of builtin tool names whose Cedar action is
// tool.passive. Calls to these tools must NOT increment the iteration counter.
// Stored as a map[string]struct{} for O(1) lookup; initialised from the
// package's sleep.ToolName constant so the name stays a single source of truth.
var passiveToolNames = map[string]struct{}{
	sleep.ToolName: {},
}

// IsPassiveTool reports whether toolName belongs to the tool.passive Cedar
// action family. Passive tools are excluded from the KnobMaxIterations budget
// (FR-010). callers in the dispatch path use this result to skip
// env.Counters.AddTool() for the named tool.
//
// The check is O(1) and allocation-free. toolName is the fully qualified
// "kenaz__<tool>" name; partial names are not matched.
func IsPassiveTool(toolName string) bool {
	_, ok := passiveToolNames[toolName]
	return ok
}

// RegisterPassiveTool adds toolName to the passive-tool set. Only for use
// during process initialisation (before any concurrent call to IsPassiveTool).
// Tests that introduce additional passive stubs may call this from init().
//
// Production code MUST NOT call this after the tool registry is live; the
// underlying map is not protected by a mutex because it is expected to be
// populated once at startup and then read-only.
func RegisterPassiveTool(toolName string) {
	passiveToolNames[toolName] = struct{}{}
}

// ShouldCountIteration returns true when calling toolName should increment
// the KnobMaxIterations budget counter. It is the affirmative gate:
//
//	if ShouldCountIteration(name) { counter.Inc() }
//
// This is the pre_tool_use hook surface: dispatch paths call this inside
// their pre-dispatch logic to decide whether to charge the iteration budget
// before executing the tool.
func ShouldCountIteration(toolName string) bool {
	return !IsPassiveTool(toolName)
}

// IterCounter is a goroutine-safe iteration counter. It is provided so this
// package's tests can assert that a sleep call leaves the counter unchanged,
// without depending on the full agentgraph Counters struct.
//
// Production code uses agentgraph.RunCounters for the actual budget gate;
// IterCounter is the lightweight test double.
type IterCounter struct {
	n atomic.Int64
}

// Inc increments the counter by one.
func (c *IterCounter) Inc() { c.n.Add(1) }

// Value returns the current counter value.
func (c *IterCounter) Value() int64 { return c.n.Load() }

// Reset sets the counter back to zero.
func (c *IterCounter) Reset() { c.n.Store(0) }

// AdvanceIfActive conditionally increments the counter for a tool dispatch.
// It returns true when the counter was incremented (i.e. toolName is not
// passive), and false when the iteration was skipped (toolName is passive).
//
// This is the canonical dispatch-path call site:
//
//	if counter.AdvanceIfActive(toolName) {
//	    // iteration charged — check budget ceiling
//	}
func (c *IterCounter) AdvanceIfActive(toolName string) bool {
	if !ShouldCountIteration(toolName) {
		return false
	}
	c.Inc()
	return true
}
