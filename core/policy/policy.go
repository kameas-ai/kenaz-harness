// Package policy is the public API surface for the kenaz-harness policy
// engine. Every consumer that takes a policy-relevant action (LLM call,
// MCP install, A2A peer dial, bundle fetch, scheduler dispatch, network
// exposure) calls into the PolicyEngine returned by core/policy/engine.
//
// Architectural integrity (DIRECTIVE_001 / spec C-001):
//
//   - This package and the per-kind clause subpackages are the only places
//     that hold policy-evaluation logic.
//   - Only core/policy/engine/opa/ may import the OPA SDK. Every other
//     piece of the engine talks to a backend through the Backend
//     interface so a future Cedar (or other) backend is a drop-in.
//   - Each control kind lives in its own subpackage under
//     core/policy/clauses/<kind>/ and registers itself with this package
//     at init() time via Register.
//
// The types in this file deliberately mirror data-model.md verbatim so
// that drift between artifact, in-memory, and audit shapes stays visible.
package policy

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"time"
)

// ----------------------------------------------------------------------------
// Engine interface (FR-006)
// ----------------------------------------------------------------------------

// PolicyEngine is the single API every consumer in the harness uses.
// Implementations live in core/policy/engine.
type PolicyEngine interface {
	// Evaluate returns a Decision in under 1 ms p99 (NFR-001) on the hot
	// path. Default posture is fail-closed: when no clause matches and no
	// fail-open opt-in applies, Evaluate returns Outcome=Deny with
	// ReasonPolicyUnavailable (FR-007, NFR-005).
	Evaluate(ctx context.Context, action Action) (Decision, error)

	// Explain returns the clauses that matched and clauses that would
	// have matched, supporting `harness policy explain` (FR-015).
	Explain(ctx context.Context, action Action) (Explanation, error)

	// EffectivePolicy returns the current cached merged snapshot. The
	// snapshot pointer is swapped atomically on Reload; existing
	// references remain valid until they fall out of scope.
	EffectivePolicy() EffectivePolicy

	// Reload triggers a recompose + revalidate. It is called by the
	// bundle resolver when a policy artifact changes (FR-001 layer
	// transitions, NFR-007 hot reload). Reload MUST NOT activate a
	// snapshot whose validator emitted any error-severity Finding; the
	// prior snapshot continues to apply (plan §8 R6).
	Reload(ctx context.Context, artifacts []PolicyArtifact) error
}

// ----------------------------------------------------------------------------
// Action / Decision (FR-006, FR-008)
// ----------------------------------------------------------------------------

// Action is the typed input every consumer constructs at the action
// boundary. Concrete shapes live in the consuming package; this surface
// stays consumer-agnostic so the engine never needs to know about
// individual subsystems.
type Action interface {
	// Kind names the operation, e.g. "llm.call", "mcp.install",
	// "a2a.dial", "bundle.fetch", "scheduler.dispatch", "network.expose".
	Kind() string

	// Inputs returns a JSON-shaped, redaction-aware map the engine
	// evaluates. Implementations MUST NOT leak credentials; redaction is
	// the consumer's responsibility per plan §8 R7.
	Inputs() map[string]any
}

// Decision is the typed result of one Evaluate call (FR-006, FR-009).
type Decision struct {
	// DecisionID is a unique ID for this evaluation. Used by override
	// tokens to reference the denial they override.
	DecisionID string `json:"decision_id"`

	// Outcome is allow or deny.
	Outcome Outcome `json:"outcome"`

	// ReasonCode is the closed denial taxonomy from FR-008.
	ReasonCode ReasonCode `json:"reason_code"`

	// PolicyID identifies the source artifact that produced the decision.
	PolicyID string `json:"policy_id"`

	// ClauseID identifies the matching clause within the artifact.
	ClauseID string `json:"clause_id"`

	// InputSummary is a redacted snapshot of Action.Inputs() suitable
	// for the audit log.
	InputSummary map[string]any `json:"input_summary"`

	// EvaluatedAt is the wall-clock time at which the engine produced
	// this decision.
	EvaluatedAt time.Time `json:"evaluated_at"`

	// Message is human-readable; consumers pattern-match on ReasonCode.
	Message string `json:"message,omitempty"`
}

// Allowed is a convenience constructor for an allow Decision.
func Allowed(decisionID, policyID, clauseID string, summary map[string]any) Decision {
	return Decision{
		DecisionID:   decisionID,
		Outcome:      Allow,
		PolicyID:     policyID,
		ClauseID:     clauseID,
		InputSummary: summary,
		EvaluatedAt:  time.Now().UTC(),
	}
}

// Denied is a convenience constructor for a deny Decision.
func Denied(decisionID string, reason ReasonCode, policyID, clauseID, message string, summary map[string]any) Decision {
	return Decision{
		DecisionID:   decisionID,
		Outcome:      Deny,
		ReasonCode:   reason,
		PolicyID:     policyID,
		ClauseID:     clauseID,
		Message:      message,
		InputSummary: summary,
		EvaluatedAt:  time.Now().UTC(),
	}
}

// Outcome is a closed enum: allow or deny.
type Outcome int

const (
	// Allow means the action is permitted by the active policy.
	Allow Outcome = iota
	// Deny means the action is rejected.
	Deny
)

// String renders Outcome for logs. The values track the canonical
// audit-log strings used by event-log consumers.
func (o Outcome) String() string {
	switch o {
	case Allow:
		return "allow"
	case Deny:
		return "deny"
	default:
		return fmt.Sprintf("unknown_outcome(%d)", int(o))
	}
}

// ReasonCode is the closed denial-reason taxonomy from FR-008. Consumers
// pattern-match on this value; the human message lives in
// Decision.Message.
type ReasonCode string

const (
	// ReasonNotInAllowlist denotes a value (provider, model, server,
	// peer, capability) absent from a permitted set.
	ReasonNotInAllowlist ReasonCode = "not_in_allowlist"

	// ReasonExceedsCeiling denotes a numeric input above an allowed
	// upper bound (e.g., per-day cost ceiling).
	ReasonExceedsCeiling ReasonCode = "exceeds_ceiling"

	// ReasonMissingSignature denotes an artifact whose signature
	// envelope is absent.
	ReasonMissingSignature ReasonCode = "missing_signature"

	// ReasonWrongSigner denotes an artifact whose signature does not
	// match a trusted anchor.
	ReasonWrongSigner ReasonCode = "wrong_signer"

	// ReasonNetworkTierNotPermitted denotes an outbound or inbound
	// network exposure outside the permitted tier.
	ReasonNetworkTierNotPermitted ReasonCode = "network_tier_not_permitted"

	// ReasonCapabilityNotPermitted denotes an MCP capability call not
	// allowed by the active server allowlist.
	ReasonCapabilityNotPermitted ReasonCode = "capability_not_permitted"

	// ReasonSandboxRequired denotes a workflow node that must run in a
	// sandbox.
	ReasonSandboxRequired ReasonCode = "sandbox_required"

	// ReasonScheduleNotPermitted denotes a scheduler dispatch outside
	// the permitted set.
	ReasonScheduleNotPermitted ReasonCode = "schedule_not_permitted"

	// ReasonPolicyUnavailable denotes a control whose input source is
	// unavailable and whose failure posture is fail_closed (the
	// default). Used as the fail-closed transitional reason when no
	// other clause matches.
	ReasonPolicyUnavailable ReasonCode = "policy_unavailable"
)

// AllReasonCodes returns every ReasonCode in the taxonomy. Test code
// uses it to assert FR-008 coverage stays in sync with the spec.
func AllReasonCodes() []ReasonCode {
	return []ReasonCode{
		ReasonNotInAllowlist,
		ReasonExceedsCeiling,
		ReasonMissingSignature,
		ReasonWrongSigner,
		ReasonNetworkTierNotPermitted,
		ReasonCapabilityNotPermitted,
		ReasonSandboxRequired,
		ReasonScheduleNotPermitted,
		ReasonPolicyUnavailable,
	}
}

// ----------------------------------------------------------------------------
// Layer + FailurePosture
// ----------------------------------------------------------------------------

// Layer identifies the source of a PolicyArtifact. Layers compose under
// strict-narrowing semantics: each child layer can only narrow its
// parent (see core/policy/layer).
type Layer string

const (
	// LayerOrg is the broadest layer.
	LayerOrg Layer = "org"
	// LayerTeam narrows org.
	LayerTeam Layer = "team"
	// LayerPersonal narrows team.
	LayerPersonal Layer = "personal"
)

// LayerOrder returns the canonical ordering for narrowing-merge:
// org → team → personal. Index 0 is the broadest layer.
func LayerOrder() []Layer { return []Layer{LayerOrg, LayerTeam, LayerPersonal} }

// IsKnown reports whether the layer string is one of the canonical
// values. Used by the schema validator to reject misspellings (FR-017).
func (l Layer) IsKnown() bool {
	switch l {
	case LayerOrg, LayerTeam, LayerPersonal:
		return true
	default:
		return false
	}
}

// FailurePosture controls behavior when a clause's input source is
// unavailable (NFR-005). Default is FailClosed; FailOpen requires
// explicit per-clause opt-in.
type FailurePosture string

const (
	// FailClosed denies on unavailable input. Emits
	// policy_unavailable_fail_closed.
	FailClosed FailurePosture = "fail_closed"
	// FailOpen allows on unavailable input. Emits a louder
	// policy_unavailable_fail_open event so monitoring can alert.
	FailOpen FailurePosture = "fail_open"
)

// ----------------------------------------------------------------------------
// Policy artifact / clause
// ----------------------------------------------------------------------------

// PolicyArtifact is a versioned, signed bundle artifact of kind
// `policy`. The shape mirrors data-model.md §PolicyArtifact verbatim.
//
// JSON / YAML tags use snake_case to align with the canonical
// authoring shape.
type PolicyArtifact struct {
	// PolicyID is a globally unique ULID assigned at authoring time.
	PolicyID string `json:"policy_id" yaml:"policy_id"`
	// Name is human-readable.
	Name string `json:"name" yaml:"name"`
	// Version is operator-facing (semver-shaped string).
	Version string `json:"version" yaml:"version"`
	// Layer pins this artifact to org / team / personal.
	Layer Layer `json:"layer" yaml:"layer"`
	// Clauses are the individual control declarations.
	Clauses []Clause `json:"clauses" yaml:"clauses"`
	// NotBefore / NotAfter are optional validity windows (FR-016).
	NotBefore *time.Time `json:"not_before,omitempty" yaml:"not_before,omitempty"`
	NotAfter  *time.Time `json:"not_after,omitempty"  yaml:"not_after,omitempty"`
	// ContentHash is filled by the load pipeline (see ContentHashOf).
	ContentHash string `json:"content_hash,omitempty" yaml:"content_hash,omitempty"`
	// SignatureEnvelope is opaque to the engine; verified by
	// a2a-signed-cards-trust before this struct ever reaches the
	// engine.
	SignatureEnvelope []byte `json:"-" yaml:"-"`
	// CompiledRego is filled by the lowering pipeline; opaque to
	// callers but exposed for diagnostics.
	CompiledRego []byte `json:"-" yaml:"-"`
}

// Clause is one parameterized control instance. Mirrors data-model.md
// §Clause verbatim.
type Clause struct {
	// ClauseID is stable within an artifact.
	ClauseID string `json:"clause_id" yaml:"clause_id"`
	// Kind references a registered ControlKind.
	Kind string `json:"kind" yaml:"kind"`
	// Params are kind-specific. Validated by ControlKind.ValidateParams.
	Params map[string]any `json:"params" yaml:"params"`
	// FailurePosture defaults to FailClosed.
	FailurePosture FailurePosture `json:"failure_posture,omitempty" yaml:"failure_posture,omitempty"`
}

// EffectivePosture resolves the effective failure posture: any value
// other than FailOpen is treated as FailClosed (the default).
func (c Clause) EffectivePosture() FailurePosture {
	if c.FailurePosture == FailOpen {
		return FailOpen
	}
	return FailClosed
}

// ----------------------------------------------------------------------------
// EffectivePolicy + Explanation placeholders
// ----------------------------------------------------------------------------

// EffectiveClause carries the merged Clause plus provenance.
type EffectiveClause struct {
	Clause         Clause `json:"clause"`
	SourceLayer    Layer  `json:"source_layer"`
	SourcePolicyID string `json:"source_policy_id"`
}

// EffectivePolicy is the merged + validated snapshot consumers
// evaluate against (data-model.md §EffectivePolicy).
type EffectivePolicy struct {
	EffectiveID       string            `json:"effective_id"`
	Clauses           []EffectiveClause `json:"clauses"`
	ValidatorFindings []Finding         `json:"validator_findings,omitempty"`
	ComputedAt        time.Time         `json:"computed_at"`
	ContentHash       string            `json:"content_hash"`
}

// Finding is a typed validator output. The full Finding type with code
// constants lives in core/policy/layer/finding.go so that package can
// depend on these primitive types without an import cycle. The shape
// here is the wire shape used in EffectivePolicy.
type Finding struct {
	Code           string `json:"code"`
	Message        string `json:"message"`
	OffendingClause string `json:"offending_clause,omitempty"`
	SourcePolicyID string `json:"source_policy_id,omitempty"`
	Severity       string `json:"severity"` // "error" | "warn"
}

// IsError reports whether the finding blocks activation.
func (f Finding) IsError() bool { return f.Severity == "error" }

// Explanation is the placeholder shape returned by Engine.Explain
// (FR-015). The full Explainer surface lands in WP13.
type Explanation struct {
	Action       string            `json:"action"`
	Decision     Decision          `json:"decision"`
	MatchedClauses []EffectiveClause `json:"matched_clauses,omitempty"`
	WouldMatch     []EffectiveClause `json:"would_match,omitempty"`
}

// ----------------------------------------------------------------------------
// Content hashing (FR-016 / WP02 T005)
// ----------------------------------------------------------------------------

// ContentHashOf returns the SHA-256 over a canonical JSON encoding of
// the artifact's identifying content (policy_id, name, version, layer,
// validity window, clauses sorted by clause_id, with stable param
// ordering). Two semantically identical artifacts produce the same
// hash regardless of input map ordering.
func ContentHashOf(p PolicyArtifact) string {
	canon := canonicalArtifact(p)
	b, err := json.Marshal(canon)
	if err != nil {
		// json.Marshal on a canonical structure cannot fail; if it
		// does we return an obvious sentinel rather than panic.
		return "sha256:invalid"
	}
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// canonicalArtifact returns a representation of the artifact suitable
// for stable hashing: clauses sorted by clause_id, param maps walked
// recursively to sorted-key form. SignatureEnvelope and ContentHash
// are excluded so the hash is over content, not metadata about it.
func canonicalArtifact(p PolicyArtifact) any {
	cls := make([]Clause, len(p.Clauses))
	copy(cls, p.Clauses)
	sort.SliceStable(cls, func(i, j int) bool {
		return cls[i].ClauseID < cls[j].ClauseID
	})
	canonClauses := make([]map[string]any, 0, len(cls))
	for _, c := range cls {
		canonClauses = append(canonClauses, map[string]any{
			"clause_id":       c.ClauseID,
			"kind":            c.Kind,
			"params":          canonValue(c.Params),
			"failure_posture": string(c.EffectivePosture()),
		})
	}
	out := map[string]any{
		"policy_id": p.PolicyID,
		"name":      p.Name,
		"version":   p.Version,
		"layer":     string(p.Layer),
		"clauses":   canonClauses,
	}
	if p.NotBefore != nil {
		out["not_before"] = p.NotBefore.UTC().Format(time.RFC3339Nano)
	}
	if p.NotAfter != nil {
		out["not_after"] = p.NotAfter.UTC().Format(time.RFC3339Nano)
	}
	return out
}

// canonValue walks a JSON-shaped value and returns a representation
// that hashes deterministically: maps become a slice of [k,v] tuples
// in sorted-key order; slices keep their order (lists are ordered
// data); scalars pass through.
func canonValue(v any) any {
	switch x := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		out := make([][2]any, 0, len(keys))
		for _, k := range keys {
			out = append(out, [2]any{k, canonValue(x[k])})
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, item := range x {
			out[i] = canonValue(item)
		}
		return out
	case []string:
		out := make([]any, len(x))
		for i, s := range x {
			out[i] = s
		}
		return out
	default:
		return x
	}
}
