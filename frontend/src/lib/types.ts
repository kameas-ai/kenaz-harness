/**
 * Hand-curated TS mirror of the Go-side payloads exposed via Wails. The
 * canonical shapes live in core/rpc/{api.go, views/*}; these mirrors keep
 * the frontend strict-typed without leaking wailsjs/go/models into
 * components (FR-019, NFR-007).
 *
 * NO `any` here — privacy CI invariant + ESLint enforce.
 */

export interface Session {
  id: string;
  name: string;
  createdAt: string;
  updatedAt: string;
  /**
   * Most recent activity timestamp. Optional because the projects-view
   * Session subset omits the field on older payloads; the rail renders
   * a fallback dash when missing.
   */
  lastActiveAt?: string;
  /** Optional starting context attached to the session (Mission A). */
  systemPrompt?: string;
  /**
   * 'system' (invisible, prepended on every send) or 'user_seed'
   * (visible — the seed lives as the first user turn in the message
   * history). Optional for sessions persisted before the feature
   * landed; the backend defaults to 'system' on read.
   */
  contextKind?: 'system' | 'user_seed';
  /**
   * Project membership. Empty / undefined means "loose" — the session
   * is not attached to any project. Mirrors the Go-side
   * session.Record.ProjectID nullable column.
   */
  projectId?: string;
  /**
   * True when the auto-titling engine has written a generated title for
   * this session (session-auto-titling-01KQ8TDS WP05). The rail renders
   * the title in italic + muted style when this is true. Flips to false
   * when the user renames the session or calls clearTitle.
   * Mirrors session.Record.AutoTitled (Go-side).
   */
  autoTitled?: boolean;

  /**
   * Branch linkage fields (branching-ux-polish-01KQ8TD7 WP02/WP03).
   * Populated only for branch sessions; absent / undefined for root sessions.
   * Projected server-side from the branches table via JOIN.
   */

  /** ID of the parent session this session branched from. */
  parentSessionId?: string;
  /** Message anchor in the parent the branch forked at. */
  parentMessageId?: string;
  /** Display-name override for the branch. */
  branchTitle?: string;
  /** Nesting depth; 0 for root sessions; pre-computed server-side. */
  branchDepth?: number;
}

/**
 * SessionUsage — per-session cumulative token + cost aggregate.
 * Mirrors core/rpc/views/sessions.SessionUsage (token-cost-telemetry WP03).
 */
export interface SessionUsage {
  /** Sum of all input tokens for the session. */
  promptTokens: number;
  /** Sum of all output tokens. */
  completionTokens: number;
  /** promptTokens + completionTokens. */
  totalTokens: number;
  /** Summed cost in USD. 0 when no cost data. */
  costUsd: number;
  /**
   * How the cost was determined:
   *   'provider'  — directly reported by the upstream provider (exact).
   *   'derived'   — estimated from the curated pricing table (~).
   *   'mixed'     — some turns provider, some derived.
   *   'unknown'   — no cost data at all; render tokens only.
   */
  costSource: 'provider' | 'derived' | 'mixed' | 'unknown';
  /** Number of assistant turns that contributed usage data. */
  messageCount: number;
  /** Last-updated date of the pricing table used ("YYYY-MM-DD"). */
  pricingDataDate: string;
  /**
   * True when the auto-titling engine wrote the session name. Mirrors
   * session.Record.AutoTitled. The rail renders auto-titled sessions
   * with a subtle italic+muted hint so the user can distinguish
   * engine-chosen titles from names they set themselves.
   */
  autoTitled?: boolean;
}

/**
 * Project — a top-level grouping of sessions. Mirrors core/projects.Project.
 */
export interface Project {
  id: string;
  name: string;
  description: string;
  createdAt: string;
  updatedAt: string;
}

export interface Provider {
  id: string;
  name: string;
  tier: string;
  kind?: ProviderKind;
  model: string;
  /**
   * Authorised model set. Empty => single-model row; chat surface
   * falls back to [model]. The model-switcher pill in SessionsView
   * reads this list (filtered to the active session's family).
   */
  models?: string[];
  /**
   * Capability metadata for each entry in `models`, parallel by
   * position. The context-window meter reads contextWindow from here;
   * 0 / missing means unknown — the meter renders an explicit unknown
   * state rather than a misleading fallback denominator.
   */
  modelInfos?: ModelInfo[];
  region?: string;
  cred?: CredentialReference;
  /**
   * "bundle" | "personal" — surfaced so the UI can show whether a
   * provider came from a signed bundle or the user's providers.json.
   */
  source?: ProviderSource;
  /**
   * Most-recent TestProvider outcome for the row's status pill. Reset
   * to false on AddProvider.
   */
  validated?: boolean;
}

export type ProviderSource = 'bundle' | 'personal';

/**
 * Reference-only credential pointer. NEVER carries a plaintext value;
 * the `kind: "keychain"` shape is what AddProvider persists. The lint
 * rule in WP14 flags additions of `value` / `secret` / `password` /
 * `apiKey` / `token` to this shape.
 */
export interface CredentialReference {
  kind: 'keychain' | 'env' | 'file' | 'aws_profile' | 'kms';
  locator: string;
}

export type ProviderKind =
  | 'anthropic'
  | 'openai'
  | 'openrouter'
  | 'bedrock'
  | 'ollama'
  | 'azure-openai'
  | 'gemini'
  /** Custom OpenAI-compatible endpoint (custom-openai-compatible-endpoint-01KQ8VN0) */
  | 'custom-openai'
  /** Fleet-hosted inference — only available when signedIn && capability('hosted_inference') (fleet-capability-surface-01NDFSEX09) */
  | 'fleet-hosted';

/**
 * GeminiEndpointKind selects which Google endpoint to target.
 * - 'ai_studio': Google AI Studio (API-key auth) — the default.
 * - 'vertex': Google Cloud Vertex AI (service-account or ADC auth).
 */
export type GeminiEndpointKind = 'ai_studio' | 'vertex';

/**
 * GeminiAuthStatus carries the auth token status for a Gemini Vertex row.
 * Returned by LLM_AuthStatus(profileID).
 */
export interface GeminiAuthStatus {
  mode: 'api_key' | 'service_account' | 'adc';
  /** ISO-8601 expiry for OAuth modes; empty for API-key mode. */
  expiresAt?: string;
  source: string;
}

/** Auth scheme for custom-openai endpoints. */
export type CustomAuthScheme = 'bearer' | 'api-key-header' | 'custom' | 'none';

/** Summary of a custom endpoint template from the template registry (WP01). */
export interface CustomTemplateSummary {
  id: string;
  name: string;
  base_url: string;
  auth_scheme: CustomAuthScheme;
}

/** Probed capability matrix for a custom endpoint. */
export interface CustomCapabilityMatrix {
  endpoint: string;
  probed_at: number;
  streaming: 'true' | 'false' | 'unknown';
  tool_calling: 'true' | 'false' | 'unknown';
  streaming_usage: 'true' | 'false' | 'unknown';
}

/** Input for the ProbeCustomEndpoint RPC. */
export interface CustomProbeRequest {
  base_url: string;
  model: string;
  auth_scheme: CustomAuthScheme;
  auth_header?: string;
  plaintext_api_key?: string;
}

/** Result of the ProbeCustomEndpoint RPC. */
export interface CustomProbeResult {
  matrix: CustomCapabilityMatrix;
  error?: string;
}

export interface AddProviderInput {
  id: string;
  name: string;
  kind: ProviderKind;
  model: string;
  /**
   * Authorised model set; first entry is the default. Empty for
   * legacy single-model adds; the backend treats Models=[Model] then.
   */
  models?: string[];
  region?: string;
  cred: CredentialReference;
  /**
   * Plaintext API key the bindings layer writes to the OS keychain
   * before zeroing. Never stored in providers.json.
   */
  plaintextApiKey?: string;
  /**
   * Gemini-specific: which endpoint variant to target.
   * 'ai_studio' (default) or 'vertex'.
   * Only set when kind === 'gemini'.
   */
  geminiEndpointKind?: GeminiEndpointKind;
  /**
   * Google Cloud project ID for Vertex AI endpoints.
   * Only set when geminiEndpointKind === 'vertex'.
   */
  project?: string;
}

export interface TestResult {
  success: boolean;
  latency_ms: number;
  message: string;
}

/**
 * ModelInfo — one entry in the dropdown the AddProvider drawer
 * populates after the user pastes their API key. Comes from the
 * provider's /models endpoint via LLMConnectorAPI.ListModels.
 */
export interface ModelInfo {
  id: string;
  displayName: string;
  description?: string;
  /** Max context length in tokens; 0 / undefined = unknown. */
  contextWindow?: number;
  /**
   * Provider's hard cap on output tokens per turn.
   * 0 / undefined = unknown — the UI should not render an explicit cap.
   * Sourced from the backend capability catalog
   * (backend-context-window-length-01KQ8TD3 WP01).
   */
  maxOutputTokens?: number;
}

export interface MCPServer {
  id: string;
  name: string;
  state: string;
  version: string;
  transport?: string;
  capabilities?: string[];
}

// ── MCP Test Connection (mission mcp-server-install-01KQ8TDP, WP07) ────
//
// Wire shape for `MCP_TestRecipe`. Field names match the Go JSON tags
// (camelCase) on mcp.TestResult. The frontend renders the result in the
// "Test Connection" drawer without any adaptation layer.

/**
 * MCPTestResult is the outcome of a one-shot Test Connection RPC.
 * All string fields may be empty on failure; numeric counts default to 0
 * when the server did not advertise the capability or the handshake
 * did not complete.
 */
export interface MCPTestResult {
  /** Whether the full initialize + capability listing completed without error. */
  ok: boolean;
  /** Server name from the initialize response serverInfo block. */
  serverName: string;
  /** Server version from the initialize response serverInfo block. */
  serverVersion: string;
  /** Protocol version echoed back by the server. */
  protocolVersion: string;
  /** Number of tools reported by tools/list (0 if not advertised). */
  toolCount: number;
  /** Number of resources reported by resources/list (0 if not advertised). */
  resourceCount: number;
  /** Number of prompts reported by prompts/list (0 if not advertised). */
  promptCount: number;
  /**
   * Up to 4 KiB of the most recent stderr output (stdio recipes only).
   * Empty for HTTP/SSE transports.
   */
  stderrTail: string;
  /** Human-readable error when ok is false. Empty when ok is true. */
  errorMessage: string;
  /** Wall-clock elapsed time from connection open to close, in milliseconds. */
  durationMs: number;
}

// Wire shape for `MCP_TestRecipe`. Field names follow Go JSON tags
// (snake_case) verbatim. `ok` is the primary discriminant; `error`
// is populated on failure. Capability counts are -1 when the server
// did not advertise the capability (not 0, which means "advertised
// but empty").

export interface MCPTestResult {
  ok: boolean;
  protocol_version?: string;
  server_info: { name: string; version: string };
  capabilities: {
    tools?: { listChanged?: boolean };
    resources?: { subscribe?: boolean; listChanged?: boolean };
    prompts?: { listChanged?: boolean };
    logging?: Record<string, never>;
  };
  /** -1 when tools capability not advertised. */
  tool_count: number;
  /** -1 when resources capability not advertised. */
  resource_count: number;
  /** -1 when prompts capability not advertised. */
  prompt_count: number;
  /** Last 4 KiB of stderr from a stdio server on failure. */
  stderr_tail?: string;
  duration_ms: number;
  error?: string;
}

// ── MCP clipboard import (mission mcp-server-install-01KQ8TDP, WP08) ───
//
// Wire shapes for `MCP_ImportClaudeDesktopConfig`. Field names follow
// the Go JSON tags (snake_case) verbatim because the binding does not
// adapt these structs frontend-side — the modal (WP09) reads the
// wire fields directly. The translator is read-only on `dry_run=true`;
// `dry_run=false` writes <DataDir>/mcp/recipes/_imports/<id>.{yaml,json}
// for every kept-or-warning entry the caller did not exclude via
// `keep_ids`.

/**
 * MCPImportStatus mirrors the Go-side ImportEntry.Status enum.
 *
 * - `kept` — translated cleanly; safe to save.
 * - `unsupported` — parses but harness can't adopt it (HTTP/SSE
 *   transport, oauth2, unknown auth scheme); Reason is populated
 *   with a one-line explanation.
 * - `malformed` — entry's JSON is structurally invalid (missing
 *   required field, wrong type); Reason carries the parser-level
 *   error.
 * - `collision_warning` — translated cleanly but id collides with an
 *   existing recipe; kept-with-warning. Reason carries the colliding
 *   id. Saving will shadow the existing recipe per MergedCatalog
 *   precedence (user > registry > shipped).
 */
export type MCPImportStatus =
  | 'kept'
  | 'unsupported'
  | 'malformed'
  | 'collision_warning';

/**
 * MCPImportEntry is one translated row from the pasted mcpServers map.
 * The `recipe` field is the zero value (id="") for non-kept entries —
 * the modal MUST gate Save on `status === 'kept' || status ===
 * 'collision_warning'`.
 *
 * `original_json` preserves the exact JSON snippet for this entry so
 * the modal can render a "show original" disclosure.
 */
export interface MCPImportEntry {
  id: string;
  original_name: string;
  status: MCPImportStatus;
  reason?: string;
  recipe: Recipe;
  original_json?: string;
}

export interface MCPTranslationReport {
  entries: MCPImportEntry[];
  kept_count: number;
  unsupported_count: number;
  malformed_count: number;
  collision_count: number;
}

/**
 * MCPImportRequest is the single argument to MCP_ImportClaudeDesktopConfig.
 *
 * - `raw_json`: the clipboard payload verbatim (max 64 KiB; oversize
 *   surfaces as a typed error from the binding).
 * - `dry_run`: `true` → no disk writes, just translation preview;
 *   `false` → commit kept-or-warning entries to disk.
 * - `keep_ids`: optional per-entry filter for the `dry_run=false`
 *   path. Empty/missing means "save every kept-or-warning entry".
 */
export interface MCPImportRequest {
  raw_json: string;
  dry_run: boolean;
  keep_ids?: string[];
}

export interface MCPImportWrotePath {
  id: string;
  yaml_path: string;
  json_path: string;
}

export interface MCPImportResponse {
  report: MCPTranslationReport;
  wrote_paths?: MCPImportWrotePath[];
}

export interface A2ACard {
  id: string;
  issuer: string;
  subject: string;
  issuedAt: string;
}

export interface Job {
  id: string;
  name: string;
  state: string;
  startedAt?: string;
}

/**
 * Reference-only secret metadata. Compile-time guard: NO `value` /
 * `secret` / `password` / `apiKey` / `token` field — the WP14 lint rule
 * flags any addition. FR-020 / C-004 / privacy CI invariant.
 */
export interface SecretReference {
  id: string;
  label: string;
  source: string;
  createdAt: string;
}

/**
 * ModelSecretRow — one entry in the model-accessible secrets panel.
 * Mirrors core/rpc/views/secrets.SecretRow. No plaintext is included
 * (FR-005a). The ref field is the @secret:<locator> token the model
 * writes in tool arguments.
 *
 * IMPORTANT: DO NOT add a `value`, `secret`, `password`, `apiKey`,
 * or `token` field — the privacy-CI lint rule flags them.
 * (model-secret-references-01KW7M5A WP10)
 */
export interface ModelSecretRow {
  ref: string;
  locator: string;
  description: string;
  kind: string;
  scope: string;
  exposedAt: string;
}

/**
 * ModelSecretExposeRequest — wire shape for Secrets_Expose.
 * The plaintext field is zeroed server-side immediately after being
 * handed to the ExposureIndex. It never enters the conversation context.
 */
export interface ModelSecretExposeRequest {
  locator: string;
  description: string;
  kind: string;
  plaintext: string;
}

export interface ContextEntry {
  id: string;
  kind: string;
  label: string;
}

/**
 * AttachmentScopeKind — three scope tiers used by context attachments.
 * Mirrors core/attachments.ScopeKind*. "global" applies to every session;
 * "project" applies to every session under a given project; "session"
 * applies to one specific session. Resolution order in
 * Attachments_ListResolved is global → project → session.
 */
export type AttachmentScopeKind = 'global' | 'project' | 'session';

/**
 * Attachment — wire shape of one stored context attachment. Mirrors
 * core/rpc/views/attachments.Attachment.
 *
 * `contentSource` is either `inline:<sha256>` (snapshot uploaded/pasted
 * by the user) or `library:<root-rel>` (snapshot pulled from the Context
 * Library at the relative path; Refresh re-reads the source). The
 * snapshot lives in `content` either way — deletion of the underlying
 * library file does NOT affect already-attached sessions (FR-202).
 *
 * `kind` is `'system'` (invisible, prepended on every send) or `'user'`
 * (visible — surfaced as a user turn). Position is the 0-based ordering
 * within (scopeKind, scopeId).
 */
export interface Attachment {
  id: string;
  scopeKind: AttachmentScopeKind;
  scopeId?: string;
  contentSource: string;
  content: string;
  kind: 'system' | 'user';
  position: number;
  createdAt: string;
}

/**
 * AttachmentAddInput — wire shape Attachments.add accepts. Mirrors
 * core/rpc/views/attachments.AddInput. The server fills in id +
 * createdAt when omitted.
 */
export interface AttachmentAddInput {
  scopeKind: AttachmentScopeKind;
  scopeId?: string;
  contentSource: string;
  content: string;
  kind?: 'system' | 'user';
  position?: number;
}

/**
 * Context Library tree node. Path is slash-separated and relative to
 * the library root — the frontend never sees an absolute path. Children
 * is non-empty only on folder kinds; the wire shape omits the field for
 * leaves so the JSON payload stays small for big libraries.
 */
export type ContextNodeKind = 'folder' | 'file';

export interface ContextNode {
  name: string;
  path: string;
  kind: ContextNodeKind;
  size?: number;
  modified?: string;
  children?: ContextNode[];
}

export interface Bundle {
  id: string;
  name: string;
  version: string;
  tier: string;
  source?: string;
  signature?: string;
  artifactCount: number;
  artifacts?: BundleArtifact[];
}

export interface BundleArtifact {
  name: string;
  kind: string;
  contentHash: string;
}

export interface Denial {
  policyId: string;
  clauseId: string;
  violatingInput: string;
  remediation: string;
}

export interface AuditEntry {
  id: string;
  timestamp: string;
  category: string;
  subject: string;
  trailing?: string;
}

export interface AuditFilter {
  categories?: string[];
  since?: string;
  until?: string;
  limit?: number;
}

/** VerifyChainResult — wire shape from Audit_VerifyChain RPC. */
export interface VerifyChainResult {
  verified: boolean;
  rows_checked: number;
  broken_at_id?: string;
}

/**
 * AuditFilterQuery — rich structured filter for audit log queries.
 * Mirrors core/event/log.FilterQuery.
 */
export interface AuditFilterQuery {
  since?: string;
  until?: string;
  actor_ids?: string[];
  kinds?: string[];
  resources?: string[];
  outcomes?: string[];
  free_text?: string;
  verbose?: boolean;
  limit?: number;
  offset?: number;
}

/**
 * SavedAuditQuery — a persisted named audit filter query.
 * Mirrors core/event/log.SavedQuery.
 */
export interface SavedAuditQuery {
  id: string;
  name: string;
  query: AuditFilterQuery;
  created_at: string;
  user_id?: string;
}

/**
 * AuditSettings — wire shape for Settings_GetAuditSettings / SetAuditSettings.
 * Mirrors core/rpc/views/settings.AuditSettings.
 */
export interface AuditSettings {
  /** "keep_forever" | "delete_after_window" | "archive_after_window" */
  strategy: string;
  /** Retention window in days. Only used for non-keep_forever strategies. */
  window_days: number;
}

/**
 * AuditExportOptions — wire shape for Audit_Export.
 * Mirrors core/event/log.ExportOptions.
 */
export interface AuditExportOptions {
  /** Root data directory; harness fills this in server-side. */
  data_dir?: string;
  filter: AuditFilterQuery;
  /** "csv" | "jsonl" | "pdf" */
  format: string;
  harness_version?: string;
  git_sha?: string;
  chain_status?: string;
}

export interface ShellStatus {
  activeProvider: string;
  trustTier: string;
  harnessBuild: string;
  connection: ConnectionState;
  eventRate: number;
  policyApplied: boolean;
  redactionOn: boolean;
  localFirstOn: boolean;
}

export interface AppInfo {
  build: string;
  commit: string;
  buildTime: string;
  goVersion: string;
  platform: string;
  windowSize: WindowSize;
  /**
   * policyEditorEnabled is true when HARNESS_POLICY_EDITOR_UI != "0".
   * Controls whether the /policy route registers and write-path RPCs are
   * available (cedar-policy-editor-ui-01KQ8TD6 WP01).
   */
  policyEditorEnabled?: boolean;
  /**
   * keychainRotationEnabled is true when HARNESS_KEYCHAIN_ROTATION is not
   * set to "off", "0", or "false". The frontend uses this to hide the
   * "Auto-resume after rotating an API key" Settings toggle and the
   * AuthFailureToast rotate button when the feature is disabled.
   * (provider-keychain-rotation-01KQ8TD9 WP07)
   */
  keychainRotationEnabled?: boolean;
  /**
   * customOpenAIEnabled is true when HARNESS_CUSTOM_OPENAI is not "0".
   * The frontend uses this to hide the "Custom OpenAI-compatible" kind in
   * the provider-add form when the feature is disabled.
   * (custom-openai-compatible-endpoint-01KQ8VN0 WP08)
   */
  customOpenAIEnabled?: boolean;
  /**
   * capabilities is the fleet capability gate state keyed by the snake_case
   * wire key (e.g. "hosted_inference"). Undefined or empty when the user is
   * signed out or fleet is disabled.
   * (fleet-capability-surface-01NDFSEX09 WP11)
   */
  capabilities?: Record<string, boolean>;
  /**
   * tier is the user's current fleet subscription tier.
   * One of "pro" | "team" | "enterprise" | "". Empty when signed out.
   * (fleet-capability-surface-01NDFSEX09 WP11)
   */
  tier?: 'pro' | 'team' | 'enterprise' | '';
}

export interface WindowSize {
  width: number;
  height: number;
}

export interface Settings {
  schemaVersion: number;
  lastRoute: string;
  theme: Theme;
  accent: string;
  windowSize: WindowSize;
  /**
   * Long-term-memory opt-in (privacy default: false). When true, the
   * harness embeds pinned messages into the local vector DB and may
   * inject retrieved snippets into future conversations across all
   * sessions.
   */
  memoryEnabled?: boolean;
  /**
   * WP05 confirm-each modal flag. The persisted form is the inverted
   * `confirmEachDisabled` bit so a fresh install (zero value) gets the
   * spec's default-ON behaviour. Frontend should treat both fields as
   * read-mostly — write via Settings_SetConfirmEach so the inversion
   * happens on the backend.
   */
  confirmEachDisabled?: boolean;
  /**
   * Chat-graph LoopNode iteration cap (agent-kernel-graph-chat-migration
   * mission). Zero on the wire means "use the spec default"
   * (DefaultMaxAgentTurns = 25). Frontend renders the placeholder
   * accordingly; the chassis reads the effective value on every chat
   * run start so a settings change takes effect on the next user turn.
   */
  maxAgentTurns?: number;

  /**
   * Compaction aggressiveness tier (mission
   * compaction-strategy-ui-01KQ8TDI §2.9). Empty == "balanced" (the
   * documented default). The Settings panel's five-stop slider emits
   * one of the five locked enum strings; the chat runner reads through
   * the EffectiveCompactionAggressiveness accessor on every send.
   */
  compactionAggressiveness?: CompactionAggressiveness;

  /**
   * Provider+model used for the summarisation call. Empty == "use the
   * session's active model" (the chained default the chat runner falls
   * back to when this is unset).
   */
  compactionModel?: ProviderProfileRef;

  /**
   * Soft-archive retention window in days. Default 90, range [7, 365].
   * Save validation rejects out-of-range values; defensive Effective
   * accessors clamp at read time as belt-and-braces.
   */
  compactionArchiveDays?: number;

  /**
   * Count of most-recent user-assistant pairs that compaction will
   * never touch. Default 4. Negative is rejected at save.
   */
  compactionRecentWindow?: number;

  // ── WP08 — Universal permission dials ────────────────────────────

  /**
   * Global permission posture for all four resource families.
   * "strict" — every call prompts. "normal" (default) — Cedar gates.
   * "permissive" — non-dangerous ops skip prompts.
   * Empty/missing == "normal".
   */
  permissionMode?: PermissionMode;

  /**
   * When true, "Allow always" is offered for dangerous-tier resources
   * (rm, sudo, system paths, etc). Default false — dangerous ops
   * re-prompt every time. Requires confirm dialog to enable.
   */
  permissionCacheDangerousOps?: boolean;

  /**
   * Marker set by WP10's first-boot migration after it writes
   * bash_allow_*.cedar files for the historical allowlist. The UI reads
   * this to suppress the one-time migration toast after it fires.
   */
  bashAllowlistMigrated?: boolean;

  /**
   * Marker set after the one-time migration toast is shown. Once true
   * the toast never displays again.
   */
  permissionsMigrationToastShown?: boolean;

  /**
   * Markdown rendering extensions dial (markdown-rendering-polish-01KQ8TDT).
   * Controls which heavy renderers (KaTeX, Mermaid) are active.
   * Empty / undefined == 'all' (default). Set to 'basic' to disable
   * both KaTeX and Mermaid on slow machines.
   */
  markdownExtensions?: MarkdownExtensions;
  // ── Branch Advisor dials (branch-as-subagent-recommendation WP08) ──

  /**
   * Master on/off for the branch advisor (FR-010). When false the banner
   * never mounts, regardless of confidence. Default false.
   */
  branchAdvisorEnabled?: boolean;

  /**
   * Heuristic confidence threshold. Default 0.85. Range [0, 1].
   * Banner only mounts when the detector returns confidence ≥ this value.
   */
  branchAdvisorMinConfidence?: number;

  /**
   * Reserved: enables the LLM-backed detector (FR-013). Default false.
   */
  branchAdvisorUseLLM?: boolean;

  /**
   * Reserved: enables auto-branching above a higher threshold (FR-014).
   * Default false.
   */
  branchAutoMode?: boolean;

  /**
   * Token budget for the reintegration summarization call (FR-008a).
   * Default 2000, min 500, max 16000.
   */
  branchReintegrationMaxTokens?: number;

  /**
   * Provider+model used for newly spawned subagent branches. Defaults to
   * compactionModel, which itself defaults to the session's active model.
   */
  branchAdvisorDefaultModel?: ProviderProfileRef;
  /**
   * User-overridden keyboard shortcut bindings. Map of shortcut id
   * (e.g. 'chat.send') → canonical binding string (e.g. 'Cmd+Shift+Enter').
   * Empty or missing map means all shortcuts use their registry defaults.
   * Backend validates: ≤200 entries, ≤64 chars/value, no control chars.
   * (keyboard-shortcuts-settings-01KQ8TDR plan §2.7)
   */
  keyboardShortcuts?: Record<string, string>;

  /**
   * Reserved for a future keyboard-shortcut preset gallery (v1 ships
   * with only the hardcoded defaults). Empty string = bundled defaults.
   * (keyboard-shortcuts-settings-01KQ8TDR plan Q1=C)
   */
  keyboardShortcutsPreset?: string;

  /**
   * Per-month spend notification threshold in USD
   * (token-cost-telemetry-01KQ8TD7 WP06). Zero (the default) disables
   * the scheduler; any positive value up to $10,000 enables the
   * 50/80/100/150/200% escalating notifications. The dial takes effect
   * on the next chat turn — the threshold checker reads it fresh on
   * every Manager.Add tail. FR-007c: visibility only — hard caps live
   * in the user's provider dashboard.
   */
  monthlyCostNotifyUsd?: number;

  /**
   * cross-session-search WP07 — inverted persisted bit for the Cmd+F
   * search modal. Default false (= search enabled). When true, the
   * SearchAPI short-circuits and returns no hits regardless of what
   * is in the FTS5 index. The on-disk index itself is unaffected;
   * toggling back resumes search immediately.
   */
  searchDisabled?: boolean;

  /**
   * auto-update v0.4.0 WP05 — inverted persisted bit for the
   * "Automatically check for updates" toggle. Default false (= checker
   * enabled). When true, the auto-update scheduler is suspended.
   */
  autoCheckUpdatesDisabled?: boolean;

  /**
   * auto-update v0.4.0 WP05 — release channel the checker subscribes
   * to. Empty == "stable". Valid: "stable" | "prerelease".
   */
  updateChannel?: 'stable' | 'prerelease';

  /**
   * auto-update v0.4.0 WP05 — poll interval in seconds. Spec values:
   * 3600 (1h), 21600 (6h, default), 86400 (24h). Zero == default.
   */
  updateCheckIntervalSec?: number;

  /**
   * auto-update v0.4.0 WP05 — versions the user clicked "Skip this
   * version" on. The checker filters these out so the user is not
   * re-prompted; the Settings panel offers a per-row "Unskip" link.
   */
  skippedUpdateVersions?: string[];

  /**
   * Per-provider-kind context-window override map
   * (backend-context-window-length-01KQ8TD3 WP05). Keys are provider
   * kind strings ("anthropic", "openai", "bedrock", "openrouter").
   * When a key is present, the context-window meter uses its value as
   * the denominator instead of the backend-curated catalog value.
   * Zero or absent means "use catalog values."
   */
  contextWindowOverrides?: Record<string, number>;

  /**
   * per-message-token-meter-01KR3PQR — when true, each assistant bubble
   * shows a small token-cost chip (prompt → completion = total · $cost).
   * Default false (OFF) — hides the chip to keep the chat uncluttered.
   * Toggle via Settings → Display → "Show per-message token meter".
   */
  showPerMessageTokenMeter?: boolean;

  /**
   * Branching UX dials — branching-ux-polish-01KQ8TD7 WP06.
   *
   * autoCollapseBranchesInSidebar: when true (default), every parent
   * session that has branch children starts collapsed in the left rail
   * so the sidebar doesn't sprawl on first load. Users can expand
   * individually; their choices persist in localStorage.
   */
  autoCollapseBranchesInSidebar?: boolean;

  /**
   * deleteBranchesWithParent: when true, deleting a parent session
   * recursively removes all descendant branch sessions before the
   * branch row is cascaded. Default false (safe / orphan behaviour).
   */
  deleteBranchesWithParent?: boolean;

  /**
   * maxVisibleBranchDepth: caps the number of nesting levels shown in
   * the sidebar branch tree. Default 5. Sessions deeper than the cap
   * are replaced by a "+N more depths" affordance.
   */
  maxVisibleBranchDepth?: number;

  // ── Long-session nudge dials (v0.5.6 memory-trust-signals) ──────────

  /**
   * longSessionNudgeTurns — number of user-assistant turn pairs (half
   * the total message count) after which the inline long-session nudge
   * banner appears. Default 30. Zero == use default.
   */
  longSessionNudgeTurns?: number;

  /**
   * longSessionNudgeTokens — cumulative prompt-token threshold after
   * which the nudge banner appears regardless of turn count.
   * Default 50000. Zero == use default.
   */
  longSessionNudgeTokens?: number;

  // ── Multimodal output capture dials (multimodal-io-extended-01KQ8TD2 WP02) ──

  /**
   * autoCaptureGeneratedImagesDisabled — inverted persisted bit for the
   * auto-capture toggle. Default false (= auto-capture enabled).
   * Frontend reads the effective state via
   * `Settings_GetAutoCaptureGeneratedImages`; never reads this directly.
   * Mirrors core/rpc/views/settings.Settings.AutoCaptureGeneratedImagesDisabled.
   */
  autoCaptureGeneratedImagesDisabled?: boolean;

  /**
   * maxGeneratedImageBytes — per-image byte cap for the auto-capture
   * pipeline in bytes. Zero (the default) means "use the server default"
   * (20 MiB). The Settings panel's number input persists in bytes; the
   * display helper divides by 1 MiB for the label.
   * Mirrors core/rpc/views/settings.Settings.MaxGeneratedImageBytes.
   */
  maxGeneratedImageBytes?: number;

  // ── Crash reporting dials (sentry-error-monitoring-01KX5R8G) ───────────
  /**
   * crashReportingTier: "off" | "anonymous" | "identified".
   * Default "off" (zero value). Controls whether and how crash reports are
   * sent to the configured Sentry DSN.
   */
  crashReportingTier?: string;
  /**
   * sentryDsn: The Sentry Data Source Name. When empty, crash reporting
   * is inoperative even if crashReportingTier is non-"off".
   */
  sentryDsn?: string;
  /**
   * hasSeenCrashReportingOnboarding: set to true after the user dismisses
   * the first-launch onboarding modal. Controls whether the modal mounts.
   */
  hasSeenCrashReportingOnboarding?: boolean;
}

/**
 * CostThresholdCrossedPayload — the event the WP06 scheduler publishes
 * on the `cost.threshold.crossed` broker topic when a per-month spend
 * tier (50/80/100/150/200 % of monthlyCostNotifyUsd) is newly crossed.
 * Mirrors core/usage.ThresholdCrossedPayload exactly.
 */
export interface CostThresholdCrossedPayload {
  /** Tier just crossed: 50 / 80 / 100 / 150 / 200. */
  pct: number;
  /** Calendar-month total spend in USD as of the triggering turn. */
  monthTotalUsd: number;
  /**
   * The monthlyCostNotifyUsd setting value at the moment of firing.
   * Surfaced so the toast can render "you've used 80% of your $25/mo
   * budget" without re-reading Settings.
   */
  thresholdUsd: number;
  /** Local-time YYYY-MM key the row was written under. */
  yearMonth: string;
  /** RFC-3339 timestamp string the firing was recorded at. */
  firedAt: string;
}

/**
 * MarkdownExtensions — controls which rendering features are active in
 * MarkdownBlock. Matches the four-stop dial in SettingsView.
 *   'basic'    — GFM only; KaTeX and Mermaid disabled.
 *   'math'     — GFM + KaTeX; Mermaid disabled.
 *   'diagrams' — GFM + Mermaid; KaTeX disabled.
 *   'all'      — all renderers active (default).
 */
export type MarkdownExtensions = 'basic' | 'math' | 'diagrams' | 'all';

/**
 * ConfirmDecision — the four canonical responses to a confirm-each
 * tool-call modal. Matches core/toolloop.ConfirmDecision.
 */
export type ConfirmDecision =
  | 'allow'
  | 'deny'
  | 'always_allow'
  | 'always_deny';

/**
 * ConfirmToolRequest — payload received on `llm:tool-confirm-request`
 * when the toolloop pauses on a confirm-each verdict. Mirrors
 * core/rpc/views/llm.ConfirmRequestPayload.
 */
export interface ConfirmToolRequest {
  request_id: string;
  session_id: string;
  parent_sub_id: string;
  server: string;
  tool: string;
  tool_use_id: string;
  args_redacted?: string;
  reason?: string;
}

export type Theme = 'light' | 'dark' | 'system';

export type ConnectionState = 'connecting' | 'ready' | 'degraded' | 'lost';

/**
 * EventStreamEntry — the typed shape rendered by EventStreamRow. The
 * `category` is constrained to the registry in lib/categories.ts.
 */
export interface EventStreamEntry {
  id: string;
  timestamp: string;
  category: string;
  subject: string;
  trailing?: string;
  size?: number;
}

/**
 * Chat-message domain types. Mirrors the Go-side `core/session` package
 * (added by the parallel session-manager mission). Until that mission
 * lands these shapes are the source of truth on the frontend.
 *
 * NEVER add a `secret` / `apiKey` / `token` / `password` / `value` field
 * here — privacy CI invariant + the WP14 lint rule reject it.
 */
export type MessageRole = 'user' | 'assistant' | 'system' | 'tool';

export interface ToolCall {
  id: string;
  name: string;
  /** Arguments rendered as a single-line monospace summary. NEVER raw JSON
   * dumped untrimmed — the redactor server-side already strips it. */
  argsSummary: string;
  /** Optional latency display, e.g. `"412ms"`. */
  latency?: string;
  /**
   * True when the dispatch resolved at least one @secret: reference token
   * in the tool arguments. Never carries plaintext — provenance only
   * (model-secret-references-01KW7M5A WP14). Drives the lock icon in the
   * chat UI.
   */
  usedSecrets?: boolean;
}

export interface Message {
  id: string;
  sessionId: string;
  role: MessageRole;
  /** Markdown-flavoured plain text. The renderer is intentionally not a
   * full markdown HTML pipeline — see MessageBubble for the safe subset. */
  content: string;
  createdAt: string;
  /** Streaming flag — true while the assistant is still emitting tokens. */
  streaming?: boolean;
  /**
   * Stream failure marker (long-turn-resilience WP00). When the SSE stream
   * closes with a non-completed reason or an error event fires mid-flight,
   * the partial assistant content is still committed to messages with this
   * field stamped to the failure reason ("error", "cancelled", "transient",
   * "closed-without-finish", etc.). Empty / undefined for healthy streams.
   * Serialised omitempty.
   */
  streamingError?: string;
  toolCalls?: readonly ToolCall[];
  /**
   * Polymorphic content blocks for multimodal messages
   * (multimodal-io WP02/WP04). Empty / undefined for legacy text-only
   * messages — MessageBubble falls back to the `content` field in that
   * case. The wire shape mirrors core/llm.ContentBlock; field names use
   * snake_case to match the Wails serializer's verbatim JSON-tag pass.
   */
  contentBlocks?: readonly ContentBlock[];
  /**
   * Compaction bookkeeping (compaction-strategy-ui WP07). Populated by
   * the WP01 schema migration once a session has compacted history.
   *
   *   - compactedIntoId : id of the synthetic summary row that folded
   *                       this message in. Empty / undefined on rows the
   *                       compaction engine never touched. Frontend uses
   *                       this to wire the "archived → summary" jump
   *                       chip on archived rows.
   *   - compactedAt     : RFC3339Nano UTC moment the engine wrote the
   *                       summary row that replaces this message. On the
   *                       synthetic summary row itself, compactedAt is
   *                       set and compactedIntoId is empty — that's how
   *                       the frontend identifies "this row IS a summary".
   *   - archivedAt      : RFC3339Nano UTC moment this row was flagged
   *                       archived. Empty / undefined on live rows;
   *                       non-empty rows are excluded from
   *                       sessions.listMessagesActive.
   */
  compactedIntoId?: string;
  compactedAt?: string;
  archivedAt?: string;
  /**
   * Long-turn-resilience-01KR3PRS WP03 — partial-output drop metadata.
   * Populated by migration 0317 on assistant rows the chat runner
   * persisted after a stream drop:
   *
   *   - streamingFailedAt    : RFC3339Nano UTC moment the runner
   *                            decided the stream was lost. Empty on
   *                            healthy rows.
   *   - streamingFailureKind : "transient" | "auth" | "unknown".
   *                            Selects the failure copy.
   *   - streamingRecoverable : true when no tool_use ran before the
   *                            drop, so the Resume button is safe.
   *   - continuationOf       : id of the partial row this row
   *                            continues. Set only on continuation
   *                            rows written by Sessions_ResumeMessage;
   *                            empty on every original assistant row.
   */
  streamingFailedAt?: string;
  streamingFailureKind?: 'transient' | 'auth' | 'unknown' | string;
  streamingRecoverable?: boolean;
  continuationOf?: string;
  /** Frontend-only marker for the WP03 partial-message bubble — set by
   * the useSession close-handler when the stream dropped before the
   * assistant turn could land via SessionWriteNode. Mirrors the WP00
   * surface: the bubble shows "Connection lost — partial reply
   * preserved." plus a Resume button when streamingRecoverable. */
  streamingError?: string;

  /**
   * Per-message token usage (per-message-token-meter-01KR3PQR). Present
   * only on assistant rows for which the token-cost-telemetry pipeline
   * captured usage data. Absent on user / system / tool rows and on rows
   * that pre-date the telemetry migration.
   */
  promptTokens?: number;
  completionTokens?: number;
  costUsd?: number;
  /** "provider" | "derived" | "mixed" | "unknown". Empty when absent. */
  messageCostSource?: string;

  /**
   * model-fallback-routing-01NDFSEX04 WP04. When the fallback runner
   * rerouted this turn to a different provider, these fields carry the
   * actual (provider, model) used. Empty / undefined when the primary
   * provider was used (no fallback occurred). In-memory only — not
   * persisted to SQL.
   */
  actualProvider?: string;
  actualModel?: string;
}

/**
 * ListMessagesResult — wire shape for sessions.listMessagesActive /
 * sessions.listMessagesAll (compaction-strategy-ui WP07). Carries the
 * message list plus a SweptCount counting rows that were once archived
 * but have since been hard-deleted by the soft-archive sweep.
 *
 * SweptCount is currently always 0 from the backend (the sweep deletes
 * rows without leaving a tombstone, so a precise count requires a
 * per-session counter that lands in WP09). The frontend renders the
 * "N earlier turns no longer available" placeholder only when
 * sweptCount > 0, so this is a feature-gated stub today.
 */
export interface ListMessagesResult {
  messages: Message[];
  sweptCount: number;
}

/**
 * ResumeMessageResult — wire shape returned by sessions.resumeMessage
 * (long-turn-resilience-01KR3PRS WP03). subscriptionId matches the LLM
 * stream-subscription contract so the caller drains the same
 * llm:stream-chunk + llm:stream-closed topics it uses for fresh turns.
 * originalMessageId echoes the partial bubble id the resume continues —
 * the caller uses it to grey out the original bubble when the
 * continuation lands.
 */
export interface ResumeMessageResult {
  subscriptionId: string;
  originalMessageId: string;
}

/**
 * ExportResult — wire shape returned by Sessions_Export.
 * Mirrors core/rpc/views/sessions.ExportResult (session-export-01NDFSEX05 WP02).
 *
 * path is the absolute path of the file written by the OS-native save dialog.
 * byteCount is the number of bytes written (main file only; sidecar
 * artifacts are excluded from the count).
 */
export interface ExportResult {
  path: string;
  byteCount: number;
}

/**
 * ImageDimensions — pixel dimensions of an image attachment.
 * Mirrors core/llm.ImageDimensions. (multimodal-io-01KQ8TDF WP04 / FR-003)
 */
export interface ImageDimensions {
  width: number;
  height: number;
}

/**
 * MediaSource — base64 / URI source of an image or document content
 * block. Mirrors core/llm.MediaSource. The Wails JSON wire shape keeps
 * the Go-side snake_case JSON tags verbatim.
 * SizeBytes and ImageDimensions are additive fields from WP04 (FR-003).
 */
export interface MediaSource {
  kind: string;
  media_type: string;
  data?: string;
  uri?: string;
  original_name?: string;
  /** Byte size of the source data (populated by the backend on Put). */
  size_bytes?: number;
  /** Pixel dimensions for image/* attachments; absent when unknown. */
  image_dimensions?: ImageDimensions;
}

/**
 * AttachmentLimitsView — per-provider attachment capability limits returned
 * by client.llm.getAttachmentLimits(). Mirrors
 * core/rpc/views/llm.AttachmentLimitsView. Zero values mean "unknown/unbounded".
 * (multimodal-io-01KQ8TDF WP04 / FR-007)
 */
export interface AttachmentLimitsView {
  imageInput: boolean;
  documentInput: boolean;
  /** Max bytes per image attachment; 0 = unbounded. */
  maxImageBytes: number;
  /** Max bytes per document attachment; 0 = unbounded. */
  maxDocumentBytes: number;
  /** Max image blocks per message; 0 = unbounded. */
  maxImageCountPerMessage: number;
  /** Max pixel count (W×H) per image; 0 = unbounded. */
  maxImagePixels: number;
  /** Max pages per PDF; 0 = unbounded. */
  maxDocumentPages: number;
  /** MIME types accepted for images; empty = accept all. */
  imageInputMimeTypes?: string[];
  /** MIME types accepted for documents; empty = accept all. */
  documentInputMimeTypes?: string[];
}

/**
 * RotationResult — outcome of LLM_TestAndRotateKey.
 * Mirrors core/rpc/views/llm.RotationResult.
 * (provider-keychain-rotation-01KQ8TD9 WP04)
 */
export interface RotationResult {
  success: boolean;
  message?: string;
  latency_ms: number;
  tested_at: string; // ISO-8601 timestamp
  /** Non-empty when a paused chat turn exists for this profile. */
  auto_resume_token?: string;
}

/**
 * ProviderKeyTestResult — outcome of LLM_TestProviderKey.
 * Mirrors core/rpc/views/llm.ProviderKeyTestResult.
 * (azure-openai-adapter-01KQ8VMZ WP03)
 */
export interface ProviderKeyTestResult {
  ok: boolean;
  model_count: number;
  deprecation_warning?: string;
  message?: string;
}

/**
 * AuthFailedPayload — payload of the `provider:auth-failed` broker event.
 * Emitted by the chat runner when an adapter returns *ErrProviderAuthFailed.
 * (provider-keychain-rotation-01KQ8TD9 WP05)
 */
export interface AuthFailedPayload {
  sub_id: string;
  session_id: string;
  profile_id: string;
  provider: string;
  model: string;
  reason: string;
}

/**
 * CapabilityMissingPayload — payload of the `provider:capability-missing`
 * broker event. Emitted by the chat runner when a custom OpenAI-compatible
 * endpoint's probed capability matrix blocks a request before any wire call.
 * (custom-openai-compatible-endpoint-01KQ8VN0 WP05)
 */
export interface CapabilityMissingPayload {
  capability: string;
  endpoint: string;
  profile_id: string;
}

/**
 * RetryAfterRotationFailedPayload — payload of the
 * `provider:retry-after-rotation-failed` broker event. Emitted when a
 * resumed (post-rotation) turn errors for non-auth reasons.
 * (provider-keychain-rotation-01KQ8TD9 WP05)
 */
export interface RetryAfterRotationFailedPayload {
  sub_id: string;
  session_id: string;
  profile_id: string;
  error_message: string;
}

/**
 * ContentBlock — one polymorphic fragment of a multimodal message.
 * Mirrors core/llm.ContentBlock. The wire shape uses the Go-side
 * snake_case JSON tags (`tool_use`, `tool_result`, `tool_data`,
 * `artifact_id`).
 *
 * `generated_image` variant carries either an artifact_id (post-WP02
 * auto-capture; bytes resolved via harness-artifact://<id>) OR an
 * inline `source` with raw bytes (pre-capture path). Renderer prefers
 * artifact_id when both are present. (multimodal-io-extended-01KQ8TD2
 * WP01)
 */
export interface ContentBlock {
  type: 'text' | 'image' | 'document' | 'generated_image' | 'tool_use' | 'tool_result';
  text?: string;
  source?: MediaSource;
  tool_use?: {
    id: string;
    name: string;
    input?: unknown;
  };
  tool_result?: {
    tool_use_id?: string;
    content?: unknown;
    is_error?: boolean;
  };
  /** Raw tool-shape payload kept opaque on the frontend (mirrors
   * core/llm.ContentBlock.ToolData json.RawMessage). */
  tool_data?: unknown;
  /** Set when type==="generated_image" and the auto-capture pipeline
   * has materialized the bytes into an artifact row. Empty for inline
   * (pre-capture) generated images. */
  artifact_id?: string;
}

/**
 * Streaming chunk delivered on the `llm:stream-chunk` topic. The broker
 * emits one per token / tool-call delta. The `done` flag marks the final
 * chunk before `llm:stream-closed` arrives.
 */
export interface StreamChunk {
  subscriptionId: string;
  sessionId: string;
  messageId: string;
  delta: string;
  toolCallDelta?: ToolCall;
  done?: boolean;
}

export interface StreamClosedPayload {
  id: string;
  reason: 'ctx-cancelled' | 'stop-called' | 'backend-error' | 'completed';
  message?: string;
}

/** Token / cost estimate surfaced by ChatInput. Placeholder values are OK
 * until the providers mission wires real accounting. */
export interface CostEstimate {
  tokens: number;
  usd: number;
}

/**
 * MemoryScopeKind — the three scope tiers used by long-term memory.
 * Mirrors core/memory.ScopeKind. "session" is the chat-local default;
 * "project" survives between sister sessions of the same project;
 * "global" is harness-wide. Promotion is monotonic
 * (session → project → global); demotion isn't supported.
 */
export type MemoryScopeKind = 'global' | 'project' | 'session';

/**
 * MemoryChunk — one persisted memory. In the hooks-driven architecture
 * most chunks are auto-persisted by the memory.persist post_send hook;
 * the chat surface still ships an explicit "remember this" button for
 * short turns the auto-path skips. The vector representation never
 * crosses the RPC boundary; the management UI only ever sees the
 * human-readable fields.
 *
 * Mirrors core/rpc/views/memory.Chunk. WP06 added the scope columns
 * (`scopeKind`, `scopeId`, `projectId`, `contentHash`) plus the auto-
 * persist metadata fields (`toolName`, `filesRead`, `filesModified`,
 * `title`).
 */
export interface MemoryChunk {
  id: string;
  sessionId?: string;
  projectId?: string;
  scopeKind: MemoryScopeKind;
  scopeId: string;
  contentHash?: string;
  sourceTurn?: string;
  content: string;
  createdAt: string;
  toolName?: string;
  filesRead?: string[];
  filesModified?: string[];
  title?: string;
  /** Bundle E WP15 — pinned chunks survive the prune sweep. */
  pinned?: boolean;
  /** Bundle E WP15 — recall hits since chunk creation. */
  recallCount?: number;
  /** Bundle E WP15 — last time this chunk was retrieved. */
  lastAccessed?: string;
  /** Bundle E WP16 — originating hook boundary ("post-llm" etc.). */
  source?: string;
  /** Narrative layer — chunk kind: "raw" | "narrative_extractive" | "narrative_synthesised" etc. */
  kind?: string;
  /** Narrative layer — retrieval weight (default 1.0 for raw, 1.5 for narrative). */
  retrievalWeight?: number;
  /** Narrative layer — originating turn ID. */
  turnId?: string;
}

/**
 * MemoryListFilter — frontend mirror of core/rpc/views/memory.ListFilter.
 * Each non-empty field acts as a conjunction. Empty filter returns
 * every chunk. Backend ignores fields the storage layer doesn't yet
 * index on; sessionId / projectId are accepted for forward
 * compatibility.
 */
export interface MemoryListFilter {
  scopeKind?: MemoryScopeKind;
  scopeId?: string;
  sessionId?: string;
  projectId?: string;
}

/**
 * MemoryJournalEntry — one row in the greedy-memory hook journal
 * (Bundle E WP16). The HookJournalView surfaces a tail so users can
 * audit what the kernel boundaries are capturing on their behalf.
 */
export interface MemoryJournalEntry {
  seq: number;
  boundary: string;
  scope: string;
  title?: string;
  source?: string;
  written: boolean;
  deduped: boolean;
  skipped: boolean;
  skipReason?: string;
  chunkId?: string;
  contentHash?: string;
  at: string;
}

/**
 * MemoryPruneStats — aggregated prune-sweep stats. Bundle E WP15.
 */
export interface MemoryPruneStats {
  startedAt: string;
  durationMs: number;
  kept: number;
  dropped: number;
  collapsed: number;
  pinned: number;
}

/**
 * MemoryPruneVerdict — per-chunk prune verdict.
 */
export interface MemoryPruneVerdict {
  id: string;
  action: 'keep' | 'drop' | 'collapse';
  reason?: string;
  keepScore: number;
  collapsedInto?: string;
}

/**
 * MemoryPruneRow — §2.5 confirmation modal row. One per would-be-dropped
 * or would-be-collapsed chunk, enriched with a snippet for user review.
 */
export interface MemoryPruneRow {
  id: string;
  snippet: string;
  reason: string;
  action: 'drop' | 'collapse';
}

/**
 * MemoryPrunePreview — dry-run prune result.
 * §2.5 extension: `rows` carries the drop/collapse subset enriched
 * with content snippets for the confirmation modal.
 */
export interface MemoryPrunePreview {
  verdicts: MemoryPruneVerdict[];
  stats: MemoryPruneStats;
  /** §2.5 — row list for the confirmation modal; may be undefined on
   * older backends that predate this field. */
  rows?: MemoryPruneRow[];
}

/**
 * MemoryHealthCounts — per-kind chunk breakdown (§2.4 health panel).
 */
export interface MemoryHealthCounts {
  total: number;
  raw: number;
  narrative: number;
  longTermPromoted: number;
  embedded: number;
  unembedded: number;
}

/**
 * MemoryHealthActivity — last-7-day activity window (§2.4).
 */
export interface MemoryHealthActivity {
  captured: number;
  pruned: number;
  promoted: number;
}

/**
 * MemoryHealthEmbedder — static embedder properties surfaced in the §2.4
 * health panel without a network call.
 */
export interface MemoryHealthEmbedder {
  kind: string;
  model: string;
  dimensions: number;
}

/**
 * MemoryHealthSnapshot — response from Memory_HealthSnapshot (§2.4).
 * All counts come from an indexed O(n) pass; no full-table scan.
 */
export interface MemoryHealthSnapshot {
  counts: MemoryHealthCounts;
  activity: MemoryHealthActivity;
  embedder: MemoryHealthEmbedder;
  capturedAt: string; // RFC3339
}

/**
 * MemoryCaptureRateSnapshot — response from Memory_CaptureRate (§2.7).
 * Drives the LegendBar capture-rate pill.
 */
export interface MemoryCaptureRateSnapshot {
  chunksPerMinute: number;
  embedderHealth: 'ok' | 'slow' | 'error';
  lastErrorAt: string | null; // RFC3339 or null
  recentErrorCount: number;
}

/**
 * MemoryEmbedderEligibility — response from Memory_EmbedderEligibility.
 *
 * Summarises which provider profiles are capable of supplying embeddings.
 * The Settings → Memory banner uses this to surface a contextual warning when
 * the user has only Anthropic-direct or Bedrock profiles (no embeddings API).
 *
 * Mirrors core/memory.EmbedderEligibility.
 */
export interface MemoryEmbedderEligibility {
  /** true when at least one configured profile can supply embeddings. */
  hasEligible: boolean;
  /** Total number of profiles that were examined. */
  allProfiles: number;
  /** Count of profiles that are eligible for embeddings. */
  eligibleProfiles: number;
  /**
   * Unique provider kinds that are present but cannot supply embeddings by
   * design (e.g. "anthropic", "bedrock"). Rendered per-provider in the banner.
   */
  skippedKinds: string[];
}

/**
 * NarrativeJobStatus — wire shape for a failed narrative synthesis job.
 * Surfaces in the Memory view "N narratives unrecoverable" banner
 * (memory-narrative-layer-01KQ8TD1 WP07).
 */
export interface NarrativeJobStatus {
  id: string;
  turnId: string;
  sessionId: string;
  attempt: number;
  lastError: string;
  createdAt: string; // RFC3339
}

/**
 * NarrativeMetrics — retrieval/citation/pin counters and computed
 * promotion score for one chunk (memory-narrative-layer-01KQ8TD1 WP07).
 */
export interface NarrativeMetrics {
  chunkId: string;
  retrievals: number;
  citations: number;
  userPins: number;
  score: number;
  lastRetrievedAt?: string; // RFC3339
  lastCitedAt?: string; // RFC3339
}

/**
 * ScoredChunk — one chunk ranked by cosine similarity against a query
 * (memory-inspection-ui-01KX5R8E §2.1/§2.2).
 */
export interface ScoredChunk {
  chunk: MemoryChunk;
  similarity: number;
  /** true when similarity >= the retriever's configured threshold (was injected). */
  injected: boolean;
}

/**
 * RetrievalReport — the most recent retrieval call for a session
 * (memory-inspection-ui-01KX5R8E §2.1, FR-001).
 */
export interface RetrievalReport {
  sessionId: string;
  query: string;
  results: ScoredChunk[];
  threshold: number;
  at: string; // RFC3339
}

/**
 * ChunkProvenance — full audit chain for one chunk
 * (memory-inspection-ui-01KX5R8E §2.6, FR-007).
 */
export interface ChunkProvenance {
  chunkId: string;
  sourceTurn?: string;
  hookBoundary?: string;
  kind?: string;
  scopePath: string;
  pinned: boolean;
  retrievalCount: number;
  citationCount: number;
  promotionScore: number;
  embedderKind?: string;
  embedDimensions?: number;
  createdAt: string; // RFC3339
}

/**
 * DialScope — the cascading-config layer keys.
 */
export type DialScope =
  | 'global'
  | 'project'
  | 'session'
  | 'graph'
  | 'run';

/**
 * DialScopeKey — addresses one cascading layer.
 */
export interface DialScopeKey {
  scope: DialScope;
  id?: string;
}

/**
 * DialConfig — the wire shape for one cascading layer's overrides.
 * Each *Set boolean toggles whether the value is an explicit override
 * or "use cascade".
 */
export interface DialConfig {
  maxTokensPerRun?: number;
  maxTokensPerRunSet?: boolean;
  maxWallclockSeconds?: number;
  maxWallclockSet?: boolean;
  maxLLMCalls?: number;
  maxLLMCallsSet?: boolean;
  maxToolCalls?: number;
  maxToolCallsSet?: boolean;
  maxCostUSD?: number;
  maxCostUSDSet?: boolean;
  planVerbosity?: string;
  planVerbositySet?: boolean;
  askThreshold?: number;
  askThresholdSet?: boolean;
  reflectFrequency?: number;
  reflectFrequencySet?: boolean;
  compactionAggressiveness?: number;
  compactionAggressivenessSet?: boolean;
  reviewIterationsCap?: number;
  reviewIterationsCapSet?: boolean;
  memoryHooksEnabled?: boolean;
  memoryHooksEnabledSet?: boolean;
  memoryPruneIntervalSeconds?: number;
  memoryPruneIntervalSet?: boolean;
  updatedAt?: string;
}

/**
 * DialEffectiveField<T> — one resolved field's value plus the layer
 * that contributed it.
 */
export interface DialEffectiveField<T> {
  value: T;
  from: DialScope;
}

/**
 * DialEffectiveDials — the resolved cascade output.
 */
export interface DialEffectiveDials {
  maxTokensPerRun: DialEffectiveField<number>;
  maxWallclockSeconds: DialEffectiveField<number>;
  maxLLMCalls: DialEffectiveField<number>;
  maxToolCalls: DialEffectiveField<number>;
  maxCostUSD: DialEffectiveField<number>;
  planVerbosity: DialEffectiveField<string>;
  askThreshold: DialEffectiveField<number>;
  reflectFrequency: DialEffectiveField<number>;
  compactionAggressiveness: DialEffectiveField<number>;
  reviewIterationsCap: DialEffectiveField<number>;
  memoryHooksEnabled: DialEffectiveField<boolean>;
  memoryPruneIntervalSeconds: DialEffectiveField<number>;
}

/**
 * DialDelta — additive bump used by BumpAndResume.
 */
export interface DialDelta {
  addTokensPerRun?: number;
  addWallclockSeconds?: number;
  addLLMCalls?: number;
  addToolCalls?: number;
  addCostUSD?: number;
}

/**
 * Lifecycle-hook event names. Mirrors core/hooks.Event* constants.
 */
export type HookEvent =
  | 'pre_send'
  | 'post_send'
  | 'pre_save_session'
  | 'post_assistant_turn_complete';

/**
 * Hook kind — selects the dispatch strategy. `builtin` runs a Go
 * function shipped with the harness; `shell` execs a user command and
 * speaks the JSON stdin/stdout protocol; `mcp` invokes an MCP tool
 * (stub-only in v1).
 */
export type HookKind = 'builtin' | 'shell' | 'mcp';

export interface HookMatch {
  sessionIds?: string[];
  kinds?: string[];
  models?: string[];
}

/**
 * Hook — one configured lifecycle hook. Mirrors core/hooks.Hook.
 *
 * `timeoutMs` is the typed per-hook execution timeout (ms). It mirrors
 * the Go-side `Hook.TimeoutMs` (`timeout_ms`) field and supersedes the
 * `config.timeout_ms` workaround the HookEditor used in v0.8.0.
 */
export interface Hook {
  id: string;
  name: string;
  event: HookEvent;
  kind: HookKind;
  enabled: boolean;
  match: HookMatch;
  builtin?: string;
  command?: string;
  mcpTool?: string;
  timeoutMs?: number;
  config?: Record<string, unknown>;
}

/**
 * BuiltinDescriptor — one entry in the AvailableBuiltins dropdown.
 */
export interface BuiltinDescriptor {
  id: string;
  name: string;
  description: string;
  events: HookEvent[];
  defaultConfig?: Record<string, unknown>;
}

/**
 * HookOutput — the discriminated-union result a single hook returns.
 * Mirrors core/hooks.HookOutput (plan §2 / FR-002).
 */
export interface HookOutput {
  decision?: 'approve' | 'block' | string;
  reason?: string;
  additionalContext?: string;
  updatedInput?: unknown;
  updatedMCPOutput?: unknown;
  permissionDecision?: 'allow' | 'deny' | string;
  permissionDecisionReason?: string;
  watchPaths?: string[];
  async?: boolean;
  asyncTimeoutMs?: number;
}

/**
 * MergedOutput — the folded result after MergeOutputs runs over N HookOutputs.
 * Mirrors core/hooks.MergedOutput.
 */
export interface MergedOutput {
  blocked: boolean;
  blockReason?: string;
  additionalContext?: string;
  updatedInput?: unknown;
  updatedMCPOutput?: unknown;
  permissionDenied: boolean;
  permissionAllowed: boolean;
  permissionReason?: string;
  watchPaths?: string[];
}

/**
 * DryRunResult — the wire shape returned by Hooks_DryRun.
 */
export interface DryRunResult {
  output: HookOutput;
  merged: MergedOutput;
  stdout?: string;
  stderr?: string;
  exitCode: number;
  latencyMs: number;
}

// ── MCP recipes (shipped catalog) ─────────────────────────────────────
//
// Mirrors the Wails wire shape from `core/rpc/views/tools/api.go`:
//
//   - `RecipeListing` is camelCase (the Go struct uses camelCase JSON
//     tags).
//   - The embedded `recipe` and `status` carry through their Go-side
//     JSON tags verbatim, which are snake_case (e.g. `display_name`,
//     `env_keys`, `last_error`). The TS shapes here normalise that to
//     camelCase via a thin adapter in harnessClient.ts; consumers of
//     these types see camelCase throughout.

/**
 * RecipeCategory groups recipes in the Tools panel. Drives the icon
 * mapping in `KaneazToolsPanel.vue` (search→Search, filesystem→Folder,
 * memory→Brain, fetch→Globe, default→Wrench).
 */
export type RecipeCategory =
  | 'search'
  | 'filesystem'
  | 'memory'
  | 'fetch'
  | 'other';

/**
 * EnvKey — one credential-bearing env var the recipe's server reads.
 * Render order in the install modal follows the `Recipe.envKeys`
 * slice order.
 */
export interface EnvKey {
  /** Exact env var name the server looks up (e.g. "BRAVE_API_KEY"). */
  name: string;
  /** Modal label. */
  display: string;
  /** Provider's API-key issuance page (optional). */
  docsUrl?: string;
  /** Required keys block install when missing; the modal asterisks them. */
  required: boolean;
}

/**
 * RecipeCapabilities is the recipe-author's declaration of which MCP
 * capabilities the server advertises; the modal copy reads this and
 * the negotiated capability set lives in `RecipeStatus.serverName/Version`.
 */
export interface RecipeCapabilities {
  tools: boolean;
  resources: boolean;
  prompts: boolean;
  sampling: boolean;
}

/**
 * ConfigKind drives the input shape rendered by the install modal for
 * a given ConfigOption. Mirrors the `ConfigKind*` constants in
 * core/mcp/recipes/recipes.go.
 */
export type ConfigKind = 'directory_list' | 'boolean' | 'string' | 'enum';

/**
 * ConfigOption — one user-editable knob declared by a recipe. The
 * filesystem recipe declares `allowed_directories` (directory_list);
 * future recipes may declare booleans (e.g. read_only), free-form
 * strings, or enums (closed-set choices rendered as a dropdown).
 * `default` may carry the `${DATA_DIR}` substitution token for
 * directory_list defaults — the backend expands the token at install
 * time, the modal renders the literal as a placeholder.
 */
export interface ConfigOption {
  name: string;
  display: string;
  kind: ConfigKind;
  default?: unknown;
  required: boolean;
  description: string;
  /**
   * Closed set of allowed values when `kind === 'enum'`. The install
   * modal renders these as a dropdown; ignored for any other kind.
   */
  choices?: string[];
}

/**
 * Recipe — one shipped catalog entry. Mirrors `core/mcp/recipes.Recipe`
 * (the catalog metadata, no live state).
 *
 * `argsTemplate` and `configOptions` are added by WP01 of the
 * filesystem-mcp-recipe mission. Recipes that take only env keys
 * (e.g. Brave Search) leave them undefined / empty.
 */
export interface Recipe {
  id: string;
  displayName: string;
  description: string;
  category: RecipeCategory;
  envKeys: EnvKey[];
  capabilities: RecipeCapabilities;
  docsUrl?: string;
  argsTemplate?: string[];
  configOptions?: ConfigOption[];
  /**
   * Optional hazard message rendered by the install modal in a stark
   * red banner with an explicit confirmation checkbox. Set on recipes
   * that grant elevated trust (e.g. `filesystem-full`, the unrestricted
   * filesystem MCP). Empty / undefined for ordinary recipes.
   */
  warning?: string;
  /**
   * Optional filename (under `core/policy/cedar/policies/`) of a Cedar
   * policy template the user is encouraged to install alongside this
   * recipe. The install modal surfaces a "copy recommended policy"
   * button when set; copying drops the file into
   * `<DataDir>/policy/`. Pairs with `warning`.
   */
  recommendedPolicyTemplate?: string;
}

/**
 * RecipeState is the lifecycle state of a recipe's stdio child process.
 * Terminal states are `stopped`, `running`, and `failed`; `starting` and
 * `restarting` are transient and the polling loop watches them.
 */
export type RecipeState =
  | 'stopped'
  | 'starting'
  | 'running'
  | 'restarting'
  | 'failed';

/**
 * RecipeStatus — live snapshot of one recipe's child process. Mirrors
 * `core/mcp/stdio.RecipeStatus`.
 */
export interface RecipeStatus {
  id: string;
  enabled: boolean;
  state: RecipeState;
  lastError?: string;
  restartAttempts: number;
  keysPresent: boolean;
  pid: number;
  protocolVersion?: string;
  serverName?: string;
  serverVersion?: string;
  toolCount: number;
  resourceCount: number;
  promptCount: number;
  stderrTail?: string;
}

// ── Artifacts (artifacts-storage WP01..WP03) ─────────────────────────
//
// Mirrors core/rpc/views/artifacts.Artifact. The wire shape is camelCase
// with a snake_case-free `sourceRef` substruct. The bytes channel carries
// base64 over the Wails boundary (Go-side []byte → JSON base64 string)
// even though the autogenerated wailsjs types call it `number[]`.

/**
 * ArtifactSource — provenance kind. `code_block` = a fenced block in an
 * assistant message with a `title=` attribute; `tool_output` = a long
 * tool-result emitted by the toolloop; `user_pin` = a manual save via
 * Sessions_SaveAsArtifact / right-click "Save as artifact";
 * `model_output` = an image generated by a model generation API
 * (DALL-E 3, gpt-image-1, Titan Image) and auto-captured by the
 * multimodal output pipeline (multimodal-io-extended-01KQ8TD2 WP02).
 */
export type ArtifactSource = 'code_block' | 'tool_output' | 'user_pin' | 'model_output';

/**
 * ArtifactScope — promotion tier. v1 carries `session` (default at
 * capture time) and `project` (after promotion). A future `global`
 * tier is reserved by the spec but not wired in v1.
 */
export type ArtifactScope = 'session' | 'project';

/**
 * ArtifactSourceRef — provenance back to the originating message /
 * tool call so the UI can render a "from message" backlink chip.
 */
export interface ArtifactSourceRef {
  messageId: string;
  toolCallId?: string;
  codeBlockIndex?: number;
  filename?: string;
  /**
   * Zero-based index of the generated image within the assistant turn
   * (present only for model_output artifacts from multi-image prompts).
   * Mirrors core/artifacts.ArtifactSourceRef.ImageIndex.
   * (multimodal-io-extended-01KQ8TD2 WP02)
   */
  imageIndex?: number;
  /**
   * Provider-rewritten prompt returned alongside the generated image
   * (e.g. DALL-E 3 safety-augmented prompt). Mirrors
   * core/artifacts.ArtifactSourceRef.RevisedPrompt.
   * (multimodal-io-extended-01KQ8TD2 WP02)
   */
  revisedPrompt?: string;
}

/**
 * Artifact — the wire shape of one stored artifact.
 */
export interface Artifact {
  id: string;
  sessionId: string;
  projectId?: string;
  title: string;
  mimeType: string;
  contentHash: string;
  byteSize: number;
  source: ArtifactSource;
  sourceRef: ArtifactSourceRef;
  scopeKind: ArtifactScope;
  /** ISO RFC3339Nano UTC. */
  createdAt: string;
}

/**
 * ArtifactFilter — narrowing filter for `Artifacts_List`. Each
 * non-empty field acts as a conjunction. Empty filter returns every
 * artifact.
 */
export interface ArtifactFilter {
  sessionId?: string;
  projectId?: string;
  mimeTypePrefix?: string;
  source?: ArtifactSource;
  scopeKind?: ArtifactScope;
}

/**
 * ArtifactWithBytes — wire envelope returned by `Artifacts_Get`. The
 * Go side encodes the on-disk CAS bytes as base64; consumers decode
 * via `atob` (text mimes) or feed the base64 to a `data:` URL
 * (image / html / download).
 */
export interface ArtifactWithBytes {
  artifact: Artifact;
  /** base64-encoded payload — Wails serialises Go `[]byte` to base64. */
  bytes: string;
}

/**
 * RecipeListing — one row returned from `Tools_ListRecipes`. Combines
 * the catalog metadata with the harness-side overlay (enabled flag,
 * live status snapshot, keys-resolvable hint).
 */
export interface RecipeListing {
  recipe: Recipe;
  enabled: boolean;
  status: RecipeStatus;
  keysPresent: boolean;
}

// ── slash commands ───────────────────────────────────────────────────

/**
 * SlashCommandInfo — one row returned by `Slash_List`. Drives the
 * autocomplete dropdown the chat composer renders when the input
 * starts with a `/`. ComingSoon flags v1 stubs (memorize, recall,
 * forget, branch); the dropdown renders them with a
 * "(coming soon)" tag. isUser flags user-defined commands so the
 * autocomplete renders a "user" chip.
 */
export interface SlashCommandInfo {
  name: string;
  description: string;
  comingSoon: boolean;
  isUser?: boolean;
}

// ── user-defined slash commands (user-slash-commands-01KQ8TD9) ───────

export type UserCommandScope = 'global' | 'project';
export type UserCommandKind = 'text' | 'tool' | 'prompt';
export type UserCommandInputKind =
  | 'text'
  | 'enum'
  | 'number'
  | 'file'
  | 'artifact_ref'
  | 'project_ref';

export interface UserCommandInput {
  name: string;
  kind: UserCommandInputKind;
  required: boolean;
  enumValues?: string[];
  default?: string;
  hint?: string;
}

/**
 * UserCommandSummary — lightweight wire shape returned by Slashcmd_List.
 */
export interface UserCommandSummary {
  name: string;
  scope: UserCommandScope;
  projectId?: string;
  kind: UserCommandKind;
  description: string;
  modelInvokable: boolean;
  icon?: string;
  hiddenFromPanel?: boolean;
  updatedAt?: number;
}

/**
 * UserCommand — full detail shape returned by Slashcmd_Get.
 */
export interface UserCommand {
  name: string;
  scope: UserCommandScope;
  projectId?: string;
  kind: UserCommandKind;
  description: string;
  whenToUse?: string;
  doesNotHandle?: string;
  modelInvokable: boolean;
  icon?: string;
  hiddenFromPanel?: boolean;
  body?: string;
  tool?: string;
  toolArgsTemplate?: string;
  inputs?: UserCommandInput[];
  payloadPath?: string;
  createdAt?: number;
  updatedAt?: number;
}

/**
 * RunResult — wire shape returned by Slashcmd_Run.
 */
export interface SlashRunResult {
  kind: SlashResultKind;
  text: string;
  renderedArgs?: string[];
  toolName?: string;
  metadata?: Record<string, unknown>;
}

/**
 * SlashResultKind — discriminator the chat surface uses to style the
 * inline result bubble. The Go side surfaces one of these four.
 */
export type SlashResultKind = 'info' | 'error' | 'warning' | 'system';

/**
 * SlashExecuteResult — wire shape returned by `Slash_Execute`. The
 * `text` body renders inline (system/info/warning/error bubble);
 * `metadata` carries well-known keys the SessionsView reads to apply
 * local side effects (e.g. `modelId` + `providerId` for `/model`).
 */
export interface SlashExecuteResult {
  text: string;
  kind: SlashResultKind;
  metadata?: Record<string, unknown>;
}

// ── reasoning knob (provider-implementation-uniformity-01KQ8V4F WP07) ──

/**
 * ReasoningConfig — the wire shape returned in SlashExecuteResult.metadata
 * under the `reasoningKnob` key after a successful /effort command.
 * Mirrors the Go llm.ReasoningConfig struct.
 *
 * Exactly one field is set:
 *   - openAIEffort:              "low" | "medium" | "high" | "minimal"
 *   - anthropicThinkingBudget:   integer token budget > 0
 */
export interface ReasoningConfig {
  openAIEffort?: string;
  anthropicThinkingBudget?: number;
}

/**
 * ReasoningStyle — discriminates how the active model expresses its
 * reasoning-effort knob. Used by ReasoningControl.vue to switch the
 * display mode. Mirrors the Go llm.ReasoningStyle enum values.
 *
 * - "none"         — model has no reasoning knob (control is hidden)
 * - "effort_string"— OpenAI o-series: low/medium/high/minimal
 * - "token_budget" — Anthropic: integer token budget
 * - "both"         — model accepts either form
 */
export type ReasoningStyle = 'none' | 'effort_string' | 'token_budget' | 'both';

// ── corpora (agent-kernel-graph; Bundle C WP10/WP11) ─────────────────

/**
 * CorpusScope discriminates the visibility of a corpus.
 */
export type CorpusScope = 'global' | 'project' | 'session';

/**
 * IngestState enumerates the lifecycle of a background ingest job.
 */
export type IngestState =
  | 'pending'
  | 'running'
  | 'completed'
  | 'failed'
  | 'canceled';

/**
 * Corpus — a named pool of source files that have been walked,
 * hashed, chunked, and embedded for top-K retrieval.
 */
export interface Corpus {
  id: string;
  name: string;
  scope: CorpusScope;
  scopeId?: string;
  tag?: string;
  createdAt: string;
  updatedAt: string;
}

/**
 * CorpusFile — one ingested source file. `sha256` enables hash-based
 * skip on re-ingest; matching files are not re-embedded.
 */
export interface CorpusFile {
  id: string;
  corpusId: string;
  path: string;
  sha256: string;
  fileSize: number;
  lineCount: number;
  ingestedAt: string;
}

/**
 * ChunkProvenance — the (file path + line range + hash) tuple a
 * retrieved chunk carries so the chat surface can render a citation.
 */
export interface CorpusChunkProvenance {
  filePath: string;
  lineStart: number;
  lineEnd: number;
  sha256: string;
}

/**
 * CorpusChunk — one indexed text fragment. Embedding bytes never
 * cross the Wails boundary; `text` is the user-readable payload.
 */
export interface CorpusChunk {
  id: string;
  corpusId: string;
  fileId: string;
  chunkSeq: number;
  text: string;
  provenance: CorpusChunkProvenance;
  createdAt: string;
}

/**
 * CorpusRetrievalResult pairs a chunk with its similarity score.
 */
export interface CorpusRetrievalResult {
  chunk: CorpusChunk;
  similarity: number;
}

/**
 * CorpusIngestStatus — pollable state of a background ingest job.
 */
export interface CorpusIngestStatus {
  jobId: string;
  corpusId: string;
  state: IngestState;
  path: string;
  filesTotal: number;
  filesDone: number;
  filesSkip: number;
  chunksTotal: number;
  startedAt: string;
  updatedAt: string;
  finishedAt?: string;
  error?: string;
}

/**
 * CorpusIngestOptions narrows ingest behaviour.
 */
export interface CorpusIngestOptions {
  recursive?: boolean;
  extensions?: string[];
  maxFileBytes?: number;
  chunkLines?: number;
}

/**
 * CorpusCreateRequest — body for CreateCorpus.
 */
export interface CorpusCreateRequest {
  name: string;
  scope: CorpusScope;
  scopeId?: string;
  tag?: string;
}

/**
 * CorpusRetrieveRequest — body for Retrieve.
 */
export interface CorpusRetrieveRequest {
  query: string;
  topK?: number;
  tokenBudget?: number;
  tag?: string;
  scope?: CorpusScope;
}

/**
 * CorpusRetrieveResponse pairs the results with a `dropped` count
 * indicating how many top-K chunks were truncated by the token budget.
 */
export interface CorpusRetrieveResponse {
  results: CorpusRetrievalResult[];
  dropped: number;
}

// ─── Agent graph (mission agent-kernel-graph; Bundle A WP06) ─────────

/**
 * GraphScope narrows ListGraphs:
 *   - "library" — bundled / shipped graphs (read-only)
 *   - "user"    — on-disk user graphs at <DataDir>/agent_graph/library/
 *   - ""        — both layers (library first then user)
 */
export type GraphScope = '' | 'library' | 'user';

/**
 * GraphInfo — one row in the graph library list. The frontend's
 * GraphsView renders these; SaveGraph is the inverse (write a YAML
 * payload by id).
 */
export interface GraphInfo {
  id: string;
  name?: string;
  description?: string;
  scope: 'library' | 'user';
  source?: string;
  updatedAt?: string;
}

/**
 * GraphSpec is the editable graph payload. The kernel parses YAML on
 * load + dump so the frontend can drive a textarea / Monaco instance
 * without modelling the typed node-attrs structure.
 */
export interface GraphSpec {
  id: string;
  name?: string;
  scope: 'library' | 'user';
  yaml: string;
}

/**
 * GraphValidationIssue is one validator violation. Stable shape so
 * the frontend can render `rule` distinctly from `message`.
 */
export interface GraphValidationIssue {
  rule: string;
  message: string;
}

/**
 * GraphValidationResult — green path returns ok=true with empty issues.
 */
export interface GraphValidationResult {
  ok: boolean;
  issues: GraphValidationIssue[];
}

/** RunState mirrors the kernel's emitted lifecycle. */
export type GraphRunState = 'running' | 'paused' | 'completed' | 'failed';

/** PendingAsk surfaces a parked AskNode question for the resume UI. */
export interface GraphPendingAsk {
  nodeId: string;
  question: string;
}

/** RunStatus is the snapshot the RunView renders. */
export interface GraphRunStatus {
  runId: string;
  graphId: string;
  sessionId?: string;
  state: GraphRunState;
  startedAt: string;
  updatedAt: string;
  completedAt?: string;
  error?: string;
  nodesComplete: number;
  llmTokens: number;
  llmCalls: number;
  toolCalls: number;
  costUsd: number;
  pendingAsk?: GraphPendingAsk;
}

/** RunTraceEvent is one row of the EventLog tail. */
export interface GraphRunTraceEvent {
  seq: number;
  runId: string;
  nodeId?: string;
  kind: string;
  ts: string;
  payload?: string;
}

/** StartRunRequest — body for StartRun. */
export interface GraphStartRunRequest {
  graphId: string;
  sessionId?: string;
  inputs?: Record<string, unknown>;
}

/** StartRunResponse pairs the new run id with its initial status. */
export interface GraphStartRunResponse {
  runId: string;
  status: GraphRunStatus;
}

// ── Node manifest catalog (mission agent-kernel-graph-node-catalog WP07) ─

/**
 * AttrProvenance maps a resolved-manifest field path (e.g.
 * "attrs.max_tokens", "defaults.temperature") to the inheritance layer
 * that contributed its effective value. Layer values:
 *   - "shipped"        — the bundled manifest's leaf layer
 *   - "archetype-<id>" — inherited from the named archetype layer
 *   - "user-override"  — user manifest at <DataDir>/agent_graph/nodes/
 *
 * The frontend's NodeInheritanceTooltip surfaces this on hover (FR-024).
 */
export interface AttrProvenance {
  fieldPath: string;
  layer: string;
}

/** AttrSpec is the wire-shaped attribute descriptor. */
export interface NodeAttrSpec {
  name: string;
  type: string;
  required?: boolean;
  default?: unknown;
  enum?: string[];
  min?: number;
  max?: number;
  minLength?: number;
  maxLength?: number;
  description?: string;
}

/** PortSpec is the wire-shaped port descriptor. */
export interface NodePortSpec {
  name: string;
  type: string;
  description?: string;
  defaultFor?: string;
}

/** PortSet pairs the merged input + output port lists. */
export interface NodePortSet {
  inputs?: NodePortSpec[];
  outputs?: NodePortSpec[];
}

/**
 * NodeManifestSummary is one catalog row. The palette tree lists these
 * without fetching the full attribute schema.
 */
export interface NodeManifestSummary {
  id: string;
  kindName?: string;
  displayName?: string;
  description?: string;
  category?: string;
  extends?: string;
  archetype?: string;
  callable: boolean;
  aliases?: string[];
  source?: string;
  hash?: string;
  version?: string;
}

/**
 * NodeManifestDetail is the full resolved manifest with per-field
 * provenance. The attribute editor consumes this shape.
 */
export interface NodeManifestDetail {
  summary: NodeManifestSummary;
  chain: string[];
  attrs: NodeAttrSpec[];
  ports: NodePortSet;
  defaults?: Record<string, unknown>;
  budgetConsumes?: string[];
  budget?: string;
  executor?: string;
  provenance: AttrProvenance[];
}

/**
 * ReloadResult is the diff returned by Nodes_ReloadOverrides.
 */
export interface NodeReloadResult {
  added: string[];
  removed: string[];
  modified: string[];
  errors?: string[];
  reloadedAt?: string;
}

/**
 * UserOverrideInfo describes one *.yaml under the user-override dir
 * after a parse pass. status === "ok" for accepted files; otherwise
 * `error` carries the per-file message.
 */
export interface NodeUserOverrideInfo {
  path: string;
  filename: string;
  id?: string;
  status: 'ok' | 'error';
  error?: string;
  sizeBytes?: number;
}

/**
 * NodeDoctorReport summarises catalog health for the NodesView debug
 * panel (mission agent-kernel-graph-node-catalog WP08). Counters are
 * non-negative ints; lastReloadAt is RFC3339Nano (empty before any
 * reload). userOverrideErrors carries the per-file parse failures
 * recorded by the most-recent reload pass.
 */
export interface NodeDoctorReport {
  shippedCount: number;
  userOverrideCount: number;
  archetypeCount: number;
  callableCount: number;
  aliasCount: number;
  userDir?: string;
  hotReloadEnabled: boolean;
  lastReloadAt?: string;
  userOverrideErrors?: string[];
  sunsetVersion?: string;
}

// ── compaction (mission agent-kernel-graph; Bundle D WP12/WP13) ──────

/**
 * CompactionSite labels the kernel firing point that triggered a
 * compaction. Three sites: token-budget pre-call, post-tool result
 * trim, and the user-facing manual trigger.
 */
export type CompactionSite = 'pre_call' | 'post_tool' | 'manual';

/**
 * CompactionStrategy identifies one of the four compaction algorithms
 * the kernel ships out of the box.
 */
export type CompactionStrategy =
  | 'summary'
  | 'drop_oldest'
  | 'semantic_cluster'
  | 'custom_subgraph';

/**
 * CompactionLayer identifies one rung in the cascading-config chain.
 * The resolver walks global → project → session → run → node and
 * merges the layers; per-node config wins where set.
 */
export type CompactionLayer =
  | 'global'
  | 'project'
  | 'session'
  | 'run'
  | 'node';

/**
 * CompactionSiteConfig is the per-site, per-layer configuration shape.
 * Empty fields fall through to the next layer.
 */
export interface CompactionSiteConfig {
  enabled: boolean;
  strategy?: CompactionStrategy;
  preCallThreshold?: number;
  toolResultMaxBytes?: number;
  maxRecursionDepth?: number;
  dropOldestKeepRecentN?: number;
  semanticClusterCount?: number;
  summaryProvider?: string;
  summaryModel?: string;
  subgraphInputPort?: string;
  subgraphOutputPort?: string;
  customGraphId?: string;
}

/**
 * CompactionConfig is one layer's contribution. The resolver
 * persists one of these per (layer, scopeId) pair.
 */
export interface CompactionConfig {
  sites?: Partial<Record<CompactionSite, CompactionSiteConfig>>;
}

/**
 * CompactionEffectiveConfig is the merged-cascade view: the effective
 * value for each field plus a per-(site, field) attribution map
 * showing which layer ultimately supplied each value.
 */
export interface CompactionEffectiveConfig {
  config: CompactionConfig;
  attribution: Partial<Record<CompactionSite, Record<string, CompactionLayer>>>;
}

/**
 * CompactionScopeKey identifies a resolution chain. Empty fields fall
 * through to the next layer.
 */
export interface CompactionScopeKey {
  projectId?: string;
  sessionId?: string;
  runId?: string;
  nodeId?: string;
}

/**
 * CompactionCustomStrategy is one row in the agent-graph library that
 * is wired as a custom_subgraph compaction strategy.
 */
export interface CompactionCustomStrategy {
  graphId: string;
  name: string;
  description?: string;
}

/**
 * CompactionManualOpts narrow the manual-compaction trigger.
 */
export interface CompactionManualOpts {
  strategy?: CompactionStrategy;
  dropOldestKeepRecentN?: number;
  semanticClusterCount?: number;
  summaryProvider?: string;
  summaryModel?: string;
  customGraphId?: string;
}

/**
 * CompactionManualResult is what TriggerManualCompaction returns.
 */
export interface CompactionManualResult {
  strategy: CompactionStrategy;
  bytesSaved: number;
  skipped?: boolean;
  reason?: string;
}

/**
 * CompactionAggressiveness — one of the five locked tiers from mission
 * compaction-strategy-ui-01KQ8TDI §2.2. The Settings panel's
 * five-stop slider emits these as the wire-stable tier name; the chat
 * runner dispatches on the same enum.
 */
export type CompactionAggressiveness =
  | 'off'
  | 'conservative'
  | 'balanced'
  | 'aggressive'
  | 'maximal';

/**
 * CompactionTierExplain — one row of the tier-explain payload returned
 * by Compaction.GetTierExplain(). Drives the "What does this mean?"
 * disclosure on the Settings panel's compaction-aggressiveness dial.
 * Numerics come from core/compaction.Tier() via the binding so the UI
 * can never drift from the engine.
 */
export interface CompactionTierExplain {
  aggressiveness: CompactionAggressiveness;
  /** Human-facing tier label, e.g. "Balanced (default)". */
  label: string;
  /** Tooltip body — one paragraph explaining the trade-off. */
  description: string;
  /** Current/cap fraction at which threshold-mode compaction kicks
   * off. Zero for off / maximal tiers. */
  triggerPct: number;
  /** Fraction of oldest tokens folded into the summary. Zero for off /
   * maximal tiers. */
  summarizePct: number;
  /** Engine path identifier — "none" | "threshold" | "rolling". */
  mode: 'none' | 'threshold' | 'rolling';
}

/**
 * ProviderProfileRef — wire shape mirroring core/rpc/views/settings's
 * ProviderProfileRef Go struct. Identifies a provider+model pair the
 * Settings panel's compaction-model picker emits. Empty fields == "use
 * the session's active model" (chained default).
 */
export interface ProviderProfileRef {
  providerId?: string;
  modelId?: string;
}

// ── conversation branches (agent-kernel-graph; Bundle B WP07/08) ──────

/**
 * BranchKind discriminates how the branch was created.
 */
export type BranchKind = 'fork' | 'linear_continuation';

/**
 * BranchStatus tracks the branch's lifecycle state.
 */
export type BranchStatus = 'active' | 'merging' | 'merged' | 'abandoned';

/**
 * Branch — one fork off a parent session. v1 branches are flat: a
 * branch always points at one parent and one child session, and there
 * is no branch-of-branch (spec FR-040 defers that to v2).
 */
export interface Branch {
  id: string;
  parentSessionId: string;
  childSessionId: string;
  kind: BranchKind;
  status: BranchStatus;
  providerId?: string;
  modelId?: string;
  title?: string;
  taskHint?: string;
  createdAt: string;
  updatedAt: string;
  mergedAt?: string;
  abandonedAt?: string;
  /**
   * True when this branch was spawned via the branch-advisor
   * (branch-as-subagent-recommendation WP04).
   */
  subagentBranch?: boolean;
  /** Correlates with KindBranchAdvisorAccepted audit event. */
  recommendationId?: string;
  /** Positive-signal labels that fired for this branch. */
  advisorSignals?: string[];
  /** Parent session id — populated on subagent branches. */
  parentSessionIdRef?: string;
}

/**
 * BranchReintegrationProposal — wire shape returned by
 * Branches_ProposeReintegrationSummary. Carries the model-generated
 * summary text, the token count it consumed, and the model name used.
 * An empty ProposedSummary signals an empty branch (zero user/assistant
 * turns); the modal switches to a "discard branch" affordance in that
 * case.
 */
export interface BranchReintegrationProposal {
  proposedSummary: string;
  tokenCount: number;
  model: string;
  warningEdited?: boolean;
}

/**
 * BranchReintegrationCommitOpts — options for CommitReintegration.
 */
export interface BranchReintegrationCommitOpts {
  branchSessionId: string;
  parentSessionId: string;
  summary: string;
  wasEdited: boolean;
}

/**
 * BranchModelPreference — the user's stated preference at fork time.
 * Maps to the dropdown in the CreateBranch modal.
 */
export type BranchModelPreference =
  | 'smaller'
  | 'larger'
  | 'same'
  | 'exact'
  | '';

/**
 * BranchCreateOptions — request body for Branches_Create.
 */
export interface BranchCreateOptions {
  parentSessionId: string;
  title?: string;
  taskHint?: string;
  modelPreference?: BranchModelPreference;
  exactProviderId?: string;
  exactModelId?: string;
  systemPromptOverride?: string;
  childName?: string;
}

/**
 * BranchStatusInfo — wire shape for Branches_GetStatus.
 */
export interface BranchStatusInfo {
  branch: Branch;
  childSessionId: string;
  hasInflightRun: boolean;
  lastActivityAt?: string;
  lastAssistantMessage?: string;
}

/**
 * BranchRecommendedModel — wire shape for Branches_RecommendModel.
 * Carries a stable reason string the frontend can localize.
 */
export interface BranchRecommendedModel {
  providerId: string;
  modelId: string;
  tier: 'small' | 'medium' | 'large' | string;
  reason: string;
  notes?: string;
  /**
   * Non-empty when the recommended provider differs from the parent's
   * (spec FR-039). The frontend renders this as a yellow callout in
   * the CreateBranchModal.
   */
  crossProviderWarning?: string;
}

// ── WP08 — Universal Permission system types ─────────────────────────────

/**
 * PermissionFamily — the four resource families that the permission
 * system covers. Matches the Cedar entity type names.
 */
export type PermissionFamily = 'bash' | 'fs' | 'credential' | 'tool';

/**
 * PermissionDecision — the three possible responses to a permission prompt.
 * Mirrors the three buttons in BasePermissionModal.
 */
export type PermissionDecision = 'allow_once' | 'allow_always' | 'deny';

/**
 * PermissionGrant — a persisted "Allow always" Cedar policy snippet,
 * as returned by client.permissions.listGrants().
 */
export interface PermissionGrant {
  /** Unique stable id (the Cedar policy filename, or transient resource key). */
  id: string;
  /** Resource family. */
  family: PermissionFamily;
  /** Human-readable resource key (e.g. "git status", "/home/user/foo.txt"). */
  resourceKey: string;
  /** ISO-8601 timestamp when the grant was created. */
  createdAt?: string;
  /** True when this is an in-memory Allow-once grant (no .cedar file). */
  transient?: boolean;
  /** When persisted, the .cedar filename under <DataDir>/policy/. */
  policyFile?: string;
}

/**
 * PermissionRequest — payload emitted on the four broker topics
 * (`bash:permission-pending`, `fs:permission-pending`,
 *  `cred:permission-pending`, `tool:permission-pending`).
 */
export interface PermissionRequest {
  request_id: string;
  session_id: string;
  family: PermissionFamily;
  /** Human-friendly label for the resource (argv, path, provider::purpose, tool). */
  resource_display: string;
  /** Optional: full canonical resource UID for Cedar. */
  resource_uid?: string;
  /** Optional: model's stated reason for the request. */
  reason?: string;
  /** True when the resource is in the dangerous tier for its family. */
  dangerous_tier?: boolean;
  /** Dangerous-tier one-line explanation copy (e.g. "Deletes files irreversibly"). */
  danger_copy?: string;
  // Filesystem-specific
  op?: 'read' | 'write' | 'delete' | 'move' | 'recipe_dir_add';
}

/**
 * CedarProposalPayload — payload emitted on the `cedar:propose-pending`
 * broker topic when an agent proposes a new Cedar policy snippet.
 * Mirrors core/mcp/builtin/harness.CedarProposalPayload (WP07).
 */
export interface CedarProposalPayload {
  request_id: string;
  name: string;
  body: string;
  rationale?: string;
  issued_at: string;
  deadline_at: string;
}

/**
 * PermissionMode — the three global permission posture values.
 */
export type PermissionMode = 'strict' | 'normal' | 'permissive';
// ── Branch Advisor (branch-as-subagent-recommendation WP02/WP03/WP06/WP07) ─

/**
 * BranchSuggestion — payload the backend attaches to a user-message echo
 * when the heuristic detector fires (C-004 / DIRECTIVE_001: rides on the
 * existing chat broker channel, no new RPC layer).
 */
export interface BranchSuggestion {
  /** Opaque ULID that correlates suggestion → accept/dismiss audit events. */
  id: string;
  /** Normalized score [0, 1]. */
  confidence: number;
  /** Human-readable explanation for the banner tooltip. */
  rationale: string;
  /** Stable signal-label list (e.g. ["can_you_also", "while_youre_at_it"]). */
  signals: string[];
  /** First ≤40 chars of the message trimmed at whitespace. */
  proposedTitle: string;
}

/**
 * BranchAdvisorDismissScope — how broadly the dismiss applies.
 */
export type BranchAdvisorDismissScope = 'message' | 'session';

/**
 * ContextItemKind — the kinds of context items the context-pick modal
 * can include when spawning a subagent branch (FR-006).
 */
export type ContextItemKind =
  | 'last_4_turns'
  | 'pinned_memories'
  | 'attached_artifacts'
  | 'system_prompt';

/**
 * SubagentToolGrantMode — the tool-grant scope for a spawned subagent
 * branch (FR-005). "inherit" copies the parent's grant; "readonly" limits
 * to read-only tools; "none" disables tools entirely; "cedar" requires an
 * explicit Cedar policy id (only rendered when cedar-policy-editor is
 * shipped).
 */
export type SubagentToolGrantMode =
  | 'inherit'
  | 'readonly'
  | 'none'
  | 'cedar';

/**
 * SubagentCreateOptions — request body for the context-pick modal's
 * Submit action. Extends BranchCreateOptions with the subagent-specific
 * advisor metadata.
 */
export interface SubagentCreateOptions extends BranchCreateOptions {
  /** ID from the BranchSuggestion that triggered this creation. */
  recommendationId?: string;
  /** Signal labels that fired for this suggestion. */
  advisorSignals?: string[];
  /** Confidence score from the detector. */
  advisorConfidence?: number;
  /** Which context items to seed the subagent session with. */
  contextItems?: ContextItemKind[];
  /** Tool-grant scope for the new branch. */
  toolGrantMode?: SubagentToolGrantMode;
  /** Cedar policy id when toolGrantMode === 'cedar'. */
  cedarPolicyId?: string;
}

/**
 * ReintegrationProposal — what ProposeReintegrationSummary returns.
 */
export interface ReintegrationProposal {
  /** The model-generated summary text, pre-filled in the editable textarea. */
  proposedSummary: string;
  /** Approximate output token count for the summary. */
  tokenCount: number;
  /** Model used for summarization (e.g. "claude-haiku-4"). */
  model: string;
  /** Artifact IDs produced in the branch session. */
  artifactRefs?: string[];
}

/**
 * ReintegrationCommitOptions — body for CommitReintegration.
 * Alias for BranchReintegrationCommitOpts so the BranchesClient
 * interface and the WP06 modal share one shape.
 */
export type ReintegrationCommitOptions = BranchReintegrationCommitOpts;

// ── autonomy-dial-01KR3M2A WP03 wire types ────────────────────────────

/**
 * AutonomyTier is the user-facing 5-stop preset enum mirroring
 * `core/autonomy.Tier`. Sent as a lowercase string on the wire.
 */
export type AutonomyTier =
  | 'strict'
  | 'cautious'
  | 'default'
  | 'bold'
  | 'autonomous';

/** Canonical knob names. */
export type AutonomyKnob =
  | 'maxIterations'
  | 'askOnAmbiguity'
  | 'autoApproveFamilies'
  | 'tokenCeilingPerTurn'
  | 'recapStyle'
  | 'continueOnError'
  | 'destructiveActionPosture';

/**
 * AutonomyLayer is the wire shape of one rung in the global → project
 * → session override hierarchy. `level: null` means "this layer
 * contributes no tier preset"; `overrides` is always present.
 *
 * Mirrors core/autonomy.Layer.MarshalJSON exactly.
 */
export interface AutonomyLayer {
  level: AutonomyTier | null;
  overrides: Partial<Record<AutonomyKnob, unknown>>;
  /** Active named posture mode (e.g. "plan_mode"), or absent when none. */
  postureMode?: string | null;
}

/** Default empty Layer used as the "inherit from upstream" placeholder. */
export function emptyAutonomyLayer(): AutonomyLayer {
  return { level: null, overrides: {} };
}

/** True when the Layer contributes nothing to resolution. */
export function isAutonomyLayerEmpty(
  l: AutonomyLayer | null | undefined,
): boolean {
  if (!l) return true;
  if (l.level !== null) return false;
  return !l.overrides || Object.keys(l.overrides).length === 0;
}

/**
 * AutonomyKnobValues is the resolved per-knob payload returned by
 * Sessions_ResolveAutonomy.
 */
export interface AutonomyKnobValues {
  maxIterations: number;
  askOnAmbiguity: string;
  autoApproveFamilies: string[];
  tokenCeilingPerTurn: number;
  recapStyle: string;
  continueOnError: string;
  destructiveActionPosture: string;
  sourceTrace: Record<string, string>;
  /** Effective tier label for chat-header chip display. */
  tier: AutonomyTier | string;
}

/** Full ResolveAutonomy RPC payload. */
export interface ResolvedAutonomy {
  resolved: AutonomyKnobValues;
  global: AutonomyLayer;
  project: AutonomyLayer;
  session: AutonomyLayer;
}

/** User-facing tier descriptions used by panel components. */
export const AUTONOMY_TIER_LABELS: Record<AutonomyTier, string> = {
  strict: 'Strict',
  cautious: 'Cautious',
  default: 'Default',
  bold: 'Bold',
  autonomous: 'Autonomous',
};

/** One-line tier description copy. */
export const AUTONOMY_TIER_DESCRIPTIONS: Record<AutonomyTier, string> = {
  strict:
    'Asks on every ambiguity. Tightest iteration cap. No tool family auto-approves.',
  cautious:
    'Small iteration budget. Asks on hard calls. Only read ops auto-approve.',
  default:
    'Balanced defaults. Read+write auto-approve. Retry once on tool error.',
  bold:
    'Generous iteration budget. Proceeds on minor ambiguity. Shell-safe ops auto-approve.',
  autonomous:
    'Unbounded iterations. Never asks. All canonical tool families auto-approve. Cedar deny remains the floor.',
};

// ── Migration drift doctor (v0.5.1 migration-doctor) ───────────────────

/**
 * DriftEntry — one discrepancy between the harness_migrations ledger and
 * the registered migration set. Mirrors core/rpc/views/storage.DriftEntry.
 */
export interface DriftEntry {
  /** Migration version number. */
  version: number;
  /** ID currently stored in the ledger for this version. Empty for code_only. */
  ledgerId: string;
  /** ID the registered migration declares. Empty for ledger_only. */
  expectedId: string;
  /**
   * "id_mismatch" — both ledger and registry present; IDs differ (automatable fix).
   * "ledger_only" — applied ledger row with no registered migration.
   * "code_only"   — registered migration not yet applied (normal pending state).
   */
  kind: 'id_mismatch' | 'ledger_only' | 'code_only';
  /** "error" | "warning" | "info" */
  severity: 'error' | 'warning' | 'info';
  /** Human-readable recommended action. */
  suggestion: string;
}

/**
 * DriftReport — result of GetMigrationDriftReport.
 * Mirrors core/rpc/views/storage.DriftReport.
 */
export interface DriftReport {
  /** All detected discrepancies, sorted by version ascending. */
  drifts: DriftEntry[];
}

/** Stable knob ordering for advanced override panels. */
export const AUTONOMY_KNOB_ORDER: readonly AutonomyKnob[] = [
  'maxIterations',
  'askOnAmbiguity',
  'autoApproveFamilies',
  'tokenCeilingPerTurn',
  'recapStyle',
  'continueOnError',
  'destructiveActionPosture',
];

/** Display label for a knob. */
export const AUTONOMY_KNOB_LABELS: Record<AutonomyKnob, string> = {
  maxIterations: 'Max iterations',
  askOnAmbiguity: 'Ask on ambiguity',
  autoApproveFamilies: 'Auto-approve families',
  tokenCeilingPerTurn: 'Token ceiling / turn',
  recapStyle: 'Recap style',
  continueOnError: 'On tool error',
  destructiveActionPosture: 'Destructive actions',
};

// ── Elicitation (ask-user-question-interactive-01KZNP3G WP02/WP04) ────

/**
 * QuestionKind is the closed enum of supported question input types.
 * Mirrors askuserquestion.QuestionKind on the Go side.
 */
export type QuestionKind =
  | 'radio'
  | 'checkbox'
  | 'text'
  | 'number'
  | 'slider'
  | 'date'
  | 'file';

/**
 * PreviewKind is the closed enum of preview renderer types.
 * Mirrors the spec's 8 preview content types.
 */
export type PreviewKind =
  | 'markdown'
  | 'code'
  | 'image'
  | 'diff'
  | 'table'
  | 'html'
  | 'vue-component'
  | 'plain';

/** QuestionOption is one selectable item for radio / checkbox kinds. */
export interface QuestionOption {
  value: string;
  label: string;
}

/** ElicitPreview is the optional side-by-side preview spec. */
export interface ElicitPreview {
  kind: PreviewKind;
  content: string;
  language?: string;
}

/**
 * ElicitRequest is the payload emitted on the "elicit:pending" broker
 * topic. Mirrors elicitview.ElicitRequest on the Go side.
 */
export interface ElicitRequest {
  request_id: string;
  question: string;
  kind: QuestionKind;
  options?: QuestionOption[];
  placeholder?: string;
  min?: number;
  max?: number;
  step?: number;
  default_value?: unknown;
  preview?: ElicitPreview;
  /** WP05: multi-question wizard batch. When present, the single-question fields above are ignored. */
  questions?: WizardQuestion[];
  /** WP06: "blocking" (default) or "deferred". */
  mode?: 'blocking' | 'deferred';
}

/**
 * DeferredResult is the immediate value returned to the model when the
 * ask-user-question tool is called in deferred mode (WP06). Mirrors
 * elicitview.DeferredResult on the Go side.
 */
export interface DeferredResult {
  deferred: boolean;
  ask_id: string;
}

/**
 * WizardQuestion is one question in a multi-step wizard batch (WP05).
 * Mirrors elicitview.WizardQuestion on the Go side.
 */
export interface WizardQuestion {
  id: string;
  question: string;
  kind: QuestionKind;
  options?: QuestionOption[];
  placeholder?: string;
  min?: number;
  max?: number;
  step?: number;
  depends_on?: WizardDependsOn;
}

/**
 * WizardDependsOn makes a question conditional on a prior answer (WP05).
 */
export interface WizardDependsOn {
  question_id: string;
  /** "answered" | {"equals": value} | {"includes": value} */
  condition: unknown;
}

/**
 * WizardAnswer is the result shape when a multi-question wizard completes (WP05).
 * Mirrors elicitview.WizardAnswer on the Go side.
 */
export interface WizardAnswer {
  answers: Record<string, unknown>;
  answered_so_far?: Record<string, unknown>;
  dismissed: boolean;
}

// ── feature flags (user-slash-commands-01KQ8TD9 WP09) ────────────────

/**
 * FeatureFlagInfo — one entry returned by Config_GetFlags.
 * Flags are read-only at runtime; they are controlled via environment
 * variables listed in the envVar field.
 */
export interface FeatureFlagInfo {
  name: string;
  enabled: boolean;
  description: string;
  /** Environment variable that controls this flag (e.g. HARNESS_USER_SLASHCMD). */
  envVar: string;
}

// ── Cedar policy types (cedar-policy-editor-ui-01KQ8TD6 WP02) ─────────────

/**
 * PolicyFile is the light-weight per-file parse-status record returned by
 * ListPolicies. Source is not included (use GetPolicy to read source).
 */
export interface PolicyFile {
  name: string;
  path?: string;
  bytes?: number;
  embedded?: boolean;
  parse_ok: boolean;
  parse_err?: string;
}

/**
 * PolicyDecision mirrors cedar.Decision through the RPC boundary.
 * Used by the audit panel in the policy view.
 */
export interface PolicyDecision {
  outcome: 'allow' | 'deny' | 'not_applicable' | 'unknown';
  action: string;
  principal: string;
  resource: string;
  matched_policy?: string;
  reason?: string;
  evaluated_at: string;
}

// ── Cedar policy editor types (cedar-policy-editor-ui-01KQ8TD6 WP02) ─────

/**
 * ParseError carries a single Cedar parse diagnostic with line and column.
 * Line and Column are 1-based; zero means "not available".
 */
export interface ParseError {
  line: number;
  column: number;
  message: string;
}

/**
 * ParseResult is the outcome of a SavePolicy or ValidatePolicy call.
 * ok is true when the source parsed cleanly; errors is non-empty only when ok is false.
 */
export interface ParseResult {
  ok: boolean;
  errors?: ParseError[];
}

/**
 * PolicyFileDetail extends PolicyFile with the raw source text.
 * Returned by GetPolicy; ListPolicies never includes source.
 */
export interface PolicyFileDetail {
  /** Policy filename (e.g. "my-policy.cedar"). */
  name: string;
  /** Absolute path on disk; empty for embedded defaults. */
  path?: string;
  /** File size in bytes. */
  bytes: number;
  /** true for embedded defaults bundled with the harness binary. */
  embedded?: boolean;
  /** true when the Cedar source parsed without errors. */
  parse_ok: boolean;
  /** Parse error message; empty when parse_ok is true. */
  parse_err?: string;
  /** The raw Cedar source text. */
  source: string;
  /** true for embedded defaults that cannot be edited or deleted via the UI. */
  read_only: boolean;
}

// ── Background task types (background-task-monitor-01KZNP3C WP05) ──────────

/** Status values for a background task. Mirrors core/tasks status constants. */
export type TaskStatus =
  | 'pending'
  | 'running'
  | 'completed'
  | 'failed'
  | 'cancelled'
  | 'crashed';

/** Kind values for a background task. */
export type TaskKind = 'bash' | 'subagent' | 'wails';

/**
 * TaskRow is the wire-safe representation of one background task.
 * Mirrors core/rpc/views/tasks.TaskRow.
 */
export interface TaskRow {
  id: string;
  kind: TaskKind | string;
  ownerSessionId: string;
  cmd: string;
  description: string;
  status: TaskStatus;
  exitCode: number;
  startedAt: string;   // ISO 8601
  endedAt?: string;    // ISO 8601; absent for running tasks
  ageMs: number;
}

/**
 * LineRow is one output line returned by Tasks_Tail.
 * Mirrors core/rpc/views/tasks.LineRow.
 */
export interface LineRow {
  stream: 'stdout' | 'stderr';
  text: string;
  offset: number;
  at: string; // ISO 8601
}

// ── branch-subagent-interactive-01KZNP3B WP01 wire types ──────────────────

/**
 * SubagentMergePolicy controls how a completed sub-agent worker delivers
 * its output back to the parent session.
 *   auto    — merge summary on completion (no user confirmation).
 *   confirm — present a merge card to the user before injecting summary.
 *   manual  — parent must call __subagent_merge(branch_id) explicitly.
 */
export type SubagentMergePolicy = 'auto' | 'confirm' | 'manual';

/**
 * AgentProfileSummary — lightweight entry in the Settings → Agents list.
 * Mirrors core/rpc/views/agents.ProfileSummaryWire.
 */
export interface AgentProfileSummary {
  id: string;
  name: string;
  description: string;
  model?: string;
  mergePolicy: SubagentMergePolicy;
  /** True for profiles shipped with the binary (read-only). */
  bundled: boolean;
}

/**
 * AgentProfile — full profile wire shape for the profile editor.
 * Mirrors core/rpc/views/agents.ProfileWire.
 */
export interface AgentProfile {
  id: string;
  name: string;
  description: string;
  whenToUse?: string;
  model?: string;
  autonomyTier: AutonomyTier;
  allowedTools?: string[];
  deniedTools?: string[];
  budgetTokens?: number;
  budgetTimeS?: number;
  systemPromptOverride?: string;
  mergePolicy: SubagentMergePolicy;
  bundled: boolean;
}

/**
 * SubagentStatus tracks a running sub-agent branch's lifecycle.
 */
export type SubagentStatus =
  | 'running'
  | 'awaiting-merge'
  | 'paused'
  | 'complete'
  | 'error'
  | 'aborted';

/**
 * SubagentBranch extends Branch with sub-agent-specific metadata.
 * Populated on branches that were spawned by __subagent_dispatch.
 */
export interface SubagentBranch extends Branch {
  /** Always true for sub-agent branches. */
  isSubagent: true;
  /** The profile id that drove the spawn. */
  profileId: string;
  /** Current lifecycle status. */
  subagentStatus: SubagentStatus;
  /** Token count consumed so far. */
  tokensUsed?: number;
  /** Budget token limit from the profile. */
  budgetTokens?: number;
  /** Elapsed seconds since spawn. */
  elapsedS?: number;
  /** Budget time limit in seconds from the profile. */
  budgetTimeS?: number;
}

// ── Local runtime types (local-model-runtimes-01KQ8VMZ WP04/WP05/WP06) ──

/**
 * LocalRuntimeModel — a model offered by a locally-running LLM runtime.
 * Mirrors core/rpc/views/llm.LocalRuntimeModel.
 */
export interface LocalRuntimeModel {
  id: string;
  displayName: string;
  /** File size in bytes (0 when unknown). */
  sizeBytes?: number;
  /** Quantization label e.g. "Q4_K_M", "F16". Empty when unknown. */
  quantLevel?: string;
  /** Parameter count in billions (0 when unknown). */
  paramCount?: number;
}

/**
 * LocalRuntimeInfo — detection snapshot for one supported local runtime.
 * Mirrors core/rpc/views/llm.LocalRuntimeInfo.
 */
export interface LocalRuntimeInfo {
  kind: string;
  name: string;
  running: boolean;
  installed: boolean;
  defaultBaseURL: string;
  port: number;
  /** Models loaded in the runtime (populated after metadata fetch). */
  models?: LocalRuntimeModel[];
}

/**
 * LocalRuntimeConfigResult — result of AutoConfigureLocalRuntime.
 * Mirrors core/rpc/views/llm.LocalRuntimeConfigResult.
 */
export interface LocalRuntimeConfigResult {
  providerId: string;
  name: string;
  models: LocalRuntimeModel[];
}

// ── model-fallback-routing-01NDFSEX04 ────────────────────────────────────

/**
 * TriggerCondition enumerates the error classes that cause the runner
 * to advance to the next entry in a fallback chain.
 * Mirrors core/llm/fallback.TriggerCondition.
 */
export type TriggerCondition =
  | 'error_5xx'
  | 'error_429'
  | 'error_auth_failed'
  | 'error_context_overflow'
  | 'error_safety_block'
  | 'error_any';

/**
 * FallbackChainEntry is one hop in a fallback chain.
 * Mirrors core/rpc/views/llm.FallbackChainEntryView.
 */
export interface FallbackChainEntry {
  providerID: string;
  model?: string;
  triggers: TriggerCondition[];
  maxAttempts: number;
  paramOverrides: Record<string, unknown>;
}

/**
 * FallbackChain is the full chain definition returned by LoadChain.
 * Mirrors core/rpc/views/llm.FallbackChainView.
 */
export interface FallbackChain {
  id: string;
  name: string;
  description?: string;
  entries: FallbackChainEntry[];
  /** True when this chain comes from the embedded bundle, not user storage. */
  bundled?: boolean;
}

/**
 * FallbackChainSummary is the lightweight list entry returned by ListFallbackChains.
 * Mirrors core/rpc/views/llm.FallbackChainSummary.
 */
export interface FallbackChainSummary {
  id: string;
  name: string;
  description?: string;
  entryCount: number;
  /** True when this chain comes from the embedded bundle, not user storage. */
  bundled: boolean;
}

/**
 * FallbackAttemptedPayload is the broker event payload emitted on
 * 'llm:fallback-attempted' when the runner hops to a fallback provider.
 * Mirrors the FallbackAttemptedEvent struct in core/llm/fallback/runner.go.
 */
export interface FallbackAttemptedPayload {
  session_id: string;
  chain_id: string;
  from_profile: string;
  from_model: string;
  to_profile: string;
  to_model: string;
  reason: string;
  attempt: number;
  trigger: string;
}

/**
 * FleetIdentity — the harness user's fleet identity.
 * Mirrors core/rpc/views/settings.FleetIdentity.
 * Tier, email, and displayName may be empty on early fleet versions.
 */
export interface FleetIdentity {
  userId: string;
  orgId: string;
  teamId: string;
  email?: string;
  displayName?: string;
  tier?: string;
  orgName?: string;
  teamName?: string;
  roles?: string[];
}

/**
 * FleetProfileInfo — safe projection of the active env profile for UI
 * rendering. Does NOT expose ClientID, APIAudience, or any secret fields.
 * Mirrors core/rpc/views/settings.FleetProfileInfo.
 */
export interface FleetProfileInfo {
  name: string;
  /** "yellow" for dev, "blue" for stage, "red" for local, "" for prod. */
  badgeColor: string;
  fleetBaseUrl: string;
  configured: boolean;
}

/**
 * CapabilitiesView — wire projection of the fleet capability snapshot.
 * Mirrors core/rpc/views/settings.CapabilitiesView.
 * (fleet-capability-surface-01NDFSEX09 WP11)
 */
export interface CapabilitiesView {
  tier: string;
  /** Map of snake_case capability key → enabled boolean. */
  enabled: Record<string, boolean>;
  /** RFC3339 timestamp of the last fetch, empty when never fetched. */
  fetchedAt: string;
  /** "fleet" | "cache" | "default-deny" */
  source: string;
}

/**
 * FleetConfigPullStatusView — wire projection of the config-pull poller state.
 * Mirrors core/rpc/views/settings.FleetConfigPullStatusView.
 * (fleet-config-pull-01NDFSEX10 WP02)
 */
export interface FleetConfigPullStatusView {
  /** bundle_id of the last successfully applied bundle, or 0. */
  lastAppliedId: number;
  /** RFC3339 timestamp of the last apply, or empty string. */
  lastAppliedAt: string;
  /** Most recent error string, or empty when healthy. */
  lastError: string;
  /** "fleet" | "cache" | "default-deny" */
  source: string;
  /** Hex SHA-256 of the last-seen bundle body (for 304 Not Modified gating). */
  bundleChecksum: string;
}

/**
 * LockdownStatusView — current emergency lockdown state.
 * Mirrors core/rpc/views/settings.LockdownStatusView.
 * (fleet-emergency-lockdown-01NDFSEX12 WP02)
 */
export interface LockdownStatusView {
  /** true when a fleet-issued emergency lockdown is in effect. */
  active: boolean;
  /** Admin-supplied reason text; empty when not active or no reason given. */
  reason: string;
}
