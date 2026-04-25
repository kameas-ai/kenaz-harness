// Package policy declares the per-resolution policy substrate (WP04):
//
//   - ConflictMode: override-by-precedence (default) vs fail-on-conflict.
//   - SizeBudget: NFR-002 per-layer ceiling and trim policy.
//   - VerificationPosture: fail-closed (default) vs serve-cache for
//     required-layer verification failures.
//
// Every knob travels on a [Resolution] value attached to ResolveRequest.
// No global state — that's an explicit invariant from WP04 acceptance.
package policy

import (
	"fmt"

	pack "github.com/sigil-tech/kaneaz-harness/core/context/pack"
)

// ConflictMode selects how the merger reacts when two layers define an
// entry with the same name.
type ConflictMode string

const (
	// ConflictOverrideByPrecedence is the v1 default: silent winner,
	// override recorded in the audit channel (FR-008, Open Question §9.2).
	ConflictOverrideByPrecedence ConflictMode = "override_by_precedence"

	// ConflictFail returns a typed error and refuses to produce a
	// snapshot when any name collision is detected (strict mode opt-in).
	ConflictFail ConflictMode = "fail_on_conflict"
)

// VerificationPosture controls behaviour when a *required* layer fails
// provenance verification (Risk R5).
type VerificationPosture string

const (
	// PostureFailClosed halts resolution (default).
	PostureFailClosed VerificationPosture = "fail_closed"

	// PostureServeCache returns the last-known-good cached snapshot when
	// available; the operator opted in to graceful degradation.
	PostureServeCache VerificationPosture = "serve_cache"
)

// SizeBudgetPolicy controls how the merger reacts when a layer's total
// size exceeds [Resolution.LayerSizeBudget].
type SizeBudgetPolicy string

const (
	// BudgetSoftWarn keeps entries by name order until the budget is
	// reached, then trims the rest and emits a structured warning.
	BudgetSoftWarn SizeBudgetPolicy = "keep_by_name_order_then_warn"

	// BudgetHardFail returns a typed error rather than trim. For
	// operators who treat the budget as an invariant.
	BudgetHardFail SizeBudgetPolicy = "hard_fail"
)

// Resolution bundles every per-call policy knob.
type Resolution struct {
	Conflict        ConflictMode
	Verification    VerificationPosture
	LayerSizeBudget int64 // 0 means use [pack.DefaultLayerSizeBudget]; <0 disables.
	BudgetMode      SizeBudgetPolicy
}

// Default returns the spec-default policy: override-by-precedence,
// fail-closed verification, NFR-002 default 256 KB budget, soft-warn trim.
func Default() Resolution {
	return Resolution{
		Conflict:        ConflictOverrideByPrecedence,
		Verification:    PostureFailClosed,
		LayerSizeBudget: pack.DefaultLayerSizeBudget,
		BudgetMode:      BudgetSoftWarn,
	}
}

// Strict returns the strict alternative: fail on conflict, fail-closed
// verification, hard-fail on budget overflow.
func Strict() Resolution {
	return Resolution{
		Conflict:        ConflictFail,
		Verification:    PostureFailClosed,
		LayerSizeBudget: pack.DefaultLayerSizeBudget,
		BudgetMode:      BudgetHardFail,
	}
}

// Normalised returns r with zero-valued fields filled by defaults; useful
// when callers construct partial Resolution values.
func (r Resolution) Normalised() Resolution {
	if r.Conflict == "" {
		r.Conflict = ConflictOverrideByPrecedence
	}
	if r.Verification == "" {
		r.Verification = PostureFailClosed
	}
	if r.BudgetMode == "" {
		r.BudgetMode = BudgetSoftWarn
	}
	if r.LayerSizeBudget == 0 {
		r.LayerSizeBudget = pack.DefaultLayerSizeBudget
	}
	return r
}

// EffectiveBudget returns the layer-size ceiling enforced by this policy.
// A negative LayerSizeBudget disables the ceiling entirely.
func (r Resolution) EffectiveBudget() int64 {
	if r.LayerSizeBudget < 0 {
		return 0
	}
	if r.LayerSizeBudget == 0 {
		return pack.DefaultLayerSizeBudget
	}
	return r.LayerSizeBudget
}

// ConflictReport is the structured payload returned alongside an
// [ErrConflict] in fail-on-conflict mode (and recorded as a warning in
// override-by-precedence mode for diagnostic visibility).
type ConflictReport struct {
	EntryName  string     `json:"entry_name"`
	LeftLayer  pack.Layer `json:"left_layer"`
	RightLayer pack.Layer `json:"right_layer"`
	LeftPack   pack.PackRef `json:"left_pack"`
	RightPack  pack.PackRef `json:"right_pack"`
	Resolved   bool       `json:"resolved"`
	Winner     pack.Layer `json:"winner,omitempty"`
	Reason     string     `json:"reason"`
}

// ErrConflict is returned by the merger in fail-on-conflict mode. It
// carries every conflict detected so operators can address them at once
// (rather than fix-build-fail-rebuild iteration).
type ErrConflict struct {
	Conflicts []ConflictReport
}

// Error implements error.
func (e *ErrConflict) Error() string {
	if len(e.Conflicts) == 0 {
		return "context: fail-on-conflict policy triggered (no detail)"
	}
	if len(e.Conflicts) == 1 {
		c := e.Conflicts[0]
		return fmt.Sprintf("context: fail-on-conflict: %q defined in %s and %s",
			c.EntryName, c.LeftLayer, c.RightLayer)
	}
	return fmt.Sprintf("context: fail-on-conflict: %d entries collide across layers", len(e.Conflicts))
}

// ErrOversizeLayer is returned in BudgetHardFail mode when a layer's
// total size exceeds the configured budget.
type ErrOversizeLayer struct {
	Layer    pack.Layer
	Pack     pack.PackRef
	Bytes    int64
	Budget   int64
	Trimmed  []string // entry names that would have been dropped in soft mode
}

// Error implements error.
func (e *ErrOversizeLayer) Error() string {
	return fmt.Sprintf("context: layer %s pack %s size %d > budget %d",
		e.Layer, e.Pack, e.Bytes, e.Budget)
}

// ErrRequiredLayerUnverified is returned when a required layer's
// provenance verification fails and the policy is fail-closed.
type ErrRequiredLayerUnverified struct {
	Pack   pack.PackRef
	Reason string
}

// Error implements error.
func (e *ErrRequiredLayerUnverified) Error() string {
	return fmt.Sprintf("context: required layer %s unverified: %s", e.Pack, e.Reason)
}
