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
	"log/slog"
	"time"

	pack "github.com/kameas-ai/kenaz-harness/core/context/pack"
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
	// kenaz__ask_user_question and the ask is registered in the registry.
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

	// ── Fleet share-and-sync audit kinds
	// (fleet-share-and-sync-01NDFSEX14).

	// KindFleetPolicyPublished fires when an admin publishes a Cedar rule
	// bundle to the team via POST /api/v1/cedar-policy/publish.
	// Payload: FleetPolicyPublishedPayload.
	KindFleetPolicyPublished Kind = "fleet.policy_published"

	// KindFleetCatalogPublished fires when a user publishes a workflow,
	// agent pack, or bundle to the team catalog.
	// Payload: FleetCatalogPublishedPayload.
	KindFleetCatalogPublished Kind = "fleet.catalog_published"

	// ── Fleet skills (fleet-skills-sync-01NDFSEX18 WP07) ────────────────────

	// KindFleetSkillPublished fires when the user successfully publishes a
	// user slash-command as a skill to the fleet catalog.
	// Payload: FleetSkillPublishedPayload.
	KindFleetSkillPublished Kind = "fleet.skill_published"

	// KindFleetSkillInstalled fires when a skill is installed from the fleet
	// catalog (pull-down) and live-registered in the registry.
	// Payload: FleetSkillInstalledPayload.
	KindFleetSkillInstalled Kind = "fleet.skill_installed"

	// KindFleetSkillUninstalled fires when a skill is removed from the local
	// store and unregistered from the registry.
	// Payload: FleetSkillUninstalledPayload.
	KindFleetSkillUninstalled Kind = "fleet.skill_uninstalled"

	// ── ACP envelope audit kind (acp-orchestration-integration-01NDFSEX06) ──

	// KindACPEnvelope fires once per ACP envelope exchanged (sent or received).
	// Payload: ACPEnvelopePayload.
	//
	// Privacy invariant: the payload MUST NOT carry the envelope body or any
	// portion of the turn payload — only the envelope id, peer id, transport,
	// direction, outcome, byte counts, and error code.
	KindACPEnvelope Kind = "acp.envelope"

	// ── Fleet context sync audit kinds (fleet-context-sync-01NDFSEX15) ──────

	// KindFleetSessionSyncEnabled fires when a user enables fleet sync for a
	// session. Payload: FleetSessionSyncPayload.
	//
	// Privacy invariant: session name and message content are NEVER included.
	// Only session_id and stream_id cross the audit boundary.
	KindFleetSessionSyncEnabled Kind = "fleet.session_sync_enabled"

	// KindFleetSessionSyncDisabled fires when fleet sync is disabled for a
	// session (toggle off). Payload: FleetSessionSyncPayload.
	KindFleetSessionSyncDisabled Kind = "fleet.session_sync_disabled"

	// KindFleetSessionSyncResumed fires when a session is resumed from fleet
	// (replay on open). Payload: FleetSessionSyncResumedPayload.
	//
	// Privacy invariant: event count only; no event content.
	KindFleetSessionSyncResumed Kind = "fleet.session_sync_resumed"

	// KindFleetSessionSyncRemoteDeleted fires when the user explicitly purges
	// a session's events from fleet. Payload: FleetSessionSyncPayload.
	KindFleetSessionSyncRemoteDeleted Kind = "fleet.session_sync_remote_deleted"

	// KindFleetProjectSyncEnabled fires when project sync is toggled on.
	// Payload: FleetProjectSyncPayload.
	KindFleetProjectSyncEnabled Kind = "fleet.project_sync_enabled"

	// KindFleetProjectSyncDisabled fires when project sync is toggled off.
	// Payload: FleetProjectSyncPayload.
	KindFleetProjectSyncDisabled Kind = "fleet.project_sync_disabled"

	// KindFleetProjectSyncRemoteDeleted fires when project events are purged
	// from fleet. Payload: FleetProjectSyncPayload.
	KindFleetProjectSyncRemoteDeleted Kind = "fleet.project_sync_remote_deleted"

	// KindFleetSessionSharedOutbound fires when a session is shared with a
	// teammate (sender side). Payload: FleetSessionHandoffPayload.
	//
	// Privacy invariant: session name and message content are NEVER included.
	KindFleetSessionSharedOutbound Kind = "fleet.session_shared_outbound"

	// KindFleetSessionSharedInbound fires when a shared session is accepted
	// by the recipient. Payload: FleetSessionHandoffPayload.
	KindFleetSessionSharedInbound Kind = "fleet.session_shared_inbound"

	// ── Fleet audit-archival kinds (fleet-audit-archival-01NDFSEX13) ─────────

	// KindFleetAuditChainBreak fires when the pre-send hash-chain verifier
	// detects a break in the local audit log. Archival hard-stops; the
	// operator must investigate and manually advance the cursor past the
	// gap (losing those events from the off-device archive). Payload:
	// FleetAuditChainBreakPayload.
	//
	// Privacy invariant: the payload MUST NOT carry event payloads or any
	// user-authored content — only IDs, the event_id where the break was
	// detected, and an error classification.
	KindFleetAuditChainBreak Kind = "fleet.audit_chain_break"

	// KindFleetAuditArchived fires once per successfully ACK'd batch of
	// audit events sent to the fleet immudb endpoint. The payload carries
	// only counts and the cursor range — no event payloads.
	// Payload: FleetAuditArchivedPayload.
	KindFleetAuditArchived Kind = "fleet.audit_archived"

	// KindFleetAuditChainSkipped fires when an operator manually advances
	// the archive cursor past a chain-break gap (explicit recovery action).
	// Payload: FleetAuditChainSkippedPayload.
	KindFleetAuditChainSkipped Kind = "fleet.audit_chain_skipped"

	// KindFleetAuditRetentionSwept fires once per successful retention sweep
	// pass that deletes locally-retained audit rows that are both ACK'd and
	// older than the retention window. Payload: FleetAuditRetentionSweptPayload.
	KindFleetAuditRetentionSwept Kind = "fleet.audit_retention_swept"

	// ── Context-bootstrap audit kind (context-bootstrap-harness-integration) ──

	// KindContextBootstrapRun fires once per bootstrap-run lifecycle event
	// (started / completed / failed / resumed). Payload:
	// ContextBootstrapRunPayload.
	//
	// Privacy invariant: the payload carries ONLY the run id, phase, outcome,
	// and aggregate counts (connectors, nodes). It NEVER carries extracted node
	// bodies, titles, source content, or any third-party credential material.
	KindContextBootstrapRun Kind = "context.bootstrap_run"

	// ── Confirm-each audit kinds (confirm-each-enforcement-01PMAG05 WP05) ──

	// KindToolConfirmDecision fires ONCE for every `confirm_each` verdict
	// the dispatch path resolves, on every path it can resolve by:
	// prompted approve, prompted deny, session-grant hit, persisted-grant
	// hit, autonomy skip-set skip, headless-policy application, and
	// toggle-off auto-allow. Payload: ToolConfirmDecisionPayload.
	//
	// The completeness matters more than the individual records. The
	// defect this mission repaired was a security control that decided
	// silently; a decision path with no audit record is the same defect
	// wearing a different hat, so the WP06 test asserts that every value
	// of ToolConfirmPath is reachable and emitted.
	//
	// Privacy invariant: the payload carries ids, names, the classified
	// family, the path, and the outcome. It MUST NOT carry tool argument
	// values, an args summary, prompt bytes, or tool output. The server
	// and tool NAMES are recorded (they are the subject of the decision);
	// nothing derived from the arguments is.
	KindToolConfirmDecision Kind = "tool.confirm_decision"

	// KindToolConfirmGrantWritten fires when the user takes the
	// visually-separated "always allow" action and a DURABLE allow rule
	// is written for (server, tool). Payload:
	// ToolConfirmGrantWrittenPayload.
	//
	// Distinct from KindToolConfirmDecision because persisting is a
	// second, deliberate act with a different blast radius: the decision
	// record says "this call was approved", this one says "future calls
	// will not be asked about". Emitted on failure too (Written=false)
	// so a silent write failure cannot masquerade as a grant.
	KindToolConfirmGrantWritten Kind = "tool.confirm_grant_written"
)

// ToolConfirmPath names which branch of the confirm-each dispatch path
// produced a decision. Values are stable wire strings.
type ToolConfirmPath string

const (
	// ToolConfirmPathPrompted — the user was asked and answered.
	ToolConfirmPathPrompted ToolConfirmPath = "prompted"

	// ToolConfirmPathSessionGrant — a prior "allow for this session"
	// answer covered this call; no prompt was shown.
	ToolConfirmPathSessionGrant ToolConfirmPath = "session_grant"

	// ToolConfirmPathPersistedGrant — a durable "always allow" rule
	// covered this call; no prompt was shown.
	ToolConfirmPathPersistedGrant ToolConfirmPath = "persisted_grant"

	// ToolConfirmPathSkipSet — the autonomy posture's prompt-skip set
	// covered the tool's family (autoApproveFamilies, or the
	// destructiveActionPosture: cedar-only extension). Family names the
	// classified family so an operator can see WHICH grant applied
	// rather than only that one did.
	ToolConfirmPathSkipSet ToolConfirmPath = "skip_set"

	// ToolConfirmPathHeadlessPolicy — no prompt channel existed and the
	// deployment's confirm_each_headless policy decided.
	ToolConfirmPathHeadlessPolicy ToolConfirmPath = "headless_policy"

	// ToolConfirmPathToggleOff — Settings.ConfirmEachEnabled() is false,
	// so the prompt was never offered and the verdict behaved as
	// auto-allow (FR-006).
	ToolConfirmPathToggleOff ToolConfirmPath = "toggle_off"
)

// AllToolConfirmPaths is the canonical list. WP06's coverage test walks
// it, so a new path added without an audit call site fails the build's
// intent rather than shipping a silent branch.
var AllToolConfirmPaths = []ToolConfirmPath{
	ToolConfirmPathPrompted,
	ToolConfirmPathSessionGrant,
	ToolConfirmPathPersistedGrant,
	ToolConfirmPathSkipSet,
	ToolConfirmPathHeadlessPolicy,
	ToolConfirmPathToggleOff,
}

// ToolConfirmDecisionPayload is the KindToolConfirmDecision payload.
//
// Privacy invariant: no argument values, no args summary, no tool
// output. Reason is the RESOLVER's or the user's short reason string,
// never a rendering of the call.
type ToolConfirmDecisionPayload struct {
	SessionID string `json:"session_id"`
	CallID    string `json:"call_id,omitempty"`
	BatchID   string `json:"batch_id,omitempty"`

	// Server and Tool are the bare names the decision was about.
	Server string `json:"server"`
	Tool   string `json:"tool"`

	// Family is the classified tool family (toolloop.ClassifyToolFamily).
	// Always populated — including "unknown", which is the value that
	// explains why an unclassifiable tool prompted under a permissive
	// posture.
	Family string `json:"family"`

	// Path names which branch decided. One of ToolConfirmPath.
	Path ToolConfirmPath `json:"path"`

	// Approved is the outcome: true when the call dispatched.
	Approved bool `json:"approved"`

	// RememberSession / Persisted record what the user asked to carry
	// forward, when the path was "prompted".
	RememberSession bool `json:"remember_session,omitempty"`
	Persisted       bool `json:"persisted,omitempty"`

	// Reason is a short classification string ("user denied",
	// "dismissed", "headless policy: deny", the resolver's reason). Not
	// free-form user input and never tool arguments.
	Reason string `json:"reason,omitempty"`
}

// ToolConfirmGrantWrittenPayload is the KindToolConfirmGrantWritten
// payload. GrantID is the revocation handle the Settings grants list
// round-trips (the .cedar filename on the desktop chassis).
type ToolConfirmGrantWrittenPayload struct {
	SessionID string `json:"session_id,omitempty"`
	Server    string `json:"server"`
	Tool      string `json:"tool"`
	GrantID   string `json:"grant_id,omitempty"`

	// Written is false when the persist was requested but the write
	// failed; Error then carries the failure class. The call itself was
	// still approved — a failed persist must not read as a granted one.
	Written bool   `json:"written"`
	Error   string `json:"error,omitempty"`
}

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
	EntryName  string       `json:"entry_name"`
	Winner     pack.Layer   `json:"winner"`
	Loser      pack.Layer   `json:"loser"`
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
	Hostname string `json:"hostname"`
	Status   int    `json:"status"`
	Bytes    int    `json:"bytes"`
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
	Scope          string   `json:"scope"`                     // "global" | "project"
	Kind           string   `json:"kind"`                      // "text" | "tool" | "prompt"
	ModelInvokable bool     `json:"model_invokable"`           // true if the command is AI-invokable
	ArgNames       []string `json:"arg_names"`                 // names of args supplied (not values)
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

// FleetPolicyPublishedPayload carries the signalling for KindFleetPolicyPublished.
// Privacy invariant: rule source bytes are NEVER included — only the rule_id
// and author are emitted.
type FleetPolicyPublishedPayload struct {
	// RuleID is the unique identifier of the published Cedar rule.
	RuleID string `json:"rule_id"`
	// Author is the identity of the publishing admin (email or user_id).
	Author string `json:"author,omitempty"`
}

// FleetCatalogPublishedPayload carries the signalling for
// KindFleetCatalogPublished. Privacy invariant: payload bytes are NEVER
// included — only catalog metadata.
type FleetCatalogPublishedPayload struct {
	// CatalogID is the server-assigned catalog item ID.
	CatalogID string `json:"catalog_id"`
	// Kind is the item kind (workflow, agent_pack, bundle).
	Kind string `json:"kind"`
	// Slug is the human-readable catalog slug.
	Slug string `json:"slug,omitempty"`
	// Version is the published SemVer string.
	Version string `json:"version,omitempty"`
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

// MustEmit is the WARN-on-error wrapper for fire-and-forget audit call sites
// (silent-failure-elimination-QQ5GXW50 WP05 / FR-005).
//
// Call sites that previously wrote `_ = audit.Emit(...)` should migrate to
// `audit.MustEmit(ctx, em, kind, payload, now)` so a marshal or emitter
// failure is logged rather than silently dropped.  The audit trail is the
// canonical record (C-005); a silent omission is worse than a loud warning.
//
// MustEmit never panics and never returns an error — it is designed for
// sites where surfacing the error to the user is either impossible or
// undesired (the primary operation succeeded; the audit trail is secondary).
// For call sites where the primary operation depends on the audit trail
// succeeding, use Emit and propagate the error normally.
func MustEmit(ctx context.Context, em Emitter, kind Kind, payload any, now time.Time) {
	if err := Emit(ctx, em, kind, payload, now); err != nil {
		slog.WarnContext(ctx, "audit emit failed",
			"kind", string(kind),
			"error", err.Error(),
		)
	}
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

// ── Fleet skill audit payload types (fleet-skills-sync-01NDFSEX18 WP07) ──────

// FleetSkillPublishedPayload carries the audit signalling for
// KindFleetSkillPublished.
//
// Privacy invariant: skill body, template, and tool_args_template are NEVER
// included — only catalog metadata and provenance.
type FleetSkillPublishedPayload struct {
	// CatalogID is the server-assigned catalog item ID.
	CatalogID string `json:"catalog_id"`
	// Slug is the human-readable catalog slug (matches the skill trigger).
	Slug string `json:"slug,omitempty"`
	// Version is the published SemVer string.
	Version string `json:"version,omitempty"`
	// Visibility is "private", "team", or "org_public".
	Visibility string `json:"visibility"`
}

// FleetSkillInstalledPayload carries the audit signalling for
// KindFleetSkillInstalled.
//
// Privacy invariant: skill body is NEVER included.
type FleetSkillInstalledPayload struct {
	// CatalogID is the fleet catalog item ID.
	CatalogID string `json:"catalog_id"`
	// Version is the installed SemVer string.
	Version string `json:"version,omitempty"`
	// Trigger is the slash-command trigger (no leading slash).
	Trigger string `json:"trigger"`
}

// FleetSkillUninstalledPayload carries the audit signalling for
// KindFleetSkillUninstalled.
type FleetSkillUninstalledPayload struct {
	// SkillID is the local store ID of the uninstalled skill.
	SkillID string `json:"skill_id"`
	// Trigger is the slash-command trigger that was unregistered.
	Trigger string `json:"trigger,omitempty"`
}

// ── ACP envelope payload type (acp-orchestration-integration-01NDFSEX06) ──────

// ACPEnvelopePayload carries the audit signalling for KindACPEnvelope.
// Emitted once per ACP envelope exchanged (sent or received).
//
// Privacy invariant: the envelope body and turn payload MUST NOT appear
// here. Only the envelope id, routing metadata, outcome, byte counts,
// and a classified error code are permitted. The audit log is not a
// message store.
type ACPEnvelopePayload struct {
	// EnvelopeID is the unique identifier of the ACP envelope (opaque ULID).
	EnvelopeID string `json:"envelope_id"`
	// PeerID is the remote peer's stable identifier.
	PeerID string `json:"peer_id"`
	// Transport is the transport kind used: "uds" | "http_loopback" | "http_lan".
	Transport string `json:"transport"`
	// Direction is "send" | "receive".
	Direction string `json:"direction"`
	// Outcome is "ok" | "denied_by_policy" | "transport_error" | "verify_rejected".
	Outcome string `json:"outcome"`
	// BytesIn is the number of bytes received (0 for outbound envelopes).
	BytesIn int64 `json:"bytes_in"`
	// BytesOut is the number of bytes transmitted (0 for inbound envelopes).
	BytesOut int64 `json:"bytes_out"`
	// ErrorCode is a machine-readable error classifier; empty on success.
	// Examples: "acp:policy_denied", "acp:transport_refused", "acp:verify_rejected".
	ErrorCode string `json:"error_code,omitempty"`
}

// ── Fleet context sync payload structs (fleet-context-sync-01NDFSEX15) ───────

// FleetSessionSyncPayload is the audit payload for session sync toggle events
// (KindFleetSessionSyncEnabled, KindFleetSessionSyncDisabled,
// KindFleetSessionSyncRemoteDeleted).
//
// Privacy invariant: SessionName and message content are NEVER included.
type FleetSessionSyncPayload struct {
	// SessionID is the opaque session identifier.
	SessionID string `json:"session_id"`
	// StreamID is the fleet stream identifier assigned to this session.
	StreamID string `json:"stream_id"`
}

// FleetSessionSyncResumedPayload is the audit payload for KindFleetSessionSyncResumed.
//
// Privacy invariant: event content is NEVER included — only the count.
type FleetSessionSyncResumedPayload struct {
	// SessionID is the opaque session identifier.
	SessionID string `json:"session_id"`
	// StreamID is the fleet stream identifier.
	StreamID string `json:"stream_id"`
	// EventsReplayed is the number of events replayed from fleet.
	EventsReplayed int `json:"events_replayed"`
}

// FleetProjectSyncPayload is the audit payload for project sync toggle events
// (KindFleetProjectSyncEnabled, KindFleetProjectSyncDisabled,
// KindFleetProjectSyncRemoteDeleted).
//
// Privacy invariant: project name and content are NEVER included.
type FleetProjectSyncPayload struct {
	// ProjectID is the opaque project identifier.
	ProjectID string `json:"project_id"`
	// StreamID is the fleet stream identifier assigned to this project.
	StreamID string `json:"stream_id"`
}

// FleetSessionHandoffPayload is the audit payload for team handoff events
// (KindFleetSessionSharedOutbound, KindFleetSessionSharedInbound).
//
// Privacy invariant: session name, message content, and recipient email are
// NEVER included — only opaque IDs cross the audit boundary.
type FleetSessionHandoffPayload struct {
	// SessionID is the opaque session identifier being shared.
	SessionID string `json:"session_id"`
	// RecipientUserID is the opaque user ID of the recipient.
	RecipientUserID string `json:"recipient_user_id"`
	// InboxItemID is the fleet inbox item ID (non-empty on inbound shares).
	InboxItemID string `json:"inbox_item_id,omitempty"`
}

// ── Fleet audit-archival payloads (fleet-audit-archival-01NDFSEX13) ──────────

// FleetAuditChainBreakPayload carries the audit signalling for
// KindFleetAuditChainBreak. Emitted when the pre-send hash-chain
// verifier detects a mismatch in the local audit log.
//
// Privacy invariant: no event payloads, user content, or credential
// bytes are included — only IDs and the event_id of the break.
type FleetAuditChainBreakPayload struct {
	// BrokenAtID is the event_id of the first row where the hash check
	// failed. The archiver halts at this cursor.
	BrokenAtID string `json:"broken_at_id"`
	// BatchStartID is the event_id of the first event in the batch that
	// was being verified when the break was detected.
	BatchStartID string `json:"batch_start_id"`
	// ErrorClass is a machine-readable classification of the break type:
	// "payload_hash_mismatch" | "prev_hash_mismatch".
	ErrorClass string `json:"error_class"`
}

// FleetAuditArchivedPayload carries the audit signalling for
// KindFleetAuditArchived. Emitted once per batch ACK'd by the fleet
// endpoint.
//
// Privacy invariant: no event payloads, user content, or credential
// bytes are included — only counts and cursor range.
type FleetAuditArchivedPayload struct {
	// BatchSize is the number of events sent and ACK'd in this batch.
	BatchSize int `json:"batch_size"`
	// FromID is the event_id of the first event in the batch.
	FromID string `json:"from_id"`
	// ToID is the event_id of the last event in the batch (the new cursor value).
	ToID string `json:"to_id"`
}

// FleetAuditChainSkippedPayload carries the audit signalling for
// KindFleetAuditChainSkipped. Emitted when an operator manually advances
// the archive cursor past a chain-break gap.
//
// Privacy invariant: no event payloads or user content — only cursor IDs.
type FleetAuditChainSkippedPayload struct {
	// FromID is the cursor value before the skip.
	FromID string `json:"from_id"`
	// ToID is the cursor value after the skip (the new resume point).
	ToID string `json:"to_id"`
}

// FleetAuditRetentionSweptPayload carries the audit signalling for
// KindFleetAuditRetentionSwept. Emitted once per retention sweep pass.
//
// Privacy invariant: no event payloads or user content — only counts
// and the oldest event_id that was deleted.
type FleetAuditRetentionSweptPayload struct {
	// DeletedCount is the number of locally-retained rows deleted.
	DeletedCount int `json:"deleted_count"`
	// OldestDeletedID is the event_id of the oldest row deleted.
	OldestDeletedID string `json:"oldest_deleted_id,omitempty"`
	// RetentionDays is the configured retention window used for this pass.
	RetentionDays int `json:"retention_days"`
}

// ContextBootstrapRunPayload carries the signalling for KindContextBootstrapRun
// (context-bootstrap-harness-integration WP05).
//
// Privacy invariant: the payload carries ONLY the run id, phase, outcome, and
// aggregate counts. It NEVER carries extracted node bodies, titles, source
// content, connector-item text, or third-party credential material.
type ContextBootstrapRunPayload struct {
	// RunID is the harness-side (or fleet-assigned) run identifier.
	RunID string `json:"run_id"`
	// Phase is the terminal phase for this event ("extraction", "done",
	// "failed", "clarify").
	Phase string `json:"phase"`
	// Outcome classifies the lifecycle event: "started" | "completed" |
	// "failed" | "resumed".
	Outcome string `json:"outcome"`
	// ConnectorCount is the number of connectors the run touched.
	ConnectorCount int `json:"connector_count"`
	// NodesWritten is the cumulative count of context nodes written.
	NodesWritten int `json:"nodes_written"`
	// CoverageHit is true when any connector stopped at a budget ceiling.
	CoverageHit bool `json:"coverage_hit"`
	// ErrorClass is a typed error label when Outcome=="failed" (never a raw
	// error message).
	ErrorClass string `json:"error_class,omitempty"`
}
