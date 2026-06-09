// Package audit defines the AuditAPI view-scoped accessor consumed by
// the audit-log viewer (downstream mission). Backed by event-log Reader.
package audit

import (
	"context"

	eventlog "github.com/kameas-ai/kenaz-harness/core/event/log"
)

// Entry is a redacted audit log entry. Redaction is applied server-side
// by the event-log mission's pipeline (privacy CI invariant #2 forbids
// raw user content in logs anyway).
type Entry struct {
	ID        string `json:"id"`
	Timestamp string `json:"timestamp"`
	Category  string `json:"category"`
	Subject   string `json:"subject"`
	Trailing  string `json:"trailing,omitempty"`
}

// Filter is a structured filter for ListEntries.
type Filter struct {
	Categories []string `json:"categories,omitempty"`
	Since      string   `json:"since,omitempty"`
	Until      string   `json:"until,omitempty"`
	Limit      int      `json:"limit,omitempty"`
}

// VerifyChainResult is the wire shape returned by VerifyChain.
type VerifyChainResult struct {
	// Verified is true when every row's payload_hash matched the
	// recomputed digest.
	Verified bool `json:"verified"`
	// RowsChecked is the number of rows examined.
	RowsChecked int `json:"rows_checked"`
	// BrokenAtID is the event_id of the first mismatch.
	// Empty when Verified is true.
	BrokenAtID string `json:"broken_at_id,omitempty"`
}

// AuditAPI is the view-scoped accessor for the append-only audit log.
type AuditAPI interface {
	ListEntries(ctx context.Context, filter Filter) ([]Entry, error)
	VerifyEntry(ctx context.Context, id string) (bool, error)
	// VerifyChain recomputes the payload hash for every event in
	// [fromID, toID] and returns whether the chain is intact.
	// Empty fromID / toID means "from first / to last".
	VerifyChain(ctx context.Context, fromID, toID string) (VerifyChainResult, error)
	// Filter applies a rich structured filter and returns matching entries.
	Filter(ctx context.Context, query eventlog.FilterQuery) ([]Entry, error)
	// ListSavedQueries returns all saved queries for the calling user.
	ListSavedQueries(ctx context.Context) ([]eventlog.SavedQuery, error)
	// SaveQuery persists a named query.
	SaveQuery(ctx context.Context, q eventlog.SavedQuery) error
	// DeleteQuery removes a saved query by ID.
	DeleteQuery(ctx context.Context, id string) error
	// Export writes an audit export file and returns the absolute path.
	Export(ctx context.Context, opts eventlog.ExportOptions) (string, error)
	// BulkPurge deletes the listed event IDs from the store after a Cedar
	// gate check. The operation is gated by ActionAuditBulkPurge; a Cedar
	// deny returns an error and leaves the store unchanged. On success the
	// purge is recorded via KindAuditBulkPurgeExecuted.
	BulkPurge(ctx context.Context, eventIDs []string) error
	StartStream(ctx context.Context, filter Filter) (subscriptionID string, err error)
	StopStream(ctx context.Context, subscriptionID string) error
}
