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
	// KindContextUpdateAvailable signals that a context-pack update is
	// available (FR-017). Distinct from KindUpdateAvailable below, which
	// is the harness binary auto-update mission's "newer release found"
	// kind. Both are intentional — the strings differ
	// ("context.update_available" vs "update.available").
	KindContextUpdateAvailable Kind = "context.update_available"

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

	// Workflow-family audit kinds (workflows-01KQ8TDG WP11). These cover
	// the four lifecycle events the workflows RPC + engine emit:
	//
	//   KindWorkflowExecuted   — emitted on Run completion (success or
	//                            failure). Payload: WorkflowExecutedPayload.
	//   KindWorkflowSaved      — emitted on Save success.
	//                            Payload: WorkflowSavedPayload.
	//   KindWorkflowDeleted    — emitted on Delete success.
	//                            Payload: WorkflowDeletedPayload.
	//   KindWorkflowStepFailed — emitted per failed step inside a Run.
	//                            Payload: WorkflowStepFailedPayload.
	//
	// Per the privacy invariant, these payloads MUST NOT carry step
	// inputs, step outputs, or prompt bytes — only ids, kinds, and
	// classification metadata.
	KindWorkflowExecuted   Kind = "workflow.executed"
	KindWorkflowSaved      Kind = "workflow.saved"
	KindWorkflowDeleted    Kind = "workflow.deleted"
	KindWorkflowStepFailed Kind = "workflow.step_failed"
	// KindWorkflowNetworkFetch fires once per web_fetch / web_scrape step
	// that successfully completes a network request (WP05). The payload
	// carries the request hostname, HTTP status, and response byte count.
	// Full URL (which may contain auth tokens) and response body are NEVER
	// recorded (privacy invariant).
	KindWorkflowNetworkFetch Kind = "workflow.network_fetch"

	// Auto-update lifecycle kinds (auto-update mission, v0.4.0 WP06).
	// Six kinds mirror the Service lifecycle: every Check call,
	// transition false→true on Available, Download success, Apply
	// (emitted before the swap so it lands even if exec fails),
	// SkipVersion, and any classified failure across Check / Download
	// / Apply.
	//
	// Privacy invariant — payloads carry ONLY version strings, sizes,
	// and error class labels. Manifest URLs (which can carry signed
	// query tokens), manifest body, release notes, and download URLs
	// are NEVER emitted.
	KindUpdateChecked    Kind = "update.checked"
	KindUpdateAvailable  Kind = "update.available"
	KindUpdateDownloaded Kind = "update.downloaded"
	KindUpdateApplied    Kind = "update.applied"
	KindUpdateSkipped    Kind = "update.skipped"
	KindUpdateFailed     Kind = "update.failed"
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

// ContextUpdateAvailablePayload — FR-017. Carries signalling for
// KindContextUpdateAvailable (context-pack update available).
type ContextUpdateAvailablePayload struct {
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

// WorkflowExecutedPayload carries signalling for KindWorkflowExecuted
// (workflows-01KQ8TDG WP11). Emitted once per Run completion (success
// or failure). No step inputs / outputs / prompt bytes are recorded.
type WorkflowExecutedPayload struct {
	WorkflowID string `json:"workflow_id"`
	RunID      string `json:"run_id"`
	Status     string `json:"status"` // completed | failed | interrupted
	DurationMs int64  `json:"duration_ms"`
	StepCount  int    `json:"step_count"`
}

// WorkflowSavedPayload carries signalling for KindWorkflowSaved
// (workflows-01KQ8TDG WP11). Hash is the canonical sha256 hex digest
// the storage layer computes on the canonical YAML source.
type WorkflowSavedPayload struct {
	WorkflowID string `json:"workflow_id"`
	Version    int    `json:"version"`
	Hash       string `json:"hash"`
}

// WorkflowDeletedPayload carries signalling for KindWorkflowDeleted.
type WorkflowDeletedPayload struct {
	WorkflowID string `json:"workflow_id"`
}

// WorkflowStepFailedPayload carries signalling for
// KindWorkflowStepFailed. ErrorClass is the typed error category
// (e.g. "context_canceled", "step_validation", "runner_error") — not
// the raw error message, which can carry user inputs.
type WorkflowStepFailedPayload struct {
	WorkflowID string `json:"workflow_id"`
	RunID      string `json:"run_id"`
	StepID     string `json:"step_id"`
	StepKind   string `json:"step_kind"`
	ErrorClass string `json:"error_class"`
}

// ─── Auto-update payloads (auto-update mission, v0.4.0 WP06) ─────────
//
// Privacy invariant: every field below is either a version string, a
// size, a duration, a platform tuple, a boolean, or a typed error class
// label. Manifest URLs (which can carry signed query tokens), download
// URLs, manifest body bytes, and release-notes content are NEVER
// recorded.

// UpdateCheckedAttrs is emitted on every successful Service.Check
// return (background poll or manual). Took is the wall-clock manifest
// fetch + parse cost in milliseconds; ResultVersion is the manifest's
// advertised version (empty on a manifest fetch error — the failure
// path emits KindUpdateFailed instead, never UpdateChecked).
type UpdateCheckedAttrs struct {
	Channel       string `json:"channel"`
	ResultVersion string `json:"result_version,omitempty"`
	Took          int    `json:"took_ms"`
}

// UpdateAvailableAttrs fires on the false→true transition of
// Info.Available during a background poll. CurrentVersion is the
// running build's version; AvailableVersion is the manifest's newer
// version. Channel is "stable" or "prerelease".
type UpdateAvailableAttrs struct {
	CurrentVersion   string `json:"current_version"`
	AvailableVersion string `json:"available_version"`
	Channel          string `json:"channel"`
}

// UpdateDownloadedAttrs fires on Service.Download success. Bytes is
// the streamed payload size in bytes; Sha256Match is true (the success
// path implies a verified digest — the field is recorded so audit
// consumers can pivot on it explicitly without parsing free text).
type UpdateDownloadedAttrs struct {
	Version     string `json:"version"`
	Bytes       int64  `json:"bytes"`
	Sha256Match bool   `json:"sha256_match"`
}

// UpdateAppliedAttrs fires immediately BEFORE the platform Swap call
// in Service.ApplyAndRestart, so the event lands in the audit log even
// if the subsequent fork-exec fails. Platform is the GOOS/GOARCH tuple
// of the staged artifact.
type UpdateAppliedAttrs struct {
	FromVersion string `json:"from_version"`
	ToVersion   string `json:"to_version"`
	Platform    string `json:"platform"`
}

// UpdateSkippedAttrs fires on Service.SkipVersion success. Reason is a
// short opaque label ("user_clicked_skip" today; reserved for future
// "auto_skipped_due_to_signature_failure" etc).
type UpdateSkippedAttrs struct {
	Version string `json:"version"`
	Reason  string `json:"reason"`
}

// UpdateFailedAttrs fires on any classified failure from Check,
// Download, or Apply. Action is one of {"check","download","apply"}.
// ErrorClass is one of:
//
//	"network"          — DNS / connect / read-error / non-2xx HTTP
//	"sha_mismatch"     — staged bytes failed sha256 verification
//	"swap_failed"      — platform Swap returned an error
//	"manifest_invalid" — manifest body decoded to garbage / missing version
//	"other"            — fall-through bucket
//
// The raw error message is NEVER recorded (it can carry user inputs or
// URL fragments) — only the classification.
type UpdateFailedAttrs struct {
	Action     string `json:"action"`
	ErrorClass string `json:"error_class"`
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

// WorkflowNetworkFetchPayload carries signalling for
// KindWorkflowNetworkFetch (workflows-agentic-01KW2D3X WP05).
//
// Privacy invariant: only the request hostname, HTTP status code, and
// response size in bytes are recorded. The full URL (which can carry
// signed auth tokens), response body, and extraction output are NEVER
// included.
type WorkflowNetworkFetchPayload struct {
	WorkflowID string `json:"workflow_id"`
	RunID      string `json:"run_id"`
	StepID     string `json:"step_id"`
	StepKind   string `json:"step_kind"` // "web_fetch" | "web_scrape"
	// Hostname is the request hostname only — never the full URL.
	Hostname   string `json:"hostname"`
	Status     int    `json:"status"`
	Bytes      int    `json:"bytes"`
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
