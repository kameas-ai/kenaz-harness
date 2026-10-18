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

// UnitSyncStatusView is the wire-safe snapshot of UnitSyncer.Status.
// Exposed via Unit_SyncStatus so the frontend can render LastPullErr /
// LastPushErr without polling at the per-second rate. (fleet-integrity-
// observability WP08)
type UnitSyncStatusView struct {
	// Cursor is the server-side sync cursor (opaque string).
	Cursor string `json:"cursor"`
	// LastPullAt is the RFC3339 timestamp of the last successful pull, or empty.
	LastPullAt string `json:"lastPullAt"`
	// LastPullErr is the most-recent pull error, or empty.
	LastPullErr string `json:"lastPullErr"`
	// LastPushErr is the most-recent push error, or empty.
	LastPushErr string `json:"lastPushErr"`
	// PushCount is the lifetime push count for this syncer instance.
	PushCount int `json:"pushCount"`
	// PullCount is the lifetime pull count for this syncer instance.
	PullCount int `json:"pullCount"`
	// ConflictCount is the number of currently surfaced conflicts.
	ConflictCount int `json:"conflictCount"`
	// SkippedUnknownKinds counts pulled nodes that are not unit kinds
	// (skipped, never a lane-stalling error — WP03).
	SkippedUnknownKinds int `json:"skippedUnknownKinds"`
	// SkippedInvalid counts unit nodes the local store refused as invalid.
	SkippedInvalid int `json:"skippedInvalid"`
	// PushRefused counts dirty units refused before the wire (artifact /
	// unknown kind, capability word in metadata); they stay dirty.
	PushRefused int `json:"pushRefused"`
	// PushHeldLoadAlways counts load_policy=always units held back because
	// the identity carries no roles (re-sign-in to refresh) — review F9.
	PushHeldLoadAlways int `json:"pushHeldLoadAlways"`
	// StrippedUnitKeys counts case-variant "_unit" metadata keys stripped
	// before push (review F13).
	StrippedUnitKeys int `json:"strippedUnitKeys"`
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

	// Unit_PromoteAsMergeRequest promotes a unit UP a classification level
	// (review F2, 2026-10-06): personal→team is a straight push of a team
	// copy (Status "published", no MR); team→org pushes the team node then
	// opens a merge request to org (the higher layer only changes when a
	// reviewer accepts on fleet); personal→org does both. The source unit
	// is untouched. toClassification is one of "team" | "org".
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

	// Unit_SyncStatus returns a snapshot of the unit syncer state: cursor,
	// last pull/push timestamps and errors, and the current conflict count.
	// Returns a zero-value view when the syncer is not wired (offline/OSS).
	// (fleet-integrity-observability WP08)
	Unit_SyncStatus(ctx context.Context) (UnitSyncStatusView, error)
}
