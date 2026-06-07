package cedar

import (
	"context"

	"github.com/kameas-ai/kenaz-harness/core/llm"
)

// LLMPolicyGuard adapts a Cedar Gate to the llm.PolicyGuard interface
// so the existing pipeline (profile → CapabilityGate → PolicyGuard →
// CredentialResolver) gates model selection through Cedar with no
// changes to core/llm itself.
//
// On Deny, the adapter returns *llm.ErrPolicyDenied carrying the
// human-readable reason — that error type is what the registry's
// pipeline already pattern-matches on, so the audit emitter and the
// frontend's denial notice flow continue to work unchanged.
//
// Allow + NotApplicable both result in nil so the call proceeds. The
// audit log captured by the Cedar engine still records every
// decision regardless of outcome.
type LLMPolicyGuard struct {
	Gate Gate
}

// NewLLMPolicyGuard wires a Gate behind the llm.PolicyGuard
// interface. gate may be nil — the resulting guard then permits
// every call (the harness is not yet running with policy enforcement
// turned on).
func NewLLMPolicyGuard(gate Gate) *LLMPolicyGuard {
	return &LLMPolicyGuard{Gate: gate}
}

// Allow implements llm.PolicyGuard.
func (g *LLMPolicyGuard) Allow(ctx context.Context, req llm.GenerationRequest, prof llm.ProviderProfile) error {
	if g == nil || g.Gate == nil {
		return nil
	}
	model := req.Model
	if model == "" {
		model = prof.Model
	}
	d := g.Gate.Evaluate(
		ctx,
		UserUID(),
		ActionModelSelect,
		ModelUID(prof.Kind, model),
		nil,
	)
	if d.Outcome == Deny {
		// Return the llm-flavoured error so registry pipeline matches.
		return &llm.ErrPolicyDenied{Reason: d.Reason}
	}
	return nil
}

// Compile-time witness: *LLMPolicyGuard satisfies llm.PolicyGuard.
var _ llm.PolicyGuard = (*LLMPolicyGuard)(nil)
