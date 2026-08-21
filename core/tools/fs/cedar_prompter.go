package fs

import (
	"context"

	"github.com/kameas-ai/kenaz-harness/core/policy/cedar"
)

// Compile-time assertion: CedarPrompter implements Prompter.
var _ Prompter = (*CedarPrompter)(nil)

// CedarPrompter adapts a *cedar.Registry onto the Prompter interface so
// the filesystem gate's NotApplicable path surfaces the same
// interactive permission modal bash already uses (B-4 / R-01,
// trust-surfaces-that-fire-01PMZ202 WP22 / UNIT-20). Mirrors
// core/tools/bash.Tool's cedarGate NotApplicable arm
// (core/tools/bash/bash.go:523-535), the canonical pattern for the
// prompt call.
//
// A nil Registry is a valid, deliberately-inert zero value: Prompt then
// returns PromptDeny with no error, which is exactly NoOpPrompter's
// contract. That lets production wiring construct this unconditionally
// — same nil-tolerant shape as corebash.Options.PromptRegistry — and
// keeps the "interactive when a channel is attached, NoOpPrompter
// otherwise" composite (Coordination note, tasks.md) correct without a
// nil-check at the call site: builtins_wiring.go passes the process
// promptRegistry, which is nil only on the test-harness / nil-core
// path.
type CedarPrompter struct {
	Registry *cedar.Registry
}

// Prompt implements Prompter by delegating to
// cedar.Registry.RequestInteractive. The registry itself resolves the
// unattended-run posture check and the autonomy fast paths
// (core/policy/cedar/prompt.go) — including the 5-minute PromptTimeout
// resolving to deny — before a human is ever involved, so those
// semantics are inherited here rather than reimplemented (C-12: no
// GateOptions.PromptTimeout field exists; the cedar package constant is
// the only budget).
func (p *CedarPrompter) Prompt(ctx context.Context, s PromptSurface) (PromptResponse, error) {
	if p == nil || p.Registry == nil {
		return PromptDeny, nil
	}
	surface := cedar.PromptSurface{
		FS: &cedar.FSPromptSurface{
			Op:            string(s.Op),
			CanonicalPath: s.CanonicalPath,
			Dangerous:     s.IsDangerous,
		},
		// E-008: fs.PromptSurface additionally carries DangerCopy and
		// IsInsideRecipeDir; cedar.FSPromptSurface has no home for
		// either, so the modal loses that hazard-banner text on this
		// path. Not fixed here — a cedar.FSPromptSurface field addition
		// is a separate, larger change (it is used across every fs
		// gate caller, not just this adapter).
		//
		// SessionID is intentionally left empty: builtins_wiring.go:502
		// constructs the gate process-scoped with no session in
		// context, so every fs grant lands in cedar's "global" transient
		// bucket rather than being scoped per-session.
	}
	resolution, err := p.Registry.RequestInteractive(ctx, surface)
	if err != nil {
		return PromptDeny, err
	}
	switch resolution.Decision {
	case cedar.DecisionDeny:
		return PromptDeny, nil
	case cedar.DecisionAllowOnce:
		return PromptAllowOnce, nil
	case cedar.DecisionAllowAlways:
		// E-007: fs.PromptResponse has no directory-scope discriminator
		// and cedar.Resolution carries no scope signal at all. Default
		// to the narrower grant — a single exact-path persistent
		// policy, not a whole directory — per the WP's explicit ruling.
		return PromptAllowExact, nil
	default:
		return PromptDeny, nil
	}
}
