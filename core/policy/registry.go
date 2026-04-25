package policy

import (
	"fmt"
	"sort"
	"sync"
)

// ControlKind is the contract every per-kind clause subpackage
// implements. Per DIRECTIVE_001 / spec C-001, control-kind logic lives
// in clauses/<kind>/, each in its own subpackage. The registry below
// is package-level state in core/policy; per-kind packages call
// Register from init().
//
// The interface is intentionally small. A new control kind (e.g., a
// future DLP scanner) implements ControlKind and never modifies any
// existing kind (FR-005, SC-006, C-007).
type ControlKind interface {
	// Kind returns the canonical, lowercase, snake_case identifier of
	// this control kind, e.g. "provider_allowlist".
	Kind() string

	// FailurePostureDefault declares the default fail posture for
	// clauses of this kind (FR-007). Operators may override per clause
	// only when the kind explicitly opts in to FailOpen.
	FailurePostureDefault() FailurePosture

	// ValidateParams checks the kind-specific params shape and returns
	// a human-readable error citing the offending field on failure.
	// The error MUST identify the clause (caller passes clause_id) so
	// FR-017 surfaces remain actionable.
	ValidateParams(clauseID string, params map[string]any) error

	// LowerToRego emits a Rego module that expresses this clause's
	// semantics. The emitted module name is the responsibility of the
	// caller (core/policy/lower) to keep deterministic.
	LowerToRego(clause Clause) (string, error)

	// NarrowingMerge enforces strict-narrowing for this kind (FR-001 /
	// NFR-004). It MUST return a typed broadening error when child
	// declares a strictly broader permission than parent. Silence
	// semantics: parent-silent on a kind means "no constraint" — the
	// caller passes parent only when it has expressed a constraint.
	NarrowingMerge(parent, child Clause) (Clause, error)
}

// registry holds the global control-kind registrations. Per-kind
// packages call Register from init() — registration order does not
// matter; lookups are sorted by name where determinism is required.
var (
	registryMu      sync.RWMutex
	registry        = map[string]ControlKind{}
	registryFrozen  bool // for tests that want to detect surprise mutations
	registryOnAdd   func(string)
)

// Register adds a ControlKind to the global registry. Duplicate
// registration panics: that is a developer error (two packages
// claiming the same kind name) and silent shadowing would be a SOC 2
// finding waiting to happen.
//
// Per-kind packages call this from init():
//
//	func init() { policy.Register(&kind{}) }
func Register(k ControlKind) {
	if k == nil {
		panic("policy.Register: nil ControlKind")
	}
	name := k.Kind()
	if name == "" {
		panic("policy.Register: empty Kind() name")
	}
	registryMu.Lock()
	defer registryMu.Unlock()
	if _, exists := registry[name]; exists {
		panic(fmt.Sprintf("policy.Register: duplicate ControlKind %q", name))
	}
	registry[name] = k
	if registryOnAdd != nil {
		registryOnAdd(name)
	}
}

// Lookup returns the ControlKind registered under the given name. The
// boolean is false when the kind is unknown — callers MUST treat that
// as a load-time rejection (FR-017): unknown kinds never silently
// become no-ops.
func Lookup(name string) (ControlKind, bool) {
	registryMu.RLock()
	defer registryMu.RUnlock()
	k, ok := registry[name]
	return k, ok
}

// RegisteredKinds returns every registered kind name in sorted order,
// suitable for `harness policy explain` listings and for validator
// determinism (NFR-002).
func RegisteredKinds() []string {
	registryMu.RLock()
	defer registryMu.RUnlock()
	out := make([]string, 0, len(registry))
	for name := range registry {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// UnregisterAllForTest clears the registry. Tests that need a fresh
// registry call this at TestMain time. It is intentionally not
// exported for callers outside the test scope; in production the
// registry is filled exactly once at process start.
//
// The function name carries the "ForTest" suffix so it is easy to
// spot in code review.
func UnregisterAllForTest() {
	registryMu.Lock()
	defer registryMu.Unlock()
	registry = map[string]ControlKind{}
	registryFrozen = false
	registryOnAdd = nil
}

// MustLookup is a convenience used inside the layer + lower packages
// once a clause has already been validated against the registry.
// Panics on miss because by then the clause should have been rejected
// at load time.
func MustLookup(name string) ControlKind {
	k, ok := Lookup(name)
	if !ok {
		panic(fmt.Sprintf("policy.MustLookup: kind %q not registered", name))
	}
	return k
}

// ResolveClauseKinds walks a slice of artifacts and returns a typed
// error when any clause references an unregistered kind. This is the
// FR-017 enforcement seam called from engine.Reload and from the
// bundle handler (WP05).
func ResolveClauseKinds(artifacts []PolicyArtifact) error {
	for _, a := range artifacts {
		for _, c := range a.Clauses {
			if _, ok := Lookup(c.Kind); !ok {
				return &UnknownKindError{
					PolicyID: a.PolicyID,
					ClauseID: c.ClauseID,
					Kind:     c.Kind,
				}
			}
		}
	}
	return nil
}

// UnknownKindError is the typed error FR-017 mandates: an unknown
// kind on policy load fails loudly, never silently no-ops.
type UnknownKindError struct {
	PolicyID string
	ClauseID string
	Kind     string
}

func (e *UnknownKindError) Error() string {
	return fmt.Sprintf(
		"policy: unknown control kind %q in policy %q clause %q",
		e.Kind, e.PolicyID, e.ClauseID,
	)
}
