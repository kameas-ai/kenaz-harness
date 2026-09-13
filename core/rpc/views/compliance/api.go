// Package compliance provides the view-scoped RPC surface for the fleet
// audit-archival compliance panel (fleet-audit-archival-01NDFSEX13 WP05).
//
// Three RPCs:
//   - Compliance_Status: snapshot of archive state for the UI.
//   - Compliance_ArchiveNow: trigger an immediate archive flush.
//   - Compliance_SetRetention: update the local retention window.
package compliance

import "context"

// ComplianceStatus is the wire shape returned by the Status RPC.
// Carries the state of the archive loop and retention policy for the
// Compliance panel.
type ComplianceStatus struct {
	// LastArchivedAt is the RFC3339 timestamp of the last successful ACK,
	// or empty string when nothing has been archived yet.
	LastArchivedAt string `json:"lastArchivedAt"`
	// PendingCount is the approximate number of locally-unarchived events.
	PendingCount int64 `json:"pendingCount"`
	// ChainBreak is true when the archiver has halted due to a hash-chain
	// break. Operator action required to resume.
	ChainBreak bool `json:"chainBreak"`
	// RetentionDays is the currently configured local retention window.
	RetentionDays int `json:"retentionDays"`
	// Enabled is true when CapAuditLogImmuDB is active for this user.
	Enabled bool `json:"enabled"`
	// ArchiverRunning is true when the background archive loop is running.
	ArchiverRunning bool `json:"archiverRunning"`
}

// ComplianceAPI is the view-scoped RPC surface for the Compliance panel.
type ComplianceAPI interface {
	// Status returns a snapshot of the audit archival state.
	Status(ctx context.Context) (ComplianceStatus, error)

	// ArchiveNow triggers an immediate archive flush.
	// Returns an error when the archiver is halted (chain-break) or not
	// running (capability not enabled).
	ArchiveNow(ctx context.Context) error

	// SetRetention updates the local retention window.
	// days must be one of 30, 60, 90, or 365.
	SetRetention(ctx context.Context, days int) error

	// SkipToID is the operator recovery action after a hash-chain
	// break: it manually advances the archiver's cursor to toID,
	// clearing the halt so ArchiveNow can proceed again, and emits
	// fleet.audit_chain_skipped. Returns ErrComplianceNotEnabled when
	// the archiver is not wired.
	//
	// (fleet-enforcement-truth-01PMZ505 WP06 — before this, SkipToID
	// existed on *fleet.AuditArchiver with zero callers: ArchiveNow's
	// own error told an operator to resolve the chain break, and there
	// was no control anywhere — in the app or the console — that did
	// so.)
	SkipToID(ctx context.Context, toID string) error
}
