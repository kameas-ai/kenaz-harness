// Package fleet provides the view-scoped RPC surface for fleet telemetry
// consent settings (fleet-otel-archival-01NDFSEX11 WP07) and the Phase-3
// unit-collaboration surface — promote-as-merge-request + conflict
// resolution (unified-context-artifacts-01NCTXU01 / Phase 3 / WP16-WP18).
package fleet

import "context"

// MergeRequestResult is the RPC-facing view of a created merge request
// (promote-as-MR, WP16). Fields mirror the fleet merge_requests object.
type MergeRequestResult struct {
	ID                 string `json:"id"`
	UnitNodeID         string `json:"unit_node_id"`
	FromClassification string `json:"from_classification"`
	ToClassification   string `json:"to_classification"`
	ProposedVersion    int    `json:"proposed_version"`
	Title              string `json:"title"`
	Body               string `json:"body"`
	Status             string `json:"status"`
	CreatedAt          string `json:"created_at"`
}

// UnitConflictView is the RPC-facing view of an unresolved same-unit pull
// conflict surfaced by the UnitSyncer (the input to WP17 resolution).
type UnitConflictView struct {
	UnitID        string `json:"unit_id"`
	NodeID        string `json:"node_id"`
	LocalVersion  int    `json:"local_version"`
	SyncedVersion int    `json:"synced_version"`
	ServerVersion int    `json:"server_version"`
}

// ResolvedUnitView is one entry in the precedence-ordered loadable set
// surfaced at resolution time (WP18). Enshrined-conflict units carry
// Flagged=true so the agent is told "N sides diverge here", never dropped.
type ResolvedUnitView struct {
	UnitID         string `json:"unit_id"`
	Title          string `json:"title"`
	Body           string `json:"body"`
	Scope          string `json:"scope"`
	Classification string `json:"classification"`
	Flagged        bool   `json:"flagged"`
	PeerUnitID     string `json:"peer_unit_id,omitempty"`
	Reason         string `json:"reason,omitempty"`
	Precedence     int    `json:"precedence"`
}

// FleetAPI is the view-scoped RPC surface for fleet telemetry consent and
// Phase-3 unit collaboration.
type FleetAPI interface {
	// GetTelemetryConsent returns the stored consent level for this device:
	// "none" (default), "aggregate", or "full".
	GetTelemetryConsent(ctx context.Context) (string, error)

	// SetTelemetryConsent persists the given consent level. Returns an error
	// when the org tier is insufficient for the requested level (e.g.
	// aggregate requires pro+, full requires team+).
	SetTelemetryConsent(ctx context.Context, level string) error

	// Unit_PromoteAsMergeRequest opens a merge request to promote a unit UP a
	// classification level (personal→team→org) instead of writing the higher
	// layer directly (WP16, write-up-reviewed). The source unit is untouched;
	// the higher layer only changes when a reviewer accepts on fleet.
	// toClassification is one of "team" | "org".
	Unit_PromoteAsMergeRequest(ctx context.Context, unitID, toClassification, title, body string) (MergeRequestResult, error)

	// Unit_ListConflicts returns the unresolved same-unit pull conflicts the
	// syncer has surfaced (the worklist for the resolution UI).
	Unit_ListConflicts(ctx context.Context) ([]UnitConflictView, error)

	// Unit_ResolveMerge applies a whole-body MERGE resolution: it writes the
	// resolved body as a new version on the local unit, clearing the conflict
	// (WP17a). The bumped version re-syncs on the next cycle.
	Unit_ResolveMerge(ctx context.Context, unitID, resolvedBody string) error

	// Unit_ResolveEnshrine applies an ENSHRINE resolution: it creates a NEW
	// coexisting unit carrying enshrinedBody, linked to the source by a
	// conflicts_with marker edge, so BOTH coexist and load (WP17b). Returns the
	// new unit id.
	Unit_ResolveEnshrine(ctx context.Context, srcUnitID, enshrinedTitle, enshrinedBody, reason string) (string, error)

	// Unit_ResolveLoadable returns the precedence-ordered loadable set for a
	// scope, with enshrined conflicts flagged (WP18). scope is "" for all,
	// or one of "global" | "project" | "session"; scopeID narrows further.
	Unit_ResolveLoadable(ctx context.Context, scope, scopeID string) ([]ResolvedUnitView, error)
}
