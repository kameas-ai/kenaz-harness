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

	// KindMCPHealthChanged fires when an installed MCP recipe transitions
	// state (stopped → starting → running → restarting → failed).
	// Payload: MCPHealthChangedPayload.
	// (mcp-server-health-ui-01KQ8TD6 WP07)
	KindMCPHealthChanged Kind = "mcp.recipe.health_changed"

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

	// KindBranchCreated fires when a branch is created via either the
	// explicit "Branch from this turn" path or the implicit edit-and-resend
	// path (branching-ux-polish-01KQ8TD7 WP01). Payload:
	// BranchCreatedPayload.
	//
	// CreationPath discriminates:
	//   "explicit"    — user chose "Branch from this turn" in the menu.
	//   "edit_resend" — implicit fork from the edit-and-resend flow.
	KindBranchCreated Kind = "branch.created"

	// KindSlashCommandRun fires when a user-defined slash command is
	// dispatched via Dispatch.Run (user-slash-commands-01KQ8TD9 WP08).
	// Payload: SlashCommandRunPayload.
	//
	// Privacy invariant: rendered args summary is limited to arg names
	// and kind labels — no resolved values that may carry secrets.
	KindSlashCommandRun Kind = "slashcmd.run"

	// Elicitation-family audit kinds (ask-user-question-interactive-01KZNP3G WP09).
	//
	// Privacy invariant: these payloads MUST NOT carry question text,
	// option labels, option descriptions, answer values, or any preview
	// content — only ids, kinds, timing, and classification metadata.
	// The audit log is not the right place for user input (DIRECTIVE_001).
	//
	// KindElicitationRequest fires when the model invokes
	// kaneaz__ask_user_question and the ask is registered in the registry.
	// Payload: ElicitationRequestPayload.
	KindElicitationRequest Kind = "elicitation.request"

	// KindElicitationResult fires when the elicitation completes: the user
	// answered, cancelled, the ask was declined (non-interactive), or it
	// timed out. Payload: ElicitationResultPayload.
	KindElicitationResult Kind = "elicitation.result"

	// Cedar policy editor audit kinds (cedar-policy-editor-ui-01KQ8TD6 WP01).
	//
	// Privacy invariant: these payloads MUST NOT carry the policy source body
	// (which may contain information about the user's data-access patterns).
	// Only metadata — name, byte count, parse status — crosses the audit boundary.

	// KindPolicyFileSaved fires when SavePolicy writes a new or updated policy
	// file to disk. Payload: PolicyFileSavedPayload.
	KindPolicyFileSaved Kind = "policy.file_saved"

	// KindPolicyFileDeleted fires when DeletePolicy removes a user policy file.
	// Payload: PolicyFileDeletedPayload.
	KindPolicyFileDeleted Kind = "policy.file_deleted"

	// KindPolicyTemplateInstalled fires when InstallTemplate copies a shipped
	// Cedar template to the user's policy directory. Payload: PolicyTemplateInstalledPayload.
	KindPolicyTemplateInstalled Kind = "policy.template_installed"

	// KindLLMStructuredResponse fires once per structured-output call regardless
	// of validation outcome (structured-output-and-grammar-01KX5R8A FR-010).
	//
	// Privacy invariant: this payload MUST NOT carry the schema bytes, the
	// grammar bytes, or any portion of the model response body. Only the
	// schema SHA-256 hash (schema_hash), provider/model metadata, and
	// classification fields cross the audit boundary.
	KindLLMStructuredResponse Kind = "llm.structured.response"

	// KindProviderKeyRotated fires when a provider API key is successfully
	// rotated via TestAndRotateKey. The payload is ProviderKeyRotatedPayload.
	//
	// Privacy invariant: the payload MUST NOT carry key material, key length,
	// or key prefix. Only the four fields enumerated in ProviderKeyRotatedPayload
	// are permitted.
	// (provider-keychain-rotation-01KQ8TD9 WP04)
	KindProviderKeyRotated Kind = "provider.key_rotated"

	// KindSessionExport fires on every successful Sessions_Export call
	// (session-export-01NDFSEX05 WP01). The payload is SessionExportPayload.
	//
	// Privacy invariant: the output file path (which is user-chosen and
	// therefore a personal-data path) is NEVER recorded — only the basename
	// (filename without directory) is included. Message content, credential
	// bytes, and user-authored text are NEVER included.
	KindSessionExport Kind = "session.export"

	// ── Model fallback routing audit kinds (model-fallback-routing-01NDFSEX04) ──

	// KindFallbackAttempted fires each time the connector retry loop
	// re-issues a turn against a fallback chain entry. Emitted before the
	// hop is dispatched so the record lands even when the hop also fails.
	//
	// Privacy invariant: the payload MUST NOT carry prompt bytes, response
	// bytes, or credential material — only ids, reason, and attempt count.
	KindFallbackAttempted Kind = "adapter.fallback_attempted"

	// KindFallbackBlockedByPolicy fires when Cedar denies the
	// llm_fallback action for the active chain. The connector then
	// returns the primary error unchanged (fail-closed).
	//
	// Privacy invariant: same as KindFallbackAttempted.
	KindFallbackBlockedByPolicy Kind = "adapter.fallback_blocked_by_policy"

	// KindPanicRecovered fires when a goroutine-level panic is caught by
	// sentry.RecoverGoroutine. The panic is swallowed (the goroutine
	// continues or exits gracefully) and a Sentry event is captured.
	// Payload: PanicRecoveredPayload.
	//
	// Privacy invariant: the payload MUST NOT carry the panic value string
	// (which may contain user data) — only the goroutine label and a
	// redacted summary produced by the sentry redactor.
	// (sentry-error-monitoring-01KX5R8G WP02)
	KindPanicRecovered Kind = "process.panic_recovered"

	// KindAuditBulkPurgeExecuted fires when Audit_BulkPurge successfully
	// deletes a batch of events from the store (audit-log-enhancement
	// -01KX5R8F WP08). Payload: AuditBulkPurgeExecutedPayload.
	//
	// Privacy invariant: the payload carries only the event ID list and
	// the purge count — no payload bytes from the deleted events.
	KindAuditBulkPurgeExecuted Kind = "audit.bulk_purge_executed"

	// KindAuditBulkPurgeBlockedByPolicy fires when the Cedar engine denies
	// ActionAuditBulkPurge for the active principal (F-001 security fix).
	// Payload: AuditBulkPurgeBlockedByPolicyPayload.
	//
	// Privacy invariant: the payload MUST NOT carry the event_ids list
	// (which reveals what the caller was trying to delete). Only the
	// denial reason and the attempt count are recorded.
	KindAuditBulkPurgeBlockedByPolicy Kind = "audit.bulk_purge_blocked_by_policy"

	// ── Fleet config-pull audit kinds (fleet-config-pull-01NDFSEX10 WP05) ──

	// KindFleetConfigApplied fires after a fleet config bundle has been fully
	// verified and all sections applied successfully.
	//
	// Privacy invariant: the payload MUST NOT carry the bundle contents
	// (cedar rules, weight URLs, model IDs). Only the bundle_id, issued_at,
	// and section names that were present are recorded.
	KindFleetConfigApplied Kind = "fleet.config.applied"

	// KindFleetConfigSignatureRejected fires when Verify() returns an error
	// for an incoming bundle (ErrInvalidSignature or ErrBundleIDNonMonotonic).
	// The previous applied bundle is kept; no config is updated.
	//
	// Privacy invariant: no bundle contents are recorded — only bundle_id
	// and the error classification.
	KindFleetConfigSignatureRejected Kind = "fleet.config.signature_rejected"

	// KindFleetConfigPartialFailure fires when the bundle passes signature
	// verification but one or more section apply calls return an error.
	// The bundle is still ACKed and the bundle_id advances.
	//
	// Privacy invariant: same as KindFleetConfigApplied — no bundle contents.
	KindFleetConfigPartialFailure Kind = "fleet.config.partial_failure"

	// ── Fleet OTel archival audit (fleet-otel-archival-01NDFSEX11 WP07) ──

	// KindFleetTelemetrySent fires once per successful flush to the fleet
	// endpoint (fleet-otel-archival-01NDFSEX11 WP07). The payload carries
	// only non-PII rollup counts for the batch.
	//
	// Privacy invariant: the payload MUST NOT carry span names, attribute
	// values, log record bodies, metric label values, or the device key
	// fingerprint. Only the batch ULID and the per-signal counts are
	// permitted.
	KindFleetTelemetrySent Kind = "fleet.telemetry_sent"

	// ── Fleet emergency lockdown audit kinds
	// (fleet-emergency-lockdown-01NDFSEX12 WP03).

	// KindFleetLockdownReceived fires when the lockdown Watcher transitions
	// to locked state. Payload: FleetLockdownReceivedPayload.
	KindFleetLockdownReceived Kind = "fleet.lockdown_received"

	// KindFleetLockdownCleared fires when the lockdown Watcher transitions
	// from locked to unlocked state. No payload fields beyond the kind.
	KindFleetLockdownCleared Kind = "fleet.lockdown_cleared"

	// KindFleetLockdownBypassUsed fires once per process start when the
	// HARNESS_FLEET_LOCKDOWN_BYPASS=1 env var is set, regardless of whether
	// a lockdown is currently in effect. Payload: FleetLockdownBypassPayload.
	KindFleetLockdownBypassUsed Kind = "fleet.lockdown_bypass_used"

	// ── Fleet context-graph sync audit kinds
	// (fleet-context-graph-sync-01NDFSEX17 WP05)

	// KindFleetContextPublished fires when a local context entry is successfully
	// pushed to the fleet context graph (team_shared or org_shared). The payload
	// carries only the node ID, classification, and version — no body content.
	//
	// Privacy invariant: body, title, and metadata are NEVER included.
	KindFleetContextPublished Kind = "fleet.context_published"

	// KindFleetContextPulled fires once per successful PullDelta call that
	// returns at least one node. The payload is a batch summary (node count,
	// cursor) — no individual entry contents.
	//
	// Privacy invariant: entry bodies and titles are NEVER included.
	KindFleetContextPulled Kind = "fleet.context_pulled"

	// KindFleetContextPromoted fires when a team_shared entry is successfully
	// elevated to org_shared via the Promote call. The payload carries the node
	// ID and the new classification only.
	//
	// Privacy invariant: entry body and title are NEVER included.
	KindFleetContextPromoted Kind = "fleet.context_promoted"
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

// MCPHealthChangedPayload carries signalling for KindMCPHealthChanged
// (mcp-server-health-ui-01KQ8TD6 WP07). Emitted on every lifecycle
// state transition of an installed recipe. Carries only the recipe id
// and the new + previous state strings — no tool inputs / outputs or
// credential material (DIRECTIVE_001 / privacy invariant).
type MCPHealthChangedPayload struct {
	RecipeID        string `json:"recipe_id"`
	PreviousState   string `json:"previous_state"`
	NewState        string `json:"new_state"`
	RestartAttempts int    `json:"restart_attempts"`
	// ErrorClass is the typed error category when transitioning to
	// "failed" state: "ping_timeout", "crash", "unknown". Empty on
	// non-failed transitions.
	ErrorClass string `json:"error_class,omitempty"`
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

// BranchCreatedPayload carries signalling for KindBranchCreated
// (branching-ux-polish-01KQ8TD7 WP01). Emitted from both creation
// paths so the audit view can show two distinct events after the
// manual acceptance smoke.
//
// Privacy invariant: ParentMessageID is a stable opaque id (no content
// bytes). Neither session names nor message bodies are included here.
type BranchCreatedPayload struct {
	ParentSessionID string `json:"parent_session_id"`
	ParentMessageID string `json:"parent_message_id,omitempty"`
	BranchSessionID string `json:"branch_session_id"`
	// CreationPath is "explicit" | "edit_resend".
	CreationPath string `json:"creation_path"`
}

// SlashCommandRunPayload carries signalling for KindSlashCommandRun
// (user-slash-commands-01KQ8TD9 WP08). Emitted once per Dispatch.Run
// call (success or failure).
//
// Privacy invariant: ArgNames lists only the argument names provided by
// the caller, NEVER their resolved values (which may contain secrets,
// file paths, or user-authored text). The rendered template body, tool
// outputs, and session messages are never included.
type SlashCommandRunPayload struct {
	Name           string   `json:"name"`
	Scope          string   `json:"scope"`           // "global" | "project"
	Kind           string   `json:"kind"`            // "text" | "tool" | "prompt"
	ModelInvokable bool     `json:"model_invokable"` // true if the command is AI-invokable
	ArgNames       []string `json:"arg_names"`       // names of args supplied (not values)
	DispatchedTool string   `json:"dispatched_tool,omitempty"` // tool name for kind=tool
	SessionID      string   `json:"session_id"`
	ProjectID      string   `json:"project_id,omitempty"`
	Success        bool     `json:"success"`
	ErrorClass     string   `json:"error_class,omitempty"` // classified error label, not raw msg
}

// ElicitationRequestPayload carries signalling for KindElicitationRequest
// (ask-user-question-interactive-01KZNP3G WP09).
//
// Privacy invariant: question text, option labels/descriptions, preview
// content, and any user-supplied data are NEVER included — only the ask
// id, session, kind, mode, and question count.
type ElicitationRequestPayload struct {
	// AskID is the registry-assigned identifier for this elicitation.
	AskID string `json:"ask_id"`
	// SessionID is the session that invoked the ask.
	SessionID string `json:"session_id"`
	// Kind is the question kind (radio, checkbox, text, number, slider, date, file).
	Kind string `json:"kind"`
	// Mode is "blocking" or "deferred".
	Mode string `json:"mode"`
	// QuestionCount is the number of questions in the batch (1–4).
	QuestionCount int `json:"question_count"`
	// HasPreviews indicates whether any question carries a preview pane.
	HasPreviews bool `json:"has_previews"`
	// TemplateID is the template name when invoked via template, empty otherwise.
	TemplateID string `json:"template_id,omitempty"`
}

// ElicitationResultPayload carries signalling for KindElicitationResult
// (ask-user-question-interactive-01KZNP3G WP09).
//
// Privacy invariant: answer values, question text, and option content
// are NEVER included — only the ask id, outcome classification, and timing.
type ElicitationResultPayload struct {
	// AskID matches the originating ElicitationRequestPayload.
	AskID string `json:"ask_id"`
	// SessionID is the session that owned this ask.
	SessionID string `json:"session_id"`
	// Outcome is "answered" | "cancelled" | "declined" | "timed_out".
	Outcome string `json:"outcome"`
	// DeclineReason is populated when Outcome=="declined":
	// "non_interactive" | "cedar_denied" | "too_many_pending" | "hook_blocked" | "expired".
	DeclineReason string `json:"decline_reason,omitempty"`
	// TimeToAnswerMs is the wall-clock latency from ask registration to
	// result receipt. Zero when Outcome is "declined" synchronously.
	TimeToAnswerMs int64 `json:"time_to_answer_ms"`
	// Deferred is true when mode was "deferred" and the answer arrived
	// asynchronously (potentially in a later session turn).
	Deferred bool `json:"deferred"`
}

// PolicyFileSavedPayload carries signalling for KindPolicyFileSaved
// (cedar-policy-editor-ui-01KQ8TD6 WP01).
//
// Privacy invariant: Source body is NEVER included — only the filename,
// size in bytes, and whether the parse succeeded.
type PolicyFileSavedPayload struct {
	// Name is the policy filename (e.g. "my-policy.cedar").
	Name string `json:"name"`
	// Bytes is the file size written to disk.
	Bytes int `json:"bytes"`
	// ParseOK is true when the source parsed cleanly before write.
	ParseOK bool `json:"parse_ok"`
}

// PolicyFileDeletedPayload carries signalling for KindPolicyFileDeleted.
// Only the filename is recorded — no content.
type PolicyFileDeletedPayload struct {
	// Name is the policy filename that was deleted.
	Name string `json:"name"`
}

// PolicyTemplateInstalledPayload carries signalling for KindPolicyTemplateInstalled.
// Records the template name, destination filename, and byte count.
// No source body is recorded.
type PolicyTemplateInstalledPayload struct {
	// Template is the shipped template filename that was copied
	// (e.g. "filesystem-full-recommended.cedar").
	Template string `json:"template"`
	// Dest is the destination filename in the user's policy directory.
	Dest string `json:"dest"`
	// Bytes is the number of bytes written.
	Bytes int `json:"bytes"`
}

// LLMStructuredResponsePayload carries signalling for KindLLMStructuredResponse
// (structured-output-and-grammar-01KX5R8A FR-010).
//
// Privacy invariant: schema bytes, grammar bytes, and any portion of the model
// response body MUST NOT appear in this struct. schema_hash is a SHA-256 hex
// digest of the schema bytes (empty string for grammar/json mode where no schema
// was supplied). The raw response and schema are never included.
type LLMStructuredResponsePayload struct {
	// Provider is the adapter kind (e.g. "anthropic", "openai", "openrouter", "bedrock").
	Provider string `json:"provider"`
	// Model is the model identifier used for the call.
	Model string `json:"model"`
	// FormatMode is the ResponseFormat.Mode value ("json" | "json_schema" | "grammar").
	FormatMode string `json:"format_mode"`
	// SchemaHash is the SHA-256 hex digest of the JSON schema bytes (Mode="json_schema").
	// Empty for Mode="json" and Mode="grammar" where no schema was supplied.
	SchemaHash string `json:"schema_hash,omitempty"`
	// ValidationOutcome classifies the result:
	//   "passed"       — first attempt validated successfully.
	//   "retry_passed" — first attempt failed, retry succeeded.
	//   "failed"       — all attempts failed validation.
	//   "skipped"      — Mode="json" or Mode="grammar" (no schema to validate).
	ValidationOutcome string `json:"validation_outcome"`
	// Attempts is the number of LLM calls made (1 = no retry, 2 = one retry fired).
	Attempts int `json:"attempts"`
	// InputTokens and OutputTokens are from the terminal Usage record.
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// ProviderKeyRotatedPayload carries the signalling for KindProviderKeyRotated.
//
// Privacy invariant: EXACTLY these four fields and no others. No key
// material, key length, key prefix, or redacted key view may appear.
// (provider-keychain-rotation-01KQ8TD9 WP04)
type ProviderKeyRotatedPayload struct {
	Provider  string    `json:"provider"`   // adapter kind ("anthropic", "openai", …)
	ProfileID string    `json:"profile_id"` // the profile whose key was rotated
	RotatedAt time.Time `json:"rotated_at"` // wall clock at rotation success
	Source    string    `json:"source"`     // "manual" | "inline-toast"
}

// SessionExportPayload carries the signalling for KindSessionExport
// (session-export-01NDFSEX05 WP01). Emitted on every successful export call.
//
// Privacy invariant:
//   - OutputBasename is the filename only (filepath.Base), NEVER the full path.
//   - Message content, credential bytes, and prompt/response text are NEVER
//     included (DIRECTIVE_001). The audit log is not a transcript copy.
type SessionExportPayload struct {
	// SessionID is the exported session's primary key.
	SessionID string `json:"session_id"`
	// Format is "markdown" or "json".
	Format string `json:"format"`
	// OutputBasename is filepath.Base(outputPath) — the filename only, no dir.
	OutputBasename string `json:"output_basename"`
	// ByteCount is the number of bytes written to the main export file.
	ByteCount int64 `json:"byte_count"`
}

// FallbackAttemptedPayload carries the signalling for KindFallbackAttempted
// (model-fallback-routing-01NDFSEX04 WP02/WP03).
//
// Privacy invariant: MUST NOT carry prompt bytes, response content, or
// credential material. Only ids, routing metadata, and attempt count.
type FallbackAttemptedPayload struct {
	// SessionID is the session the turn belongs to.
	SessionID string `json:"session_id,omitempty"`
	// From is the (profileID, model) of the failed primary call.
	FromProfile string `json:"from_profile"`
	FromModel   string `json:"from_model"`
	// To is the (profileID, model) the hop resolves to.
	ToProfile string `json:"to_profile"`
	ToModel   string `json:"to_model"`
	// Reason is the TriggerCondition string that matched
	// (e.g. "error_5xx", "error_429").
	Reason string `json:"reason"`
	// Attempt is the 1-based hop index within the chain walk.
	Attempt int `json:"attempt"`
}

// FallbackBlockedByPolicyPayload carries the signalling for
// KindFallbackBlockedByPolicy (model-fallback-routing-01NDFSEX04 WP03).
//
// Privacy invariant: same as FallbackAttemptedPayload.
type FallbackBlockedByPolicyPayload struct {
	// SessionID is the session the turn belongs to.
	SessionID string `json:"session_id,omitempty"`
	// ChainID is the chain whose access Cedar denied.
	ChainID string `json:"chain_id"`
	// Reason is the Cedar denial reason string.
	Reason string `json:"reason"`
}

// PanicRecoveredPayload carries the signalling for KindPanicRecovered.
// (sentry-error-monitoring-01KX5R8G WP02)
//
// Privacy invariant: the raw panic value string is NEVER included — only the
// goroutine label and a redacted summary string produced by the sentry package.
type PanicRecoveredPayload struct {
	// GoroutineLabel is the human-readable label passed to
	// sentry.RecoverGoroutine (e.g. "scheduler.tick").
	GoroutineLabel string `json:"goroutine_label"`
	// Summary is a redacted one-line description of the panic (produced by
	// sentry.RedactString — never the raw panic value).
	Summary string `json:"summary"`
}

// AuditBulkPurgeExecutedPayload carries the signalling for
// KindAuditBulkPurgeExecuted (audit-log-enhancement-01KX5R8F WP08).
//
// Privacy invariant: the EventIDs are the bare ULID strings of the
// purged rows; no payload bytes from the deleted events are included.
type AuditBulkPurgeExecutedPayload struct {
	// EventIDs is the list of purged event_id values.
	EventIDs []string `json:"event_ids"`
	// PurgedCount is len(EventIDs); redundant for fast aggregation.
	PurgedCount int `json:"purged_count"`
}

// AuditBulkPurgeBlockedByPolicyPayload carries the signalling for
// KindAuditBulkPurgeBlockedByPolicy (F-001 security fix).
//
// Privacy invariant: the event_ids list is NOT included — it reveals
// what the caller was attempting to delete. Only the denial reason and
// the count of ids in the attempted purge are recorded.
type AuditBulkPurgeBlockedByPolicyPayload struct {
	// AttemptCount is the number of event IDs in the blocked purge request.
	AttemptCount int `json:"attempt_count"`
	// Reason is the Cedar denial reason string (safe to log).
	Reason string `json:"reason"`
}

// ── Fleet config-pull payload types (fleet-config-pull-01NDFSEX10 WP05) ────

// FleetConfigAppliedPayload is the audit payload for KindFleetConfigApplied.
//
// Privacy invariant: no bundle contents (cedar rules, weight URLs, model IDs)
// are recorded — only the bundle_id, issuance time, and which sections were
// present in the bundle.
type FleetConfigAppliedPayload struct {
	// BundleID is the bundle_id of the applied bundle.
	BundleID int64 `json:"bundle_id"`
	// IssuedAt is the server-side issuance time of the applied bundle.
	IssuedAt time.Time `json:"issued_at"`
	// Sections is the list of non-empty sections that were present:
	// "cedar_delta", "mcp_allowlist", "model_prefs", "kameas_ml_weight_urls".
	Sections []string `json:"sections"`
}

// FleetConfigSignatureRejectedPayload is the audit payload for
// KindFleetConfigSignatureRejected. Records what was received but rejected.
//
// Privacy invariant: no bundle contents are included.
type FleetConfigSignatureRejectedPayload struct {
	// BundleID is the bundle_id of the rejected bundle.
	BundleID int64 `json:"bundle_id"`
	// ErrorKind is the typed rejection reason:
	// "invalid_signature", "non_monotonic_id", "key_not_configured".
	ErrorKind string `json:"error_kind"`
}

// FleetConfigPartialFailurePayload is the audit payload for
// KindFleetConfigPartialFailure. The bundle was verified and applied but
// at least one section apply returned an error.
//
// Privacy invariant: no bundle contents are included.
type FleetConfigPartialFailurePayload struct {
	// BundleID is the bundle_id of the partially-applied bundle.
	BundleID int64 `json:"bundle_id"`
	// FailedSections lists the section names that returned errors.
	FailedSections []string `json:"failed_sections"`
}

// FleetTelemetrySentPayload carries the non-PII rollup for
// KindFleetTelemetrySent (fleet-otel-archival-01NDFSEX11 WP07).
//
// Privacy invariant: no span names, attribute values, log record bodies,
// metric label values, or key fingerprints are included — only the batch
// ULID and per-signal item counts.
type FleetTelemetrySentPayload struct {
	// BatchID is the ULID that was set in the POST body's batch_id field.
	BatchID string `json:"batch_id"`
	// SpanCount is the number of spans included in the batch.
	SpanCount int `json:"span_count"`
	// MetricCount is the number of metric data-points included in the batch.
	MetricCount int `json:"metric_count"`
	// LogCount is the number of log records included in the batch (0 for
	// aggregate consent which omits log records entirely).
	LogCount int `json:"log_count"`
}

// ─── Fleet emergency lockdown payloads (fleet-emergency-lockdown-01NDFSEX12) ───

// FleetLockdownReceivedPayload carries the signalling for
// KindFleetLockdownReceived. Reason is the admin-supplied reason string
// from the fleet server (may be empty). No user-input or session data
// is included.
type FleetLockdownReceivedPayload struct {
	// Reason is the admin-supplied reason from the fleet lockdown event.
	// Empty when the server did not include a reason.
	Reason string `json:"reason,omitempty"`
}

// FleetLockdownBypassPayload carries the signalling for
// KindFleetLockdownBypassUsed. ProcessID allows correlating bypass
// events across restart cycles. No user-input or session data is
// included.
type FleetLockdownBypassPayload struct {
	// EnvVar is the name of the bypass environment variable that was set.
	// Always "HARNESS_FLEET_LOCKDOWN_BYPASS" for now; field exists so
	// future bypass mechanisms are distinguishable in the audit log.
	EnvVar string `json:"env_var"`
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

// ── Fleet context-graph sync payload types
// (fleet-context-graph-sync-01NDFSEX17 WP05)

// FleetContextPublishedPayload is the audit payload for KindFleetContextPublished.
//
// Privacy invariant: body, title, and metadata are NEVER included.
type FleetContextPublishedPayload struct {
	// NodeID is the stable UUID of the published node.
	NodeID string `json:"node_id"`
	// Classification is "team_shared" or "org_shared".
	Classification string `json:"classification"`
	// Version is the version sent to the server.
	Version int `json:"version"`
}

// FleetContextPulledPayload is the audit payload for KindFleetContextPulled.
// Emitted once per PullDelta call that returns at least one node.
//
// Privacy invariant: entry bodies and titles are NEVER included.
type FleetContextPulledPayload struct {
	// NodeCount is the number of nodes received in this pull batch.
	NodeCount int `json:"node_count"`
	// TombstoneCount is the number of those nodes that were soft-deleted.
	TombstoneCount int `json:"tombstone_count"`
	// Cursor is the RFC3339Nano timestamp advanced after this pull.
	Cursor string `json:"cursor"`
}

// FleetContextPromotedPayload is the audit payload for KindFleetContextPromoted.
//
// Privacy invariant: entry body and title are NEVER included.
type FleetContextPromotedPayload struct {
	// NodeID is the stable UUID of the promoted node.
	NodeID string `json:"node_id"`
	// ToClassification is always "org_shared" in v0.
	ToClassification string `json:"to_classification"`
}
