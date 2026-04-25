package engine

import (
	"context"
	"sort"
	"sync"

	"github.com/sigil-tech/kaneaz-harness/core/policy"
)

// EmbeddedBackend is a backend-interface implementation that does not
// require the OPA SDK. It treats each lowered clause as a typed
// EmbeddedRule keyed by Action.Kind and evaluates them deterministically
// in clause-id order. The backend is the production-shaped fallback
// used when the OPA adapter has not yet been wired (DIRECTIVE_001
// keeps OPA isolated to core/policy/engine/opa); tests use it
// exclusively because it is hermetic and dependency-free.
//
// The backend understands the Rego we emit because the lowering pipeline
// in core/policy/lower owns both the producer and the consumer side.
// Real OPA reads the same modules through its Rego compiler when the
// `opa` build tag is enabled.
type EmbeddedBackend struct {
	// RuleSource is the function the lowering pipeline calls to
	// publish prepared rules. Tests inject a custom source; production
	// wiring fills it from CompiledBundle.Manifest plus a per-kind
	// Rule constructor exposed by clauses/<kind>.
	RuleSource func(bundle CompiledBundle) ([]EmbeddedRule, error)
}

// NewEmbeddedBackend constructs a backend with the given rule source.
// The source must return rules deterministically — same bundle in,
// same rule order out (NFR-002).
func NewEmbeddedBackend(src func(bundle CompiledBundle) ([]EmbeddedRule, error)) *EmbeddedBackend {
	return &EmbeddedBackend{RuleSource: src}
}

// Name implements Backend.
func (b *EmbeddedBackend) Name() string { return "embedded" }

// Compile implements Backend.
func (b *EmbeddedBackend) Compile(_ context.Context, bundle CompiledBundle) (PreparedSet, error) {
	rules := []EmbeddedRule{}
	if b.RuleSource != nil {
		rs, err := b.RuleSource(bundle)
		if err != nil {
			return nil, err
		}
		rules = rs
	}
	// Stable order: ActionKind, then PolicyID, then ClauseID.
	sort.SliceStable(rules, func(i, j int) bool {
		if rules[i].ActionKind != rules[j].ActionKind {
			return rules[i].ActionKind < rules[j].ActionKind
		}
		if rules[i].PolicyID != rules[j].PolicyID {
			return rules[i].PolicyID < rules[j].PolicyID
		}
		return rules[i].ClauseID < rules[j].ClauseID
	})
	idx := make(map[string][]EmbeddedRule)
	for _, r := range rules {
		idx[r.ActionKind] = append(idx[r.ActionKind], r)
	}
	return &embeddedPrepared{byKind: idx}, nil
}

// EmbeddedRule is the typed Rule shape the embedded backend evaluates.
// One rule per clause; the per-kind clause package supplies the
// Match closure during lowering.
type EmbeddedRule struct {
	// ActionKind is the Action.Kind() value this rule applies to.
	ActionKind string

	// PolicyID / ClauseID describe provenance and drive the audit
	// log entry produced when this rule produces the decision.
	PolicyID string
	ClauseID string

	// Match runs the rule against the action and returns the typed
	// decision. The boolean result indicates "this rule is
	// applicable" — when false, the engine continues to the next
	// rule rather than treating the result as a final outcome.
	Match func(ctx context.Context, action policy.Action) (BackendDecision, bool)
}

// embeddedPrepared implements PreparedSet.
type embeddedPrepared struct {
	mu     sync.RWMutex
	byKind map[string][]EmbeddedRule
}

// EvaluateAction runs every rule whose ActionKind matches in
// deterministic order. The first deny wins; otherwise, if any rule
// returned an allow, we return that allow. If no rule matched, we
// return (nil, false) so the engine falls back to fail-closed
// (NFR-005).
func (p *embeddedPrepared) EvaluateAction(ctx context.Context, action policy.Action) (*BackendDecision, bool) {
	p.mu.RLock()
	rules := p.byKind[action.Kind()]
	p.mu.RUnlock()
	var firstAllow *BackendDecision
	for _, r := range rules {
		dec, ok := r.Match(ctx, action)
		if !ok {
			continue
		}
		if dec.Outcome == policy.Deny {
			out := dec
			out.PolicyID = r.PolicyID
			out.ClauseID = r.ClauseID
			return &out, true
		}
		if firstAllow == nil {
			out := dec
			out.PolicyID = r.PolicyID
			out.ClauseID = r.ClauseID
			firstAllow = &out
		}
	}
	if firstAllow != nil {
		return firstAllow, true
	}
	return nil, false
}
