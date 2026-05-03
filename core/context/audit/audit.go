// Package audit declares the context-event kinds emitted to the
// append-only event log (plan §5.4). Per C-005 the event log is the
// canonical record; this package never writes its own store. The
// concrete event-log mission (event-log-01KQ1A3M) provides the
// [Emitter] implementation; until that lands the [Emitter] interface
// here is the only contract surface that crosses the boundary.
package audit

import (
	"context"
	"encoding/json"
	"time"

	pack "github.com/sigil-tech/kaneaz-harness/core/context/pack"
)

// Kind enumerates context-event kinds (FR-011, FR-012). The kinds
// mirror plan §5.4 1:1.
type Kind string

const (
	KindResolutionStarted   Kind = "context.resolution_started"
	KindPackFetched         Kind = "context.pack_fetched"
	KindPackVerified        Kind = "context.pack_verified"
	KindPackRejected        Kind = "context.pack_rejected"
	KindOverrideApplied     Kind = "context.override_applied"
	KindCacheServed         Kind = "context.cache_served"
	KindResolutionCompleted Kind = "context.resolution_completed"
	KindInjectionEmitted    Kind = "context.injection_emitted"
	KindScopeRevoked        Kind = "context.scope_revoked"
	KindUpdateAvailable     Kind = "context.update_available"

	// Compaction kinds (compaction-strategy-ui-01KQ8TDI WP01). These
	// signal lifecycle events of the summarize-then-replace compaction
	// engine; the engine emits one of {session_compacted, failed} per
	// run, and originals_deleted on the retention sweep that tombstones
	// archived rows.
	KindSessionCompacted          Kind = "compaction.session_compacted"
	KindCompactionFailed          Kind = "compaction.failed"
	KindCompactedOriginalsDeleted Kind = "compaction.originals_deleted"

	// KindSessionAutoTitled signals that the auto-titling engine produced
	// (or attempted to produce) a session title
	// (session-auto-titling-01KQ8TDS WP01).
	KindSessionAutoTitled Kind = "sessions.auto_titled"

	// KindCredentialAccessed signals that a credential was accessed via
	// credstore.Use (credential-store-01KQ8TDD WP03). The payload carries
	// only the redaction-safe RefID; raw bytes, locator strings, and
	// display strings are never included.
	KindCredentialAccessed Kind = "credential.accessed"
	// Branch Advisor kinds (branch-as-subagent-recommendation WP08).
	// Four lifecycle events cover the suggestion → accept/dismiss →
	// commit flow so the operator can compute advisor accuracy and
	// dismissal rate.

	// KindBranchAdvisorSuggested fires when Detect returns a non-nil
	// BranchSuggestion and the resolution logic (env flag, project
	// override, session dismiss) passes. Payload:
	// BranchAdvisorSuggestedPayload.
	KindBranchAdvisorSuggested Kind = "branch_advisor.suggested"

	// KindBranchAdvisorAccepted fires when the user submits the
	// context-pick modal and a subagent branch session is created.
	// Payload: BranchAdvisorAcceptedPayload.
	KindBranchAdvisorAccepted Kind = "branch_advisor.accepted"

	// KindBranchAdvisorDismissed fires when the user clicks "No thanks",
	// "Don't suggest again", or when the project-level override is
	// "always_off". Payload: BranchAdvisorDismissedPayload.
	KindBranchAdvisorDismissed Kind = "branch_advisor.dismissed"

	// KindBranchReintegrated fires when CommitReintegration appends the
	// synthetic system message to the parent session. Payload:
	// BranchReintegratedPayload.
	KindBranchReintegrated Kind = "branch_advisor.reintegrated"
)

// Event is the wire shape passed to the event log. The concrete event-log
// mission accepts a richer envelope; this package speaks only the
// payload-and-kind shape and lets the host re-pack it as needed.
type Event struct {
	Kind    Kind            `json:"kind"`
	TS      time.Time       `json:"ts"`
	Payload json.RawMessage `json:"payload"`
}

// Emitter is the contract surface this package consumes. The event-log
// mission's Emitter implements this trivially. Zero-mutation guarantees
// (append-only, redaction pipeline) are owned by the host.
type Emitter interface {
	Emit(ctx context.Context, e Event) error
}

// ResolutionStartedPayload carries the signalling for FR-011.
type ResolutionStartedPayload struct {
	RequestID    string `json:"request_id"`
	LockfileHash string `json:"lockfile_hash"`
	LayerCount   int    `json:"layer_count"`
}

// PackFetchedPayload — channel + content hash + version (FR-011).
type PackFetchedPayload struct {
	Pack    pack.PackRef `json:"pack"`
	Channel string       `json:"channel"`
}

// PackVerifiedPayload carries provenance metadata (FR-003, NFR-003).
type PackVerifiedPayload struct {
	Pack       pack.PackRef `json:"pack"`
	AnchorID   string       `json:"anchor_id"`
	Algorithm  string       `json:"algorithm"`
	CacheState string       `json:"cache_state"`
	GraceState string       `json:"grace_state"`
}

// PackRejectedPayload carries the rejection reason (FR-003 / SC-003).
// Code is the trust mission's typed code (FR-017 taxonomy) — see
// core/context/verify.
type PackRejectedPayload struct {
	Pack    pack.PackRef `json:"pack"`
	Code    string       `json:"code"`
	Message string       `json:"message"`
}

// OverrideAppliedPayload (FR-008).
type OverrideAppliedPayload struct {
	EntryName string       `json:"entry_name"`
	Winner    pack.Layer   `json:"winner"`
	Loser     pack.Layer   `json:"loser"`
	WinnerPack pack.PackRef `json:"winner_pack"`
	LoserPack  pack.PackRef `json:"loser_pack"`
}

// CacheServedPayload — FR-007 / NFR-004.
type CacheServedPayload struct {
	SnapshotID string `json:"snapshot_id"`
	Mode       string `json:"mode"`
}

// ResolutionCompletedPayload — FR-011.
type ResolutionCompletedPayload struct {
	SnapshotID    string        `json:"snapshot_id"`
	Duration      time.Duration `json:"duration"`
	OverrideCount int           `json:"override_count"`
}

// InjectionEmittedPayload — FR-010, FR-012.
type InjectionEmittedPayload struct {
	SessionID  string `json:"session_id"`
	SnapshotID string `json:"snapshot_id"`
	AgentID    string `json:"agent_id,omitempty"`
}

// ScopeRevokedPayload — FR-016.
type ScopeRevokedPayload struct {
	Pack pack.PackRef `json:"pack"`
	Role string       `json:"role"`
}

// UpdateAvailablePayload — FR-017.
type UpdateAvailablePayload struct {
	Pack             pack.PackRef `json:"pack"`
	AvailableVersion string       `json:"available_version"`
	DiffSummary      string       `json:"diff_summary"`
}

// SessionCompactedPayload carries the success-path signalling for the
// summarize-then-replace compaction engine
// (compaction-strategy-ui-01KQ8TDI WP01). Emitted once per successful
// Compact() call. Compression ratio is precomputed
// (TokensAfterSummary / TokensInSpan) so dashboards don't have to.
type SessionCompactedPayload struct {
	SessionID          string  `json:"session_id"`
	AggressivenessTier string  `json:"aggressiveness_tier"`
	ModelUsed          string  `json:"model_used"`
	TokensInSpan       int     `json:"tokens_in_span"`
	TokensAfterSummary int     `json:"tokens_after_summary"`
	CompressionRatio   float64 `json:"compression_ratio"`
}

// CompactionFailedPayload carries the failure-path signalling for the
// compaction engine. ErrorKind is the typed error class
// ("model_too_small", "during_tool_pair", "session_full", …) per the
// engine's exported sentinel errors.
type CompactionFailedPayload struct {
	SessionID          string `json:"session_id"`
	AggressivenessTier string `json:"aggressiveness_tier"`
	ModelUsed          string `json:"model_used"`
	TokensInSpan       int    `json:"tokens_in_span"`
	ErrorKind          string `json:"error_kind"`
}

// CompactedOriginalsDeletedPayload signals that the retention sweep
// tombstoned a window of archived session_messages rows. Carries the
// row count plus the archived_at extremes the sweep covered, so an
// operator inspecting the audit log can reproduce the cursor query.
type CompactedOriginalsDeletedPayload struct {
	DeletedCount     int       `json:"deleted_count"`
	OldestArchivedAt time.Time `json:"oldest_archived_at"`
	NewestArchivedAt time.Time `json:"newest_archived_at"`
}

// CredentialAccessedPayload is the audit payload for KindCredentialAccessed
// (credential-store-01KQ8TDD WP03). It carries only the redaction-safe RefID;
// raw key bytes, locator strings, and display strings are never included
// (DIRECTIVE_001 / FR-008).
type CredentialAccessedPayload struct {
	// RefID is ref.CredentialReference.ID() — a hash-derived, redaction-safe
	// token that identifies the credential without revealing its locator.
	RefID string `json:"ref_id"`
	// Purpose is the AccessPurpose enum string (e.g. "provider_call").
	Purpose string `json:"purpose"`
	// ToolCallID is the in-flight tool-call id, if available from the context.
	ToolCallID string `json:"tool_call_id,omitempty"`
	// RequestID is the HTTP / RPC request id, if available from the context.
	RequestID string `json:"request_id,omitempty"`
	// AccessedAt is the wall-clock time the Use call was made.
	AccessedAt time.Time `json:"accessed_at"`
}

// SessionAutoTitledPayload carries signalling for the auto-titling engine
// (session-auto-titling-01KQ8TDS WP01 §2.8). Emitted on both success and
// failure paths; ErrorKind is empty on success.
type SessionAutoTitledPayload struct {
	SessionID      string `json:"session_id"`
	GeneratedTitle string `json:"generated_title,omitempty"`
	ModelUsed      string `json:"model_used"`
	DurationMs     int64  `json:"duration_ms"`
	Trigger        string `json:"trigger"` // "first_turn" | "manual" | "after_clear"
	ErrorKind      string `json:"error_kind,omitempty"`
}

// BranchAdvisorSuggestedPayload carries signalling for
// KindBranchAdvisorSuggested (branch-as-subagent-recommendation WP08).
type BranchAdvisorSuggestedPayload struct {
	Confidence       float64  `json:"confidence"`
	Signals          []string `json:"signals"`
	MessageID        string   `json:"message_id"`
	SessionID        string   `json:"session_id"`
	RecommendationID string   `json:"recommendation_id"`
}

// BranchAdvisorAcceptedPayload carries signalling for
// KindBranchAdvisorAccepted.
type BranchAdvisorAcceptedPayload struct {
	Confidence       float64 `json:"confidence"`
	BranchSessionID  string  `json:"branch_session_id"`
	RecommendationID string  `json:"recommendation_id"`
}

// BranchAdvisorDismissedPayload carries signalling for
// KindBranchAdvisorDismissed. Scope is "message" | "session" |
// "project"; Reason is "no_thanks" | "dont_suggest_again" |
// "project_off".
type BranchAdvisorDismissedPayload struct {
	Scope            string `json:"scope"`
	Reason           string `json:"reason"`
	RecommendationID string `json:"recommendation_id,omitempty"`
}

// BranchReintegratedPayload carries signalling for
// KindBranchReintegrated.
type BranchReintegratedPayload struct {
	ParentSessionID   string `json:"parent_session_id"`
	BranchSessionID   string `json:"branch_session_id"`
	SummaryTokenCount int    `json:"summary_token_count"`
	WasEdited         bool   `json:"was_edited"`
}

// Marshal is a small convenience wrapper that builds an [Event] for any
// payload value with the current timestamp.
func Marshal(kind Kind, payload any, now time.Time) (Event, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return Event{}, err
	}
	return Event{Kind: kind, TS: now, Payload: raw}, nil
}

// Emit is a small convenience wrapper for callers that have a payload
// value but not a pre-built Event. Errors are returned to the caller —
// audit must never panic.
func Emit(ctx context.Context, em Emitter, kind Kind, payload any, now time.Time) error {
	if em == nil {
		return nil // emitter is optional; resolution can be silent in tests.
	}
	e, err := Marshal(kind, payload, now)
	if err != nil {
		return err
	}
	return em.Emit(ctx, e)
}
