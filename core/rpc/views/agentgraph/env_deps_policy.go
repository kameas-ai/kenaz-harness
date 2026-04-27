package agentgraph

import (
	"context"

	coreag "github.com/sigil-tech/kaneaz-harness/core/agentgraph"
	"github.com/sigil-tech/kaneaz-harness/core/policy/cedar"
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

// Compile-time witness.
var _ coreag.PolicyGate = (*PolicyGateAdapter)(nil)
