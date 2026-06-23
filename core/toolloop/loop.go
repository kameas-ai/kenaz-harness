// Iteration gate for the toolloop (WP04 — kenaz__sleep).
//
// The spec (FR-010) requires that kenaz__sleep does NOT count against
// KnobMaxIterations. The agentgraph loop body calls every tool through
// the BuiltinPool and increments a counter on each tool dispatch. The
// IterGate below is the hook point that lets the dispatch path decide
// whether to increment before handing control to the tool.
//
// # Architecture
//
// The IterGate exposes two helpers:
//
//  1. IsPassiveTool(name) — returns true for tools whose Cedar action is
//     tool.passive (currently kenaz__sleep only). The agentgraph's
//     toolDispatchExecutor consults this before calling env.Counters.AddTool()
//     so passive tools never increment the run-wide ToolCallsMade counter
//     (which maps onto KnobMaxIterations through the budget gates).
//
//  2. IterCounter — a thin, thread-safe counter that the loop test uses to
//     verify the invariant: calling kenaz__sleep through a BuiltinPool
//     leaves the counter at its pre-call value.
//
// # Integration with pre_tool_use
//
// The spec notes that the iteration-skip integrates with the toolloop's
// iteration gate via the pre_tool_use surface (Wave 1 WP03). The IterGate
// is that surface: callers fire ShouldCountIteration(toolName) inside
// their pre_tool_use logic to determine whether to increment.
//
// # Why not in agentgraph?
//
// The passive-tool identity (IsPassiveTool) belongs in the toolloop package
// because:
//   - The toolloop owns BuiltinPool + BuiltinRegistry — the layer that
//     maps namespaced names to concrete implementations.
//   - The agentgraph would need to import toolloop to check the name;
//     keeping the check here avoids an import cycle.
//   - Future passive tools (e.g. __yield) register through the same gate
//     without touching agentgraph.
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

// IterCounter is a goroutine-safe iteration counter. It is provided so the
// toolloop integration tests can assert that a sleep call leaves the counter
// unchanged, without depending on the full agentgraph Counters struct.
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
