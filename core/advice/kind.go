package advice

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// SafetyClass is an AdviceKind's auto-act safety classification (spec §3,
// owner ruling). It is the ONLY thing that may let a recommendation act
// without a human click at the Autonomous tier, and it lives in the
// registry rather than in UI convention — RequireCanAutoAct below is the
// Go-level, compile-visible enforcement point (the #78 lesson: a toggle
// that "reports it is on" in a panel is not the same as a switch nothing
// can bypass).
type SafetyClass string

const (
	// SafetyReversible marks a kind whose auto-act path is a peer, not a
	// mutation: branch_now is the v1 example — a branch is a new peer
	// session, the original is untouched. Only a SafetyReversible kind
	// may auto-act at the Autonomous tier (threshold >= 80).
	SafetyReversible SafetyClass = "reversible"
	// SafetySuggestOnly marks a kind whose action discards state or
	// changes cost/behaviour irreversibly enough that it stays a click
	// even for autonomous runs until real acceptance data says
	// otherwise (spec §3): compact_now (discards context) and
	// escalate_model (changes cost/behaviour) are both suggest_only in
	// v1, unconditionally, regardless of tier.
	SafetySuggestOnly SafetyClass = "suggest_only"
)

// Valid reports whether c is one of the two known safety classes. The
// zero value (empty string) is NOT valid — every registered kind must
// state one explicitly; there is no safe default for "unspecified".
func (c SafetyClass) Valid() bool {
	switch c {
	case SafetyReversible, SafetySuggestOnly:
		return true
	default:
		return false
	}
}

// Features is the model-facing, JSON-marshalable output of one kind's
// feature extractor — the ONLY slice of session state the model sees for
// a given Recommend call (spec §2: "never raw transcript by default").
// Kinds should populate this with a struct, not a map: encoding/json
// marshals struct fields in declaration order deterministically, which
// FeaturesHash (hash.go) relies on for a reproducible cache key across
// materially-identical inputs; a map's key order is also deterministic
// in Go's encoding/json (sorted), but a struct additionally gives
// RenderPrompt a stable, typed shape to render from.
type Features any

// FeatureExtractor is a kind's typed feature-extraction contract. input
// is whatever session-state snapshot the KIND's OWN package defines and
// passes in — this package does not know or constrain its concrete
// shape, by design: AC-08 requires that adding a fourth kind touches
// nothing outside its own registry entry and extractor, which is only
// possible if core/advice stays ignorant of what any kind's extractor
// actually consumes. An extractor MUST type-assert input to its own
// concrete type and return an error (never panic) on a mismatch —
// exactly the same defensive contract RiskRater's Rate applies to a
// model's response text: unparseable/unexpected input degrades to an
// error, never a guess.
type FeatureExtractor func(input any) (Features, error)

// PromptRenderer turns one kind's extracted Features into the two prompt
// halves an Advisor implementation sends to the model. Kept separate
// from FeatureExtractor so a kind's prompt text can change (bumping
// PromptVersion) without touching how its features are derived, and vice
// versa — the same separation of concerns risk/llmrater.go's fixed
// system prompt didn't need (one rater, one prompt) but a registry of
// heterogeneous kinds does.
type PromptRenderer func(features Features) (system, user string)

// AdviceKind is one registry entry: everything the seam needs to resolve
// a model, build a prompt, hash+cache the result, and enforce the
// auto-act safety boundary for one advice question. Spec §2's four
// required fields — id, extractor, prompt_version, safety class — map
// directly onto ID, Extract, PromptVersion, SafetyClass; RenderPrompt is
// this package's own addition to let Extract and prompt text evolve
// independently (see PromptRenderer's doc comment).
type AdviceKind struct {
	// ID is the kind's stable identifier (e.g. "branch_now",
	// "compact_now", "escalate_model"). Used as the registry key and as
	// part of the session cache key — changing it after a kind ships is
	// equivalent to registering a brand-new kind and abandoning the old
	// one's cache/label history.
	ID string
	// PromptVersion identifies which prompt-text revision this
	// registration produces (mirrors risk.Rating.PromptVersion /
	// llmRaterPromptVersion's doc comment exactly): bump it whenever
	// RenderPrompt's SEMANTICS change, never on a typo fix, so a cached
	// recommendation produced under an old prompt is never compared
	// against, or returned for, a new one. Part of the cache key for
	// exactly that reason (see cache.go's cacheKey).
	PromptVersion string
	// SafetyClass gates whether this kind may ever auto-act at the
	// Autonomous tier. See RequireCanAutoAct.
	SafetyClass SafetyClass
	// Extract is this kind's typed feature extractor.
	Extract FeatureExtractor
	// RenderPrompt turns Extract's output into model input.
	RenderPrompt PromptRenderer
}

// validate reports the required fields missing from k, per spec §2 /
// tasks.md WP03's completeness check ("every registered kind carries
// id/extractor/prompt_version/safety-class"). RenderPrompt is required
// too — a kind with no way to build a prompt can never produce a
// recommendation — but is reported separately from the four spec-named
// fields so a caller reading the error can tell which of the two
// generations of requirement (spec's four vs. this package's fifth) it
// tripped.
func (k AdviceKind) validate() error {
	var missing []string
	if k.ID == "" {
		missing = append(missing, "id")
	}
	if k.PromptVersion == "" {
		missing = append(missing, "prompt_version")
	}
	if !k.SafetyClass.Valid() {
		missing = append(missing, "safety_class")
	}
	if k.Extract == nil {
		missing = append(missing, "extractor")
	}
	if k.RenderPrompt == nil {
		missing = append(missing, "prompt_renderer")
	}
	if len(missing) > 0 {
		name := k.ID
		if name == "" {
			name = "<empty>"
		}
		return fmt.Errorf("advice: kind %q missing required field(s): %s", name, strings.Join(missing, ", "))
	}
	return nil
}

// ErrKindNotRegistered is returned by Get-adjacent lookups (and wrapped
// into RequireCanAutoAct's error) when no kind with the given id has
// been registered.
var ErrKindNotRegistered = errors.New("advice: kind not registered")

// ErrKindAlreadyRegistered is returned by Register when id collides with
// an existing registration — kinds are registered exactly once, at
// package init time, never re-registered or overwritten.
var ErrKindAlreadyRegistered = errors.New("advice: kind already registered")

// ErrSuggestOnlyCannotAutoAct is wrapped into RequireCanAutoAct's error
// when the named kind's SafetyClass is SafetySuggestOnly.
var ErrSuggestOnlyCannotAutoAct = errors.New("advice: suggest-only kind cannot auto-act")

var (
	registryMu sync.RWMutex
	registry   = map[string]AdviceKind{}
)

// Register adds k to the package-level registry. Returns an error
// (never panics) when k fails validate() or when k.ID collides with an
// existing registration — a production init() should normally call
// MustRegister instead; Register exists so tests (AC-08's toy-kind test,
// this package's own registry tests) can assert on the error value
// directly.
func Register(k AdviceKind) error {
	if err := k.validate(); err != nil {
		return err
	}
	registryMu.Lock()
	defer registryMu.Unlock()
	if _, exists := registry[k.ID]; exists {
		return fmt.Errorf("%w: %q", ErrKindAlreadyRegistered, k.ID)
	}
	registry[k.ID] = k
	return nil
}

// MustRegister is Register, panicking on error. The intended call shape
// for a kind's own package init() — exactly like
// core/wiring/knobcoverage's Register[T] pattern, a startup-time
// programmer error (a kind shipped with a missing field, or two kinds
// racing to claim the same id) should fail loudly at boot, not degrade
// into "this kind silently never produces advice."
func MustRegister(k AdviceKind) {
	if err := Register(k); err != nil {
		panic(err)
	}
}

// Get returns the registered kind for id, if any.
func Get(id string) (AdviceKind, bool) {
	registryMu.RLock()
	defer registryMu.RUnlock()
	k, ok := registry[id]
	return k, ok
}

// All returns every registered kind, sorted by ID for deterministic
// iteration (tests and the WP03 CI gate both depend on this being
// stable across runs).
func All() []AdviceKind {
	registryMu.RLock()
	defer registryMu.RUnlock()
	out := make([]AdviceKind, 0, len(registry))
	for _, k := range registry {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Count returns the number of registered kinds.
func Count() int {
	registryMu.RLock()
	defer registryMu.RUnlock()
	return len(registry)
}

// unregisterForTest removes id from the registry. Test-only: production
// registrations are permanent for the process lifetime (kinds are
// registered once, at init, and never unregistered) — this exists solely
// so package tests (e.g. AC-08's toy-kind proof) can register a
// throwaway kind without polluting other tests' view of the registry via
// All()/Count().
func unregisterForTest(id string) {
	registryMu.Lock()
	defer registryMu.Unlock()
	delete(registry, id)
}

// RequireCanAutoAct is the compile-visible enforcement point spec §3
// requires: "the safety class lives in the registry and is enforced in
// Go at the act site — a compile-visible switch, not UI convention (the
// #78 lesson)." A future WP07 auto-act call site MUST call this — and
// treat any non-nil error as "do not auto-act, fall back to the passive
// chip" — before executing a kind's action unattended. No production
// call site exists yet in this WP (WP07 ships the delivery layer this
// guards); scripts/ci/check-advice-kinds.sh's suggest-only check greps
// for call sites of this exact function name and, for each one whose
// argument it can resolve to a string literal, verifies the named kind
// is not SafetySuggestOnly.
func RequireCanAutoAct(kindID string) error {
	k, ok := Get(kindID)
	if !ok {
		return fmt.Errorf("%w: %q", ErrKindNotRegistered, kindID)
	}
	if k.SafetyClass != SafetyReversible {
		return fmt.Errorf("%w: %q is %s", ErrSuggestOnlyCannotAutoAct, kindID, k.SafetyClass)
	}
	return nil
}
