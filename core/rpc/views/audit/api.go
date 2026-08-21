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
//
// NOT extended with a third "unavailable" state in this change, even
// though spec §8 AC-014 / G-2 asks for one ("Frontend: the Audit view
// renders 'unavailable' distinctly from 'verified'... Fails if: the
// frontend collapses the third state back to a boolean"): doing so
// requires a Wails bindings regeneration (`wails generate module`),
// which needs a profile-directory override via $HOME — CLAUDE.md's own
// documented incident (this exact command applied six live migrations
// to a developer's real ~/.kenaz/harness/prod/data.db on 2026-08-20)
// is why that override exists, and the agent sandbox in THIS session
// categorically refuses any command that sets HOME, for a worktree-
// isolated agent, with no override. Hand-editing frontend/wailsjs/**
// is separately and explicitly forbidden (check-codegen.sh hashes the
// Go source against a restampable hash and cannot tell a hand-written
// binding from a generated one). Both paths to a wire-type change were
// unavailable in this environment.
//
// What ships instead: VerifyChain (impl.go) now returns Verified: false
// (never true) when no store is configured — the CORE of G-2 / D-6 ("no
// manufactured success") without a wire-type change. RowsChecked is 0
// in that case, distinguishing it from a checked-and-intact chain
// (RowsChecked > 0, Verified true) at the existing field granularity,
// though not as unambiguously as a dedicated Available flag would (a
// verified empty range also reports RowsChecked: 0). Follow-up, for an
// agent/session with wails-toolchain access outside this sandbox: add
// `Available bool` here, route both Go call sites and
// AuditView.vue's tri-state render off it, regenerate bindings, run
// `check-codegen.sh --update-wailsjs-hash`.
type VerifyChainResult struct {
	// Verified is true when every row's payload_hash matched the
	// recomputed digest. False both when a real chain check found a
	// break AND when no store was configured to check against — see
	// the type doc comment above for why that ambiguity is not yet
	// resolved.
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
