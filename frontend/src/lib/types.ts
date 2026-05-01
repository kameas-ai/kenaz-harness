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
  | 'ollama';

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
}

export interface MCPServer {
  id: string;
  name: string;
  state: string;
  version: string;
  transport?: string;
  capabilities?: string[];
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
}

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
 * MediaSource — base64 / URI source of an image or document content
 * block. Mirrors core/llm.MediaSource. The Wails JSON wire shape keeps
 * the Go-side snake_case JSON tags verbatim.
 */
export interface MediaSource {
  kind: string;
  media_type: string;
  data?: string;
  uri?: string;
  original_name?: string;
}

/**
 * ContentBlock — one polymorphic fragment of a multimodal message.
 * Mirrors core/llm.ContentBlock. The wire shape uses the Go-side
 * snake_case JSON tags (`tool_use`, `tool_result`).
 */
export interface ContentBlock {
  type: 'text' | 'image' | 'document' | 'tool_use' | 'tool_result';
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
 * MemoryPrunePreview — dry-run prune result.
 */
export interface MemoryPrunePreview {
  verdicts: MemoryPruneVerdict[];
  stats: MemoryPruneStats;
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
export type ConfigKind = 'directory_list' | 'boolean' | 'string';

/**
 * ConfigOption — one user-editable knob declared by a recipe. The
 * filesystem recipe declares `allowed_directories` (directory_list);
 * future recipes may declare booleans (e.g. read_only) or free-form
 * strings. `default` may carry the `${DATA_DIR}` substitution token
 * for directory_list defaults — the backend expands the token at
 * install time, the modal renders the literal as a placeholder.
 */
export interface ConfigOption {
  name: string;
  display: string;
  kind: ConfigKind;
  default?: unknown;
  required: boolean;
  description: string;
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
 * Sessions_SaveAsArtifact / right-click "Save as artifact".
 */
export type ArtifactSource = 'code_block' | 'tool_output' | 'user_pin';

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
 * "(coming soon)" tag.
 */
export interface SlashCommandInfo {
  name: string;
  description: string;
  comingSoon: boolean;
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
