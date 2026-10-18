package agentgraph

import (
	"context"
	"strings"
	"sync/atomic"

	coreag "github.com/kameas-ai/kenaz-harness/core/agentgraph"
	"github.com/kameas-ai/kenaz-harness/core/policy/cedar"
	corefs "github.com/kameas-ai/kenaz-harness/core/tools/fs"
)

// PolicyGateAdapter wraps a cedar.Gate onto the agentgraph.PolicyGate
// interface. Production wiring binds the Cedar engine here so the
// kernel's filesystem + state executors enforce the active policy.
//
// nil gate ⇒ AllowAll fallback (every check passes). Matches the
// boot-stage default the chassis ships with.
type PolicyGateAdapter struct {
	gate cedar.Gate
	// fs is the late-bound core/tools/fs.Gate slot
	// (graph-fs-gate-01GFSG01 WP02). A POINTER to the slot so every
	// WithPostureMode copy shares it: chat_runner re-wraps the adapter
	// per session, and a SetFileGate that lands after (or before) a
	// copy must reach that copy too. Late-bound because the graph
	// manager's EnvDeps are built before newLLMStack constructs the fs
	// gate (and newLLMStack takes the graph manager as an argument).
	fs *fileGateSlot
}

// fileGateSlot holds the shared *corefs.Gate. The concrete pointer
// type (not an interface) keeps a nil gate genuinely nil — the
// test-chassis path has no fs gate, and an interface would box a typed
// nil.
type fileGateSlot struct {
	p atomic.Pointer[corefs.Gate]
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
	return &PolicyGateAdapter{gate: g, fs: &fileGateSlot{}}
}

// SetFileGate binds the fs.Gate instance the fs builtin tools use, so
// the read_file / write_file executors consult the same confirmed
// roots, prompt and unattended-deny flow (FileAccessGate). nil unbinds
// (Cedar-only, the pre-WP02 behaviour). Safe to call concurrently with
// evaluations and after WithPostureMode copies exist.
func (a *PolicyGateAdapter) SetFileGate(g *corefs.Gate) {
	if a == nil || a.fs == nil {
		return
	}
	a.fs.p.Store(g)
}

// AuthorizeFileRead implements coreag.FileAccessGate.
func (a *PolicyGateAdapter) AuthorizeFileRead(ctx context.Context, path string) error {
	return a.authorizeFile(ctx, corefs.OpRead, path)
}

// AuthorizeFileWrite implements coreag.FileAccessGate.
func (a *PolicyGateAdapter) AuthorizeFileWrite(ctx context.Context, path string) error {
	return a.authorizeFile(ctx, corefs.OpWrite, path)
}

// authorizeFile runs the fs.Gate flow. Anything but Allow becomes a
// *cedar.PolicyDeniedError carrying the gate's decision, so the
// executor's error handling (and IsPolicyDenied callers) see the same
// shape as a Cedar forbid. An invalid path (corefs.ErrInvalidPath) is
// returned as-is.
func (a *PolicyGateAdapter) authorizeFile(ctx context.Context, op corefs.Op, path string) error {
	if a == nil || a.fs == nil {
		return nil
	}
	g := a.fs.p.Load()
	if g == nil {
		return nil
	}
	d, err := g.Evaluate(ctx, op, path)
	if err != nil {
		return err
	}
	if d.Outcome != cedar.Allow {
		return &cedar.PolicyDeniedError{Decision: d}
	}
	return nil
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

// WithPostureMode re-wraps the adapter's underlying Cedar gate with
// cedar.WithPostureMode, so a named posture mode (currently only
// "plan_mode") denies write-class actions at the Cedar layer, not just
// at the knob level (trust-surfaces-that-fire-01PMZ202 WP23 / AN-04
// second seam).
//
// Before this, plan_mode's write-denial lived entirely in
// autonomy.planModePreset — an empty AutoApproveFamilies, which only
// changes whether a tool call auto-approves or parks on a confirmation
// prompt. A user (or a hook, or a stale session-level Allow-always
// grant) answering "allow" to that prompt still let the write through,
// because nothing at the Cedar gate itself knew the session was in
// plan_mode. WithPostureMode closes that: it is evaluated BEFORE the
// underlying policy bundle, so plan_mode narrows what the bundle would
// otherwise permit, the same "narrows, never broadens" contract
// cedar.WithPostureMode documents.
//
// mode == "" (no active posture mode, the overwhelming common case) is
// a transparent pass-through — see cedar.WithPostureMode's own
// contract for any mode other than PostureModePlanMode. Returns a new
// *PolicyGateAdapter; a is left unmodified so a caller cannot
// accidentally share the wrapped gate across sessions that resolve to
// different posture modes.
func (a *PolicyGateAdapter) WithPostureMode(mode string) coreag.PolicyGate {
	if a == nil {
		return a
	}
	out := NewPolicyGateAdapter(cedar.WithPostureMode(mode, a.gate))
	// Share the fs.Gate slot, not a snapshot of it (graph-fs-gate-
	// 01GFSG01 WP02): the copy must see a SetFileGate on the original.
	if a.fs != nil {
		out.fs = a.fs
	}
	return out
}

// Compile-time witnesses.
var (
	_ coreag.PolicyGate     = (*PolicyGateAdapter)(nil)
	_ coreag.FileAccessGate = (*PolicyGateAdapter)(nil)
)
