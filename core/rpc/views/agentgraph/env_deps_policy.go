package agentgraph

import (
	"context"
	"strings"

	coreag "github.com/kameas-ai/kenaz-harness/core/agentgraph"
	"github.com/kameas-ai/kenaz-harness/core/policy/cedar"
)

// PolicyGateAdapter wraps a cedar.Gate onto the agentgraph.PolicyGate
// interface. Production wiring binds the Cedar engine here so the
// kernel's filesystem + state executors enforce the active policy.
//
// nil gate ⇒ AllowAll fallback (every check passes). Matches the
// boot-stage default the chassis ships with.
type PolicyGateAdapter struct {
	gate cedar.Gate
}

// NewPolicyGateAdapter constructs the adapter. nil gate is replaced
// with cedar.AllowAll so call sites always have a Gate to evaluate
// against (the helper functions in core/policy/cedar/hooks.go are
// already nil-safe, but pinning the fallback here makes the wiring
// intent explicit).
func NewPolicyGateAdapter(g cedar.Gate) *PolicyGateAdapter {
	if g == nil {
		g = cedar.AllowAll{}
	}
	return &PolicyGateAdapter{gate: g}
}

// CheckFileRead delegates to cedar.CheckFileRead.
func (a *PolicyGateAdapter) CheckFileRead(ctx context.Context, path string) error {
	return cedar.CheckFileRead(ctx, a.gate, path)
}

// CheckFileWrite delegates to cedar.CheckFileWrite.
func (a *PolicyGateAdapter) CheckFileWrite(ctx context.Context, path string) error {
	return cedar.CheckFileWrite(ctx, a.gate, path)
}

// CheckStateRead delegates to cedar.CheckStateRead.
func (a *PolicyGateAdapter) CheckStateRead(ctx context.Context, source string) error {
	return cedar.CheckStateRead(ctx, a.gate, source)
}

// CheckStateWrite delegates to cedar.CheckStateWrite.
func (a *PolicyGateAdapter) CheckStateWrite(ctx context.Context, target string) error {
	return cedar.CheckStateWrite(ctx, a.gate, target)
}

// CheckTool delegates to BOTH cedar.CheckUseTool (the coarse
// Action::"use_tool" family every shipped tool policy names) and
// cedar.CheckTool (the finer-grained ActionToolExec sibling) — UNIT-15
// (trust-surfaces-that-fire-01PMZ202 WP17). toolName is the
// fully-qualified "<server>__<tool>" name; splitToolName mirrors the
// split cedar.Engine.populateFamilyContext applies to the resource id
// (core/policy/cedar/engine.go) so both evaluators see the same
// server/tool pair the entity UID was built from.
func (a *PolicyGateAdapter) CheckTool(ctx context.Context, toolName string) error {
	server, tool := splitToolName(toolName)
	if err := cedar.CheckUseTool(ctx, a.gate, server, tool); err != nil {
		return err
	}
	return cedar.CheckTool(ctx, a.gate, server, tool)
}

// splitToolName splits the kenaz-harness "<server>__<tool>" convention.
// Falls back to server="" (which cedar.ToolUID maps to "builtin") when
// no separator is present.
func splitToolName(name string) (server, tool string) {
	const sep = "__"
	idx := strings.Index(name, sep)
	if idx < 0 {
		return "", name
	}
	return name[:idx], name[idx+len(sep):]
}

// Compile-time witness.
var _ coreag.PolicyGate = (*PolicyGateAdapter)(nil)
