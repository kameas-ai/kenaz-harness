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
 * MemoryChunk — one persisted memory. In the hooks-driven architecture
 * most chunks are auto-persisted by the memory.persist post_send hook;
 * the chat surface still ships an explicit "remember this" button for
 * short turns the auto-path skips. The vector representation never
 * crosses the RPC boundary; the management UI only ever sees the
 * human-readable fields.
 */
export interface MemoryChunk {
  id: string;
  sessionId?: string;
  sourceTurn?: string;
  content: string;
  createdAt: string;
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
