// Package engine is the runtime PolicyEngine implementation. It owns
// the prepared-evaluator cache, the EffectivePolicy snapshot pointer,
// and the hot-path Evaluate flow.
//
// Architectural integrity (DIRECTIVE_001 / spec C-001):
//
//   - This package MUST NOT import the OPA SDK directly. The OPA
//     adapter in core/policy/engine/opa is the only allowed importer;
//     this package consumes it through the Backend interface so a
//     future Cedar backend (plan D2) is a drop-in.
//   - Evaluation logic per kind lives in core/policy/clauses/<kind>;
//     this package only orchestrates dispatch.
package engine

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/policy"
)

// Backend is the engine-internal contract every policy backend
// satisfies. The default backend lives in this package; the OPA
// adapter in core/policy/engine/opa implements the same interface.
//
// The Backend never holds policy-domain logic — it is the moral
// equivalent of "here's a compiled module, evaluate it for this
// input."
type Backend interface {
	// Compile takes a CompiledBundle (module-name → source) and
	// prepares per-Action.Kind queries. The returned PreparedSet is
	// what the engine evaluates against on the hot path.
	Compile(ctx context.Context, bundle CompiledBundle) (PreparedSet, error)

	// Name returns a short identifier used in logs and explain
	// output, e.g. "embedded" or "opa".
	Name() string
}

// CompiledBundle is the lowering pipeline's output, consumed by the
// Backend. Module names are deterministic per WP03; the manifest
// supplies the per-clause traceability used by explain (WP13).
type CompiledBundle struct {
	Modules  map[string]string // module name → Rego source (or backend-specific source)
	Manifest []ModuleEntry     // sorted by (PolicyID, ClauseID)
}

// ModuleEntry traces a compiled module back to its source clause.
type ModuleEntry struct {
	PolicyID    string
	ClauseID    string
	Kind        string
	ModuleName  string
	SourceLayer policy.Layer
}

// PreparedSet is the per-Action.Kind cache of evaluators. Backends
// build it once per Reload; the engine consults it on the hot path.
type PreparedSet interface {
	// EvaluateAction runs every prepared query whose ActionKind
	// matches the action's kind. The first matching query wins
	// (consistent with the lowering's "exactly one rule per clause"
	// shape). If no query matches, returns (nil, false).
	EvaluateAction(ctx context.Context, action policy.Action) (*BackendDecision, bool)
}

// BackendDecision is the backend's view of a decision: a typed
// outcome plus the matching clause id. The engine wraps this into
// policy.Decision adding consumer-facing metadata.
type BackendDecision struct {
	Outcome    policy.Outcome
	ReasonCode policy.ReasonCode
	PolicyID   string
	ClauseID   string
	Message    string
}

// ----------------------------------------------------------------------------
// Engine struct
// ----------------------------------------------------------------------------

// Options configures a new Engine.
type Options struct {
	// Backend is the Rego (or substitute) evaluator. Required.
	Backend Backend

	// Emitter is the EventEmitter used to record audit events. If
	// nil, an in-memory MemoryEmitter is used.
	Emitter policy.EventEmitter

	// Lower is the YAML→Rego lowering function, normally
	// core/policy/lower.Compile. The seam exists so tests can swap
	// in a spy that asserts the hot path never recompiles (WP03 T004).
	Lower func(artifacts []policy.PolicyArtifact) (CompiledBundle, error)

	// Merger merges a slice of artifacts into an EffectivePolicy.
	// Normally core/policy/layer.Merge. The seam stays here to break
	// the import cycle: the engine package depends on layer through
	// this function pointer rather than importing layer.
	Merger func(artifacts []policy.PolicyArtifact) (policy.EffectivePolicy, []policy.Finding)

	// Now is a clock the engine consults for evaluation timestamps.
	// Defaults to time.Now when nil; tests inject a fixed clock.
	Now func() time.Time

	// IDGen returns a unique decision id. Defaults to a monotonic
	// counter. Production wiring substitutes a ULID generator.
	IDGen func() string
}

// Engine is the runtime PolicyEngine implementation.
type Engine struct {
	opts Options

	// snapshot holds the active EffectivePolicy + PreparedSet. The
	// pointer is swapped atomically on Reload so in-flight
	// evaluations always see a consistent view (plan §4 step 7).
	snapshot atomic.Pointer[snapshot]

	mu        sync.Mutex // serializes Reload calls
	idCounter uint64
}

type snapshot struct {
	effective policy.EffectivePolicy
	prepared  PreparedSet
}

// New constructs an Engine. The returned engine has an empty
// snapshot — every Evaluate call falls back to fail-closed until
// Reload is invoked with at least one artifact.
func New(opts Options) (*Engine, error) {
	if opts.Backend == nil {
		return nil, errors.New("policy/engine: Backend is required")
	}
	if opts.Emitter == nil {
		opts.Emitter = policy.NewMemoryEmitter()
	}
	if opts.Now == nil {
		opts.Now = func() time.Time { return time.Now().UTC() }
	}
	e := &Engine{opts: opts}
	if opts.IDGen == nil {
		opts.IDGen = e.nextDecisionID
		e.opts.IDGen = opts.IDGen
	}
	// Seed with an empty snapshot so EffectivePolicy() never panics.
	empty := &snapshot{
		effective: policy.EffectivePolicy{
			EffectiveID: "empty",
			ComputedAt:  e.opts.Now(),
		},
	}
	e.snapshot.Store(empty)
	return e, nil
}

func (e *Engine) nextDecisionID() string {
	n := atomic.AddUint64(&e.idCounter, 1)
	return fmt.Sprintf("dec-%016x", n)
}

// EffectivePolicy returns the current snapshot. Cheap (atomic load).
func (e *Engine) EffectivePolicy() policy.EffectivePolicy {
	return e.snapshot.Load().effective
}

// Evaluate runs the prepared evaluator for the action's kind. Falls
// back to fail-closed (Outcome=Deny, ReasonPolicyUnavailable) when no
// clause matches, and emits the corresponding PolicyEvent.
func (e *Engine) Evaluate(ctx context.Context, action policy.Action) (policy.Decision, error) {
	if action == nil {
		return policy.Decision{}, errors.New("policy/engine: nil action")
	}
	snap := e.snapshot.Load()
	now := e.opts.Now()
	id := e.opts.IDGen()

	summary := redactInputs(action.Inputs())

	if snap.prepared == nil {
		// No policy loaded yet; default fail-closed (NFR-005).
		dec := policy.Denied(id, policy.ReasonPolicyUnavailable, "", "",
			"policy backend not yet loaded; default fail-closed", summary)
		dec.EvaluatedAt = now
		_ = e.opts.Emitter.Emit(ctx, policy.PolicyEvent{
			Kind:       policy.EventPolicyUnavailableFailClosed,
			OccurredAt: now,
			DecisionID: id,
			Decision:   &dec,
			Inputs:     summary,
		})
		return dec, nil
	}

	bd, ok := snap.prepared.EvaluateAction(ctx, action)
	if !ok {
		// No prepared query matched: fail-closed (FR-007 / NFR-005).
		dec := policy.Denied(id, policy.ReasonPolicyUnavailable, "", "",
			"no clause matched action.kind="+action.Kind()+"; default fail-closed", summary)
		dec.EvaluatedAt = now
		_ = e.opts.Emitter.Emit(ctx, policy.PolicyEvent{
			Kind:       policy.EventPolicyUnavailableFailClosed,
			OccurredAt: now,
			DecisionID: id,
			Decision:   &dec,
			Inputs:     summary,
		})
		return dec, nil
	}

	dec := policy.Decision{
		DecisionID:   id,
		Outcome:      bd.Outcome,
		ReasonCode:   bd.ReasonCode,
		PolicyID:     bd.PolicyID,
		ClauseID:     bd.ClauseID,
		Message:      bd.Message,
		InputSummary: summary,
		EvaluatedAt:  now,
	}
	kind := policy.EventPolicyEvaluationAllowed
	if bd.Outcome == policy.Deny {
		kind = policy.EventPolicyEvaluationDenied
	}
	_ = e.opts.Emitter.Emit(ctx, policy.PolicyEvent{
		Kind:       kind,
		OccurredAt: now,
		PolicyID:   bd.PolicyID,
		ClauseID:   bd.ClauseID,
		DecisionID: id,
		Decision:   &dec,
		Inputs:     summary,
	})
	return dec, nil
}

// Explain is a minimal placeholder per FR-015 / WP13. It returns the
// decision plus the matched clause provenance. The full explainer
// surface (`would_match`, remediation hints) is a follow-up WP.
func (e *Engine) Explain(ctx context.Context, action policy.Action) (policy.Explanation, error) {
	dec, err := e.Evaluate(ctx, action)
	if err != nil {
		return policy.Explanation{}, err
	}
	exp := policy.Explanation{Action: action.Kind(), Decision: dec}
	if dec.ClauseID == "" {
		return exp, nil
	}
	for _, c := range e.EffectivePolicy().Clauses {
		if c.Clause.ClauseID == dec.ClauseID && c.SourcePolicyID == dec.PolicyID {
			exp.MatchedClauses = append(exp.MatchedClauses, c)
			break
		}
	}
	return exp, nil
}

// Reload composes a new snapshot: kind resolution → layer merge →
// lowering → backend prepare. Per plan §4 step 5, an error-severity
// validator finding aborts activation; the prior snapshot remains the
// active one.
func (e *Engine) Reload(ctx context.Context, artifacts []policy.PolicyArtifact) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	now := e.opts.Now()

	// Step 1: FR-017 — every clause kind must be registered.
	if err := policy.ResolveClauseKinds(artifacts); err != nil {
		return err
	}

	// Step 2: FR-016 — drop artifacts outside their validity window.
	active := filterByValidity(artifacts, now, defaultSkew)

	// Step 3: layer merge with strict-narrowing validation.
	var (
		effective policy.EffectivePolicy
		findings  []policy.Finding
	)
	if e.opts.Merger != nil {
		effective, findings = e.opts.Merger(active)
	} else {
		// No merger configured: treat the input as a single layer
		// (used by WP01 smoke tests before WP04 lands).
		effective = naiveEffective(active, now)
	}
	for _, f := range findings {
		if f.IsError() {
			// Emit the finding so audit can see it, but do NOT
			// activate the snapshot (plan §8 R6).
			_ = e.opts.Emitter.Emit(ctx, policy.PolicyEvent{
				Kind:       policy.EventPolicyValidatorFinding,
				OccurredAt: now,
				Finding:    findingPtr(f),
				Message:    f.Message,
			})
			return &ValidationError{Findings: findings}
		}
		_ = e.opts.Emitter.Emit(ctx, policy.PolicyEvent{
			Kind:       policy.EventPolicyValidatorFinding,
			OccurredAt: now,
			Finding:    findingPtr(f),
			Message:    f.Message,
		})
	}

	// Step 4: lower (YAML→Rego) and prepare.
	var bundle CompiledBundle
	if e.opts.Lower != nil {
		var err error
		bundle, err = e.opts.Lower(materializeArtifacts(effective))
		if err != nil {
			return fmt.Errorf("policy/engine: lower failed: %w", err)
		}
	} else {
		bundle = CompiledBundle{Modules: map[string]string{}}
	}
	prepared, err := e.opts.Backend.Compile(ctx, bundle)
	if err != nil {
		return fmt.Errorf("policy/engine: backend compile failed: %w", err)
	}

	// Step 5: atomic swap.
	prev := e.snapshot.Load()
	e.snapshot.Store(&snapshot{effective: effective, prepared: prepared})

	prevID := ""
	if prev != nil {
		prevID = prev.effective.EffectiveID
	}
	_ = e.opts.Emitter.Emit(ctx, policy.PolicyEvent{
		Kind:        policy.EventPolicyLoaded,
		OccurredAt:  now,
		OldEffective: prevID,
		NewEffective: effective.EffectiveID,
		Message:     "engine snapshot replaced",
	})
	if prev != nil && prevID != "" && prevID != effective.EffectiveID {
		_ = e.opts.Emitter.Emit(ctx, policy.PolicyEvent{
			Kind:        policy.EventPolicyLayerTransitioned,
			OccurredAt:  now,
			OldEffective: prevID,
			NewEffective: effective.EffectiveID,
		})
	}
	return nil
}

// ValidationError surfaces validator-error findings. Reload returns
// it so callers can ignore-or-fail-loud; the prior snapshot remains
// active.
type ValidationError struct {
	Findings []policy.Finding
}

func (e *ValidationError) Error() string {
	if len(e.Findings) == 0 {
		return "policy: validation error"
	}
	first := e.Findings[0]
	return fmt.Sprintf("policy: validation error: %s (clause %q): %s",
		first.Code, first.OffendingClause, first.Message)
}

// ----------------------------------------------------------------------------
// helpers
// ----------------------------------------------------------------------------

const defaultSkew = 5 * time.Minute

func filterByValidity(in []policy.PolicyArtifact, now time.Time, skew time.Duration) []policy.PolicyArtifact {
	out := make([]policy.PolicyArtifact, 0, len(in))
	for _, a := range in {
		if a.NotBefore != nil && now.Before(a.NotBefore.Add(-skew)) {
			continue
		}
		if a.NotAfter != nil && now.After(a.NotAfter.Add(skew)) {
			continue
		}
		out = append(out, a)
	}
	return out
}

// naiveEffective is the fallback used when no Merger is configured
// (WP01 only). It returns every clause tagged with its source layer
// and policy id — without narrowing semantics.
func naiveEffective(artifacts []policy.PolicyArtifact, now time.Time) policy.EffectivePolicy {
	out := policy.EffectivePolicy{
		EffectiveID: fmt.Sprintf("naive-%d", now.UnixNano()),
		ComputedAt:  now,
	}
	for _, a := range artifacts {
		for _, c := range a.Clauses {
			out.Clauses = append(out.Clauses, policy.EffectiveClause{
				Clause:         c,
				SourceLayer:    a.Layer,
				SourcePolicyID: a.PolicyID,
			})
		}
	}
	sort.SliceStable(out.Clauses, func(i, j int) bool {
		if out.Clauses[i].SourcePolicyID != out.Clauses[j].SourcePolicyID {
			return out.Clauses[i].SourcePolicyID < out.Clauses[j].SourcePolicyID
		}
		return out.Clauses[i].Clause.ClauseID < out.Clauses[j].Clause.ClauseID
	})
	out.ContentHash = "naive"
	return out
}

// materializeArtifacts re-derives a slice of PolicyArtifact-shaped
// inputs from the merged EffectivePolicy. Lowering wants to see the
// merged clauses, not the raw layered artifacts; this seam keeps the
// engine independent of the layer package.
func materializeArtifacts(eff policy.EffectivePolicy) []policy.PolicyArtifact {
	byPolicy := map[string]*policy.PolicyArtifact{}
	order := []string{}
	for _, c := range eff.Clauses {
		pa, ok := byPolicy[c.SourcePolicyID]
		if !ok {
			pa = &policy.PolicyArtifact{
				PolicyID: c.SourcePolicyID,
				Layer:    c.SourceLayer,
			}
			byPolicy[c.SourcePolicyID] = pa
			order = append(order, c.SourcePolicyID)
		}
		pa.Clauses = append(pa.Clauses, c.Clause)
	}
	sort.Strings(order)
	out := make([]policy.PolicyArtifact, 0, len(order))
	for _, id := range order {
		out = append(out, *byPolicy[id])
	}
	return out
}

func redactInputs(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func findingPtr(f policy.Finding) *policy.Finding { return &f }
