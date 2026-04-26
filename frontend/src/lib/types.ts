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
  toolCalls?: readonly ToolCall[];
  /**
   * Polymorphic content blocks for multimodal messages
   * (multimodal-io WP02/WP04). Empty / undefined for legacy text-only
   * messages — MessageBubble falls back to the `content` field in that
   * case. The wire shape mirrors core/llm.ContentBlock; field names use
   * snake_case to match the Wails serializer's verbatim JSON-tag pass.
   */
  contentBlocks?: readonly ContentBlock[];
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
