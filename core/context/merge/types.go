// Package merge implements the deterministic three-tier merge engine
// (FR-001, FR-008, WP03 / WP04). It consumes parsed [pack.ContextPack]
// values from each layer (org / team / personal) and produces an ordered
// [Result] that records winners, override decisions, conflict reports,
// and any structural warnings (budget trimming).
//
// Merge ordering is **personal > team > org**. The merger is pure
// (deterministic, no I/O, no time, no global state); every per-call
// behaviour switches travel on [policy.Resolution].
package merge

import (
	pack "github.com/kameas-ai/kenaz-harness/core/context/pack"
)

// LayerInput is one layer's contribution: the layer designation plus the
// (optional) parsed pack. A nil Pack means the layer is absent.
type LayerInput struct {
	Layer pack.Layer
	Pack  *pack.ContextPack
}

// Empty reports whether the layer has no contributing pack.
func (l LayerInput) Empty() bool { return l.Pack == nil }

// ResolvedEntry is one merged, post-policy, post-budget entry the
// downstream injector hands to an agent session.
type ResolvedEntry struct {
	pack.ContextEntry
	// Winner is the layer that contributed this entry's body. Equal to
	// SourceLayer for non-overridden entries.
	Winner pack.Layer `json:"winner"`
}

// OverrideRecord captures one layer-A-beat-layer-B decision. It is the
// operator-visible artefact behind FR-008's "every override is recorded".
type OverrideRecord struct {
	EntryName  string       `json:"entry_name"`
	Winner     pack.Layer   `json:"winner_layer"`
	WinnerPack pack.PackRef `json:"winner_pack"`
	Loser      pack.Layer   `json:"loser_layer"`
	LoserPack  pack.PackRef `json:"loser_pack"`
}

// LayerActivation summarises one contributing layer in the result.
// Used by audit / event-log emission downstream.
type LayerActivation struct {
	Layer    pack.Layer   `json:"layer"`
	Pack     pack.PackRef `json:"pack"`
	Entries  int          `json:"entries"` // post-scope-filter, pre-trim
	Trimmed  []string     `json:"trimmed,omitempty"`
	Warnings []pack.Warning `json:"warnings,omitempty"`
}

// Result is the byte-stable merge product.
type Result struct {
	Entries   []ResolvedEntry          `json:"entries"`
	Overrides []OverrideRecord         `json:"overrides"`
	Layers    []LayerActivation        `json:"layers"`
	Warnings  []pack.Warning           `json:"warnings,omitempty"`
}
