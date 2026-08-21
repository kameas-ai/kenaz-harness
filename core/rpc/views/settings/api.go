// Package settings defines the SettingsAPI view-scoped accessor and
// the SettingsStore implementation interface backing it. Persistence
// is a single JSON file per privacy CI invariant #5 (plan §4.3).
package settings

import (
	"context"
	"encoding/json"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/autonomy"
	"github.com/kameas-ai/kenaz-harness/core/compactionpolicy"
)

// Settings is the persisted UI state shape (plan §5.5). lastRoute drives
// first-paint route restoration.
//
// SchemaVersion does NOT gate any migration today
// (controls-and-readouts-that-tell-the-truth-01PMZ808 WP06, FR-007,
// narrowed 2026-08-19). Three production sites default-backfill it
// (impl.go:84-85, :308-309, :1580-1581 — `if got.SchemaVersion == 0 { …
// = 1 }`); nothing compares it against any other value, and there is no
// migration table or dispatcher. `justify(blocker: "no
// settings-migration dispatcher; needs the settings.json upgrade
// fixture WP-PI builds", owner: alec, date: 2026-08-19)`. The field and
// its About-panel display stay: a number that is always 1 is a fact,
// not a lie.
//
// MemoryEnabled is the explicit opt-in for the cross-session long-term
// memory feature. Privacy default is OFF: when false the harness never
// embeds, queries, or injects memory chunks regardless of what is on disk.
type Settings struct {
	SchemaVersion int        `json:"schemaVersion"`
	LastRoute     string     `json:"lastRoute"`
	Theme         string     `json:"theme"`
	Accent        string     `json:"accent"`
	WindowSize    WindowSize `json:"windowSize"`
	MemoryEnabled bool       `json:"memoryEnabled"`
	// ConfirmEachDisabled is the inverted persisted form of the
	// WP05 confirm-each tool-call modal flag. We store the disabled
	// bit (default false → modal ENABLED) so the zero value of a
	// freshly-installed settings file matches the spec's "default
	// ON" requirement without an extra Configured marker.
	//
	// Frontend / toolloop callers should never read this directly —
	// use ConfirmEachEnabled() helper or the Settings_GetConfirmEach
	// binding which inverts on the boundary.
	ConfirmEachDisabled bool `json:"confirmEachDisabled"`

	// Artifacts capture settings (artifacts-storage FR-017). All four
	// fields use a zero-value-friendly persisted form: AutoCapture*
	// stores the inverted "Disabled" bit so a fresh install (zero
	// values across the board) matches the spec's defaults
	// (capture ON, 10 lines, 200 bytes). The MinLines / MinBytes
	// zero-values fall back to the spec defaults via the helper
	// methods on the loaded record.
	AutoCaptureCodeBlocksDisabled  bool `json:"autoCaptureCodeBlocksDisabled,omitempty"`
	CodeBlockMinLines              int  `json:"codeBlockMinLines,omitempty"`
	CodeBlockMinBytes              int  `json:"codeBlockMinBytes,omitempty"`
	AutoCaptureToolOutputsDisabled bool `json:"autoCaptureToolOutputsDisabled,omitempty"`

	// WebSearchEnabled controls the local-first web search built-in
	// (core/tools/websearch). Default OFF on a fresh install — the user
	// opts in from the Tools panel. Local-only by design: DuckDuckGo
	// HTML scrape + Wikipedia + go-readability extraction. No API key.
	WebSearchEnabled bool `json:"webSearchEnabled,omitempty"`

	// BashEnabled controls the local-first bash built-in
	// (core/tools/bash). Default OFF. Sandboxed to a configurable
	// command allowlist (BashAllowlistEditor component) regardless of
	// provider. The allowlist itself ships with a conservative default
	// (read-only commands; deny by pattern).
	BashEnabled bool `json:"bashEnabled,omitempty"`

	// WebFetchEnabled controls the kenaz__web_fetch built-in. Default OFF
	// (zero-value → disabled). The tool makes authenticated HTTP requests
	// on behalf of the model, resolving @secret: references at request time.
	// Gated by Cedar network policy (host allowlist) in addition to this toggle.
	// (crash-recovery-tool-gating-0XQTC4RK FR-005)
	WebFetchEnabled bool `json:"webFetchEnabled,omitempty"`

	// SaveArtifactDisabled is the inverted-form persisted bit for the
	// kenaz__save_artifact built-in. Default ON (zero-value Disabled
	// → tool enabled) — saving deliverables is a low-risk primitive
	// that should work on first launch without setup. Mirrors the
	// AutoCaptureCodeBlocksDisabled pattern. Read via the
	// SaveArtifactEnabled() accessor; never read directly.
	SaveArtifactDisabled bool `json:"saveArtifactDisabled,omitempty"`

	// MaxAgentTurns caps the number of LLM ↔ tool round-trips inside
	// a single chat-graph LoopNode body before the cap-hit pause-not-kill
	// UX fires (commit c760087). Zero falls back to the spec default
	// (DefaultMaxAgentTurns — see its doc; raised to 500 when migrating
	// off core/toolloop's per-call DefaultMaxIter=8). The chassis reads
	// the effective value via EffectiveMaxAgentTurns on every chat run
	// start so a settings change takes effect on the next user turn
	// without restarting the harness.
	MaxAgentTurns int `json:"maxAgentTurns,omitempty"`

	// ReasoningBudgetTokens is the extended-thinking / reasoning token
	// budget threaded onto the chat graph's model node on every run
	// start (wiring-integrity-01PMAG04 WP08).
	//
	// The attr, the LLMRequest field, and the GenerationRequest.Reasoning
	// translation have all existed and been tested since
	// model-request-path-live-01PMDL01 WP06b — but no shipped graph ever
	// set the attr, so the whole path was inert. This dial is the missing
	// last hop.
	//
	// Zero means "reasoning disabled", which is the provider default and
	// reproduces pre-01PMAG04 behaviour byte-for-byte. It is deliberately
	// NOT defaulted to a non-zero value: enabling reasoning changes the
	// cost and latency of every turn, so it is a product decision made by
	// turning this on, not a side effect of wiring it up.
	ReasoningBudgetTokens int `json:"reasoningBudgetTokens,omitempty"`

	// CompactionAggressiveness controls when and how aggressively the
	// harness summarises older session history when approaching the
	// model's context window. One of: "off", "conservative", "balanced",
	// "aggressive", "maximal". Empty string == "balanced" via the
	// EffectiveCompactionAggressiveness accessor.
	//
	// Plan: compaction-strategy-ui-01KQ8TDI §2.9. The chat runner reads
	// the effective tier on every send; settings changes take effect on
	// the next turn without restarting.
	CompactionAggressiveness string `json:"compactionAggressiveness,omitempty"`

	// CompactionModel is the provider+model used for the summarisation
	// call. Empty == "use the session's active model" (the chained
	// default the chat runner falls back to when this is unset). The
	// wire shape mirrors core/agentgraph/compaction.ProviderProfileRef but is
	// declared locally here so settings doesn't take a dependency on
	// the engine's internal types beyond the policy constants.
	CompactionModel ProviderProfileRef `json:"compactionModel,omitempty"`

	// CompactionArchiveDays is the soft-archive retention window in
	// days. Default 90, range [7, 365]. Save() rejects out-of-range
	// values; the EffectiveCompactionArchiveDays accessor clamps for
	// defensive reads (e.g. against a hand-edited settings file).
	CompactionArchiveDays int `json:"compactionArchiveDays,omitempty"`

	// CompactionRecentWindow is the count of most-recent user-assistant
	// pairs that compaction will never touch. Default 4 (locked plan
	// default §2.6). Negative is rejected at Save; zero rounds up to the
	// default via the EffectiveCompactionRecentWindow accessor.
	CompactionRecentWindow int `json:"compactionRecentWindow,omitempty"`

	// ── WP08 — Universal permission dials ──────────────────────────────

	// PermissionMode controls the global permission posture for all
	// four resource families (bash, filesystem, credential, tool).
	// One of: "strict", "normal" (default, empty==normal), "permissive".
	// "strict"     — every call prompts regardless of saved policies.
	// "normal"     — prompts only for NotApplicable Cedar decisions.
	// "permissive" — all non-dangerous ops permitted without prompt.
	// UI: stern confirm dialog when switching to "permissive".
	PermissionMode string `json:"permissionMode,omitempty"`

	// PermissionCacheDangerousOps controls whether "Allow always" is
	// available for dangerous-tier operations (rm, sudo, system paths,
	// etc). Default false — dangerous ops re-prompt every time.
	// When true, the bash and filesystem modals enable "Allow always"
	// for dangerous-tier resources. Requires confirm dialog on enable.
	PermissionCacheDangerousOps bool `json:"permissionCacheDangerousOps,omitempty"`

	// BashAllowlistMigrated tracks whether the first-boot migration
	// (WP10) has run. When true the UI suppresses the one-time migration
	// toast. Set by the migration runner; do not set manually.
	BashAllowlistMigrated bool `json:"bashAllowlistMigrated,omitempty"`

	// PermissionsMigrationToastShown tracks whether the one-time
	// first-boot migration toast has been displayed to the user.
	// Set to true after the toast is shown so it never shows again.
	PermissionsMigrationToastShown bool `json:"permissionsMigrationToastShown,omitempty"`

	// CedarStrictCredentialMode controls NotApplicable handling in the
	// credstore Cedar gate (mission cedar-credential-policy-01KQ8TDE,
	// WP05). When false (default / lenient), a NotApplicable Cedar
	// outcome is treated as allow. When true (strict), NotApplicable
	// for non-mcp_spawn purposes is treated as deny — the store
	// becomes fail-closed for unmatched credential access patterns.
	//
	// The UI dial for this setting is a follow-up to WP05; set via
	// Settings_GetCedarStrictCredentialMode /
	// Settings_SetCedarStrictCredentialMode bindings today.
	CedarStrictCredentialMode bool `json:"cedarStrictCredentialMode,omitempty"`

	// CedarStrictWorkflowMode selects the `mode` context attribute the
	// Workflow-family Cedar bundle branches on: false (default) sends
	// "permissive", true sends "strict".
	//
	// Strict is what makes `default_workflows_policy.cedar`'s
	// shell-step forbid rule fire on save. That bundle is embedded in
	// every engine, so before this field existed it shipped to every
	// user with an arm nothing could reach.
	//
	// No UI dial yet — the Settings → Workflows panel is a tracked
	// follow-up (docs/unwired-ledger.md, 2026-08-16). Settable today by
	// editing `cedarStrictWorkflowMode` in the harness settings file,
	// and read live on every workflow run/save.
	CedarStrictWorkflowMode bool `json:"cedarStrictWorkflowMode,omitempty"`

	// GraphAuthoringEnabled is the model-authored-agent-graphs consent
	// dial (model-authored-graphs-01PMGA01 UNIT-4, FR-006). Default
	// false: a fresh install — and any install upgraded from a build
	// that predates this field, since JSON decode leaves an absent bool
	// key at Go's zero value — denies graph.author until the user opts
	// in from Settings. Read live (not cached) on every graph.author
	// evaluation, carried into the Cedar context as
	// context.authoring_enabled, so flipping it takes effect on the
	// next draft attempt without an app restart. Deliberately NOT
	// exposed through harness.SettingsAllowlist — see the Store
	// interface doc comment on LoadGraphAuthoringEnabled for why.
	GraphAuthoringEnabled bool `json:"graphAuthoringEnabled,omitempty"`

	// CredentialAuditRetentionDays controls how long
	// KindCredentialAccessed audit rows are retained before the daily
	// sweep deletes them (credential-store-01KQ8TDD WP07). Zero (default)
	// keeps rows forever — matches the harness greedy-retention posture
	// (spec Q26.1). Non-zero values prune rows older than N days on the
	// daily credstore sweep goroutine.
	//
	// UI surface: Settings → Privacy, "Credential audit retention: ___
	// days (blank = forever)". Frontend sends the numeric value; the
	// binding layer converts 0/empty to 0 (forever).
	CredentialAuditRetentionDays int `json:"credentialAuditRetentionDays,omitempty"`
	// ── Branch Advisor dials (branch-as-subagent-recommendation WP08) ──

	// BranchAdvisorEnabled is the master on/off for the branch advisor.
	// When false — the default — the banner never mounts, regardless of
	// confidence score (ChatInput.vue's runAdvisorDetector gates on this
	// before the confidence check; engineer-truth-pass-01PMTP01 WP02).
	//
	// Not FR-010: the owning mission
	// (kitty-specs/_archive/branch-as-subagent-recommendation-01KQ8TDJ)
	// never specified a master switch — its FR-010 is the confidence
	// threshold, i.e. BranchAdvisorMinConfidence below. This field was
	// added to the struct without an owning WP; there is no FR to cite.
	BranchAdvisorEnabled bool `json:"branchAdvisorEnabled,omitempty"`

	// BranchAdvisorMinConfidence is the heuristic-score threshold below
	// which the banner does not mount. Default 0.85 (locked Q29.1).
	// Range [0, 1]; Save rejects values outside this range — enforced by
	// validateBranchFields (impl.go), added in engineer-truth-pass-
	// 01PMTP01 WP03 (this sentence had no enforcement behind it before).
	BranchAdvisorMinConfidence float64 `json:"branchAdvisorMinConfidence,omitempty"`

	// BranchAdvisorUseLLM enables the optional LLM-backed detector
	// (FR-013). Default false — reserved field; no implementation in v1.
	BranchAdvisorUseLLM bool `json:"branchAdvisorUseLLM,omitempty"`

	// BranchAutoMode enables auto-branching when confidence exceeds a
	// higher threshold (FR-014). Default false — power-user feature off
	// by default. Reserved field; no implementation in v1.
	BranchAutoMode bool `json:"branchAutoMode,omitempty"`

	// BranchReintegrationMaxTokens caps the length of the reintegration
	// summary (FR-008a). v1's ProposeReintegrationSummary is rule-based
	// (branches/impl.go: it concatenates and rune-truncates the last
	// ≤8 assistant turns; Model is reported as "rule_based") — no model
	// is instructed anything. Default 2000, min 500, max 16000. Zero
	// falls back to the default via EffectiveBranchReintegrationMaxTokens,
	// which ProposeReintegrationSummary calls (engineer-truth-pass-
	// 01PMTP01 WP02; before WP02 this field had no reader and the cap
	// was a hardcoded 2000 regardless of what was persisted here). Save
	// rejects non-zero values outside [min, max] — validateBranchFields
	// (impl.go), added in WP03.
	BranchReintegrationMaxTokens int `json:"branchReintegrationMaxTokens,omitempty"`

	// BranchAdvisorDefaultModel is the (provider, model) intended for
	// newly spawned subagent branches.
	//
	// NARROWED 2026-08-20 (controls-and-readouts-that-tell-the-truth-
	// 01PMZ808 WP05, FR-005): this field has neither a reader nor a
	// writer anywhere in production, and EffectiveBranchAdvisorDefaultModel
	// below has zero callers. It is not yet consumed — do not read
	// "defaults to CompactionModel" as live behaviour. Spec R-6: the
	// chain's second link, core/rpc/views/branches/impl.go's
	// parentModel, is a stub that discards both its parameters and
	// returns ("", ""), so wiring this field alone would produce a dial
	// that appears to work and silently resolves to nothing — worse
	// than the current honest inertness. See docs/unwired-ledger.md.
	BranchAdvisorDefaultModel ProviderProfileRef `json:"branchAdvisorDefaultModel,omitempty"`
	// KeyboardShortcuts holds user-overridden shortcut bindings keyed by
	// stable shortcut id (e.g. "chat.send" → "Cmd+Shift+Enter"). An
	// absent/empty map means all shortcuts use their registry defaults.
	// Backend validates: ≤200 entries, ≤64 chars/value, no control chars.
	// omitempty: old clients that don't know about this field simply read
	// an empty map and use registry defaults — no migration needed.
	// (keyboard-shortcuts-settings-01KQ8TDR plan §2.7 / C-004)
	KeyboardShortcuts map[string]string `json:"keyboardShortcuts,omitempty"`

	// KeyboardShortcutsPreset is reserved for a future preset-gallery
	// follow-up mission. v1 always persists as empty string; old clients
	// ignore the field. (keyboard-shortcuts-settings-01KQ8TDR plan Q1=C)
	KeyboardShortcutsPreset string `json:"keyboardShortcutsPreset,omitempty"`

	// FSRequestAccessDisabled is the inverted persisted bit for the
	// kenaz__request_filesystem_access built-in. Default ON (zero-value
	// Disabled → tool enabled) so users can expand filesystem access
	// from the first session without any setup. Read via the
	// FSRequestAccessEnabled() accessor; never read directly.
	FSRequestAccessDisabled bool `json:"fsRequestAccessDisabled,omitempty"`

	// SearchDisabled is the inverted persisted bit for the cross-session
	// search modal (cross-session-search-01KQ8TDQ WP07). Default ON
	// (zero-value Disabled → search enabled). When true, the SearchAPI
	// short-circuits with an empty result set regardless of what is in
	// the FTS5 index — privacy escape hatch for users who don't want
	// their message corpus surfaced through Cmd+F. Read via the
	// SearchEnabled() accessor; never read directly.
	SearchDisabled bool `json:"searchDisabled,omitempty"`

	// Autonomy is the persisted global autonomy.Layer
	// (autonomy-dial-01KR3M2A WP02). Empty value (no level + no
	// overrides) means "use the tier-default fallback." The wire shape
	// is the canonical autonomy.Layer JSON envelope ({"level":...,
	// "overrides":{...}}); a missing field round-trips as the empty
	// Layer. The settings store is JSON-file backed (no SQL table for
	// settings), so the global autonomy layer rides on top of the
	// existing settings.json round-trip rather than its own migration.
	Autonomy json.RawMessage `json:"autonomy,omitempty"`

	// ── Auto-update dials (auto-update-v0.4.0 WP05) ─────────────────────
	//
	// AutoCheckUpdatesDisabled is the inverted-form persisted bit for the
	// "Automatically check for updates" toggle. Default ON (zero-value
	// Disabled → checker enabled) — fresh installs poll the release feed
	// at the configured interval without any user setup. Read via the
	// AutoCheckUpdates() accessor; never read directly.
	AutoCheckUpdatesDisabled bool `json:"autoCheckUpdatesDisabled,omitempty"`

	// UpdateChannel selects which release feed the checker subscribes to.
	// One of: "stable" (default, empty == stable), "prerelease". Other
	// values are rejected at Save with ErrInvalidUpdateChannel; the
	// EffectiveUpdateChannel accessor falls back to "stable" for defensive
	// reads (e.g. against a hand-edited settings file).
	UpdateChannel string `json:"updateChannel,omitempty"`

	// UpdateCheckIntervalSec is the poll interval in seconds. Spec values:
	// 3600 (1h), 21600 (6h, default), 86400 (24h). Zero == default. Save
	// rejects negatives and out-of-range values via ErrInvalidUpdateCheckInterval.
	UpdateCheckIntervalSec int `json:"updateCheckIntervalSec,omitempty"`

	// SkippedUpdateVersions is the list of release versions the user
	// clicked "Skip this version" on. The updater's "is there a new
	// release?" check filters these out so the user is not re-prompted.
	// The Settings panel's collapsible "Skipped versions" section lets the
	// user un-skip a row; mutated via SaveSkippedUpdateVersions /
	// AppendSkippedUpdateVersion / RemoveSkippedUpdateVersion.
	SkippedUpdateVersions []string `json:"skippedUpdateVersions,omitempty"`

	// MonthlyCostNotifyUSD is the per-month spend threshold at which
	// the harness fires escalating notifications at 50 / 80 / 100 /
	// 150 / 200 % of the dial value (token-cost-telemetry-01KQ8TD7
	// WP06). Zero (the default) disables the scheduler entirely.
	// Range [0, MaxMonthlyCostNotifyUSD]. NARROWED
	// (controls-and-readouts-that-tell-the-truth-01PMZ808 UNIT-14
	// WP19, FR-029, 2026-08-21): Save does NOT reject a negative value
	// — both FileStore and memoryStore clamp it to zero — and only
	// errors above MaxMonthlyCostNotifyUSD. FR-007c: this is a
	// visibility dial — hard caps live in the user's provider
	// dashboard.
	MonthlyCostNotifyUSD float64 `json:"monthlyCostNotifyUsd,omitempty"`

	// MCPAutoRestartDisabled is the inverted persisted bit for the
	// "Auto-restart MCP servers on disconnect" dial
	// (mcp-server-health-ui-01KQ8TD6 WP06). Default ON (zero-value
	// Disabled → auto-restart enabled) so a crashed server recovers
	// without user intervention on a fresh install. Read via the
	// MCPAutoRestart() accessor; never read directly.
	MCPAutoRestartDisabled bool `json:"mcpAutoRestartDisabled,omitempty"`

	// AgenticTurnRouting is the LAUNCH GATE for the routed chat turn
	// (agentgraph-total-convergence-01PMGX01 WP11b; design in
	// agentic-turn-routing-01PMAG01 §3.6).
	//
	// On: chat_default runs with a `router` node in the agent loop and
	// a `review` exit gate between the loop and the history writer, and
	// the kernel arms TaskState on success paths so the gate has a goal
	// to check against. Off (the default, and the zero value):
	// agentgraph.GateAgenticTurnRouting strips both nodes back out and
	// the graph traverses exactly as it did before the rewrite.
	//
	// WHY IT DEFAULTS OFF, when nothing else in this campaign does.
	// chat_default.yaml was rewritten IN PLACE — §3.6's rollout
	// decision — and an in-place rewrite of the graph every chat turn
	// runs has no fallback otherwise. This flag is the
	// revert-without-redeploy lever for that, which is a different
	// thing from shipping a feature switched off: the work is complete
	// and both positions are pinned by golden traces. Flip it after a
	// dev-channel soak.
	//
	// Read at graph-load time, per StartStream (core/rpc/api.go's
	// GraphLoader) — read-at-consumption, not a boot seed, so moving it
	// takes effect on the next turn rather than the next launch.
	AgenticTurnRouting bool `json:"agenticTurnRouting,omitempty"`

	// AutoTitleDisabled is the inverted persisted bit for the
	// session auto-titling feature (p0-wiring-fixes-3TVMG0MX WP05).
	// Default ON (zero-value Disabled → auto-title enabled) so new
	// installs get titles without any setup. Read via the
	// AutoTitleEnabled() accessor; never read directly.
	AutoTitleDisabled bool `json:"autoTitleDisabled,omitempty"`

	// MoveFidelityHistoryDisabled is the inverted persisted bit for
	// model-visible move fidelity (model-moves-transcript-01PMCH01 WP03,
	// spec FR-002 + §4).
	//
	// WHAT IT CHANGES. With it ON (the default), the history the next
	// request is built from carries the model's own reasoning chain in
	// the provider's native shape — assistant tool_use blocks paired with
	// tool_result messages — instead of the flattened one-message-per-turn
	// transcript that discards what the model tried, what the tools
	// returned and why it pivoted. It is a PROVIDER-VISIBLE change: every
	// subsequent request's message array differs.
	//
	// WHY INVERTED. Default ON is the spec's position, and the inverted
	// bit is this repo's idiom for it (see ConfirmEachDisabled,
	// AutoTitleDisabled): the zero value — a fresh install with no
	// settings.json — must mean "on" without writing any state.
	//
	// DEFAULT ON APPLIES TO NEW SESSIONS ONLY. This dial is one of two
	// inputs; the other is sessions.move_history_mode, stamped at session
	// creation. A session that predates the mission keeps the classic
	// composition no matter where this sits, so turning the dial on never
	// changes the shape of a conversation already in flight. Turning it
	// OFF, however, reverts every session at once and immediately: the
	// composition reads this at the point of consumption, so it is a live
	// revert lever rather than a restart-to-revert one.
	//
	// Read via the MoveFidelityHistoryEnabled() accessor; never read the
	// raw bit.
	MoveFidelityHistoryDisabled bool `json:"moveFidelityHistoryDisabled,omitempty"`

	// ── Builtin filesystem tool dials (builtin-filesystem-tools-01KR3N4P) ──

	// FSReadDisabled is the inverted persisted bit for the read-family
	// builtin filesystem tools (kenaz__read_file, kenaz__list_dir,
	// kenaz__glob, kenaz__grep, kenaz__list_open_worklist). Default OFF
	// (zero-value Disabled → tools disabled) — the user opts in from the
	// Tools panel (WP06). Read via the FSReadEnabled() accessor; never
	// read directly.
	FSReadDisabled bool `json:"fsReadDisabled,omitempty"`

	// FSWriteDisabled is the inverted persisted bit for the write-family
	// builtin filesystem tools (kenaz__write_file, kenaz__edit_file).
	// Default OFF (zero-value Disabled → tools disabled) — the user opts
	// in from the Tools panel (WP06). Read via the FSWriteEnabled()
	// accessor; never read directly.
	FSWriteDisabled bool `json:"fsWriteDisabled,omitempty"`

	// TodoDisabled is the inverted persisted bit for the
	// kenaz__todo_write builtin tool
	// (builtin-tools-search-and-elicitation-01KZNP3D WP05/WP07).
	// Default OFF (zero-value Disabled → tool disabled) — the user opts
	// in from the Tools panel. Read via the TodoEnabled() accessor;
	// never read directly. Stored as the Disabled bit so a fresh install
	// starts with the todo tool off (opt-in, same as FSReadDisabled /
	// FSWriteDisabled above).
	TodoDisabled bool `json:"todoDisabled,omitempty"`

	// EditFileArtifactSyncDisabled is the per-user opt-out bit for the
	// edit-file artifact sync feature (edit-file-artifact-sync-01KQ8TD5
	// WP04). Default ON (zero-value Disabled → feature enabled when the
	// env-var gate HARNESS_EDIT_FILE_ARTIFACT_SYNC=on is set). Read via
	// the EditFileArtifactSyncEnabled() accessor; never read directly.
	EditFileArtifactSyncDisabled bool `json:"editFileArtifactSyncDisabled,omitempty"`

	// ContextWindowOverrides is a per-provider-kind map of user-supplied
	// context-window sizes (in tokens). Keys are provider kind strings
	// ("anthropic", "openai", "bedrock", "openrouter"). When a key is
	// present, the frontend context-window meter uses its value as the
	// denominator instead of the backend-curated catalog value
	// (backend-context-window-length-01KQ8TD3 WP05). Zero or negative
	// values are silently ignored — the catalog value takes precedence.
	// An absent map (nil/empty) means "use catalog values for all providers."
	ContextWindowOverrides map[string]int `json:"contextWindowOverrides,omitempty"`

	// ── Branching UX dials (branching-ux-polish-01KQ8TD7 WP06) ──────────

	// AutoCollapseBranchesInSidebar controls the initial collapse state
	// for branch trees in the left rail. When true (default), every
	// parent session that has children starts collapsed so the rail
	// doesn't sprawl on first load. The user can expand individually;
	// their choices are persisted in localStorage under
	// harness.sidebar.branchCollapsed.v1.
	//
	// *bool, not bool (controls-and-readouts-that-tell-the-truth-01PMZ808
	// WP03): the JSON zero-value of bool is false, but the spec default is
	// true, so a plain `bool` + `omitempty` cannot distinguish "explicitly
	// set false" from "never set" — both marshal to an absent key, and a
	// v0.64.0-and-earlier settings.json (which never wrote this key) loads
	// as false rather than the documented true. A pointer only omits on
	// nil, so an explicit false round-trips and an absent key stays
	// distinguishable. See EffectiveAutoCollapseBranchesInSidebar below.
	AutoCollapseBranchesInSidebar *bool `json:"autoCollapseBranchesInSidebar,omitempty"`

	// DeleteBranchesWithParent controls cascade-delete behaviour when a
	// parent session is deleted. When false (default / safe), child
	// sessions become orphans — their branch row is gone (ON DELETE
	// CASCADE) but the child session persists and the sidebar promotes
	// it to a root row. When true, deleting a parent recursively
	// removes all descendant sessions before cascading the branch rows.
	DeleteBranchesWithParent bool `json:"deleteBranchesWithParent,omitempty"`

	// MaxVisibleBranchDepth clamps sidebar indentation to this many
	// levels — deeper branch rows stop indenting further but still
	// render; nothing hides them. Default 5. Zero falls back to the
	// default via EffectiveMaxVisibleBranchDepth.
	//
	// NARROWED 2026-08-20 (controls-and-readouts-that-tell-the-truth-
	// 01PMZ808 WP02): this field previously claimed a depth-overflow
	// expand-on-click affordance that does not exist anywhere in the
	// app — SettingsView.vue, this doc, and frontend/src/lib/types.ts
	// all made the same claim. See docs/unwired-ledger.md for the dated
	// justification and E-002 (the product call on whether to build it).
	MaxVisibleBranchDepth int `json:"maxVisibleBranchDepth,omitempty"`

	// ── Embedder configuration (v0.5.2 universal-embedder fix) ──────────────
	//
	// EmbedderProviderProfileID is the personal-provider profile to use
	// for embedding.  Empty (default) means "auto-pick the first eligible
	// provider" — openai > openrouter > custom_openai_compatible > azure,
	// in store order.  The value must match a profile ID returned by
	// Settings → Models so the embedder picker dropdown can round-trip it.
	EmbedderProviderProfileID string `json:"embedderProviderProfileId,omitempty"`

	// EmbedderModelOverride is the model to pass to the embeddings API.
	// Empty (default) applies the per-Kind default:
	//   - openai / openrouter / custom_openai_compatible → "text-embedding-3-small"
	//   - azure → the profile's own Model field
	// The Settings → Memory text input surfaces this with the per-Kind
	// default as a placeholder so the user knows what they're overriding.
	EmbedderModelOverride string `json:"embedderModelOverride,omitempty"`

	// ShowPerMessageTokenMeter controls the per-message token-cost chip
	// (per-message-token-meter-01KR3PQR). Default false (OFF) — the chip
	// is hidden to keep the chat uncluttered. When true, every completed
	// assistant bubble shows a small "1.2k → 240 = 1.4k tok · $0.012"
	// chip that expands to a breakdown popover on click. Stored as a plain
	// bool (not inverted) because the desired default is OFF, matching the
	// zero-value of bool.
	ShowPerMessageTokenMeter bool `json:"showPerMessageTokenMeter,omitempty"`

	// ── Long-session nudge dials (v0.5.6 memory-trust-signals) ──────────
	//
	// LongSessionNudgeTurns is the number of user-assistant turn pairs
	// (i.e. half of total message count) after which the inline nudge
	// banner appears. Default 30 (60 messages). Zero falls back to the
	// default via EffectiveLongSessionNudgeTurns. Negative values are
	// rejected at Save.
	LongSessionNudgeTurns int `json:"longSessionNudgeTurns,omitempty"`

	// LongSessionNudgeTokens is the cumulative prompt-token threshold
	// after which the nudge banner appears regardless of turn count.
	// Default 50000. Zero falls back to the default via
	// EffectiveLongSessionNudgeTokens. Negative values are rejected at
	// Save.
	LongSessionNudgeTokens int `json:"longSessionNudgeTokens,omitempty"`

	// ── Memory narrative layer dials (memory-narrative-layer-01KQ8TD1) ──

	// MemoryNarrativeEnabled is the user opt-out for the narrative layer
	// (WP12). When false no narratives are produced and the compactor's
	// narrative_first strategy is bypassed. Default true after Phase 2
	// rollout; the feature is additionally gated by
	// HARNESS_MEMORY_NARRATIVE_LAYER env var which defaults false in Phase 1.
	MemoryNarrativeEnabled bool `json:"memoryNarrativeEnabled,omitempty"`

	// SummarizerProfileID is the provider profile used for per-turn LLM
	// synthesis (WP04). Empty (default) means "auto-select cheapest":
	// Haiku-4.5 if available, otherwise first OpenRouter free-tier profile.
	// Invalid ID → typed error in narrative_jobs_pending.last_error.
	SummarizerProfileID string `json:"summarizerProfileId,omitempty"`

	// NarrativePromotionWeights are the per-signal weights used for
	// long-term promotion score (WP06). JSON map with keys "retrieval",
	// "citation", "pin". Defaults: {retrieval:1, citation:3, pin:10}.
	NarrativePromotionWeights string `json:"narrativePromotionWeights,omitempty"`

	// NarrativePromotionThreshold is the score floor for long-term
	// promotion (WP06). Default 10.
	NarrativePromotionThreshold int `json:"narrativePromotionThreshold,omitempty"`

	// NarrativeRetrievalWeight is the score multiplier for narrative
	// chunks during similarity search (WP01). Default 1.5.
	NarrativeRetrievalWeight float64 `json:"narrativeRetrievalWeight,omitempty"`

	// NarrativePromoterParallelism is the number of synthesis workers
	// (WP03). Default 2.
	NarrativePromoterParallelism int `json:"narrativePromoterParallelism,omitempty"`

	// NarrativePreludeTopN is the number of long-term chunks loaded
	// into the system prompt at session start (WP09). Default 5.
	NarrativePreludeTopN int `json:"narrativePreludeTopN,omitempty"`

	// ── Multimodal input dial (multimodal-io-01KQ8TDF WP08 / FR-023) ─────
	//
	// MultimodalInputDisabled is the inverted persisted bit for the
	// multimodal input (image / PDF attachments) feature.
	// Default ON (zero-value Disabled = false → feature enabled) — new
	// installs can drag-and-drop images without any setup. The frontend
	// reads the effective value via the multimodalInputEnabled Bindings
	// (Settings_GetMultimodalInput / Settings_SetMultimodalInput) and
	// hides the paperclip button + drop overlay when false.
	// Note: the HARNESS_MULTIMODAL_IN env flag can force-disable this
	// regardless of the stored value (see capabilities loader).
	MultimodalInputDisabled bool `json:"multimodalInputDisabled,omitempty"`

	// ── Key-rotation dial (provider-keychain-rotation-01KQ8TD9 WP07) ─────
	//
	// AutoResumeOnKeyRotationDisabled is the inverted persisted bit for the
	// "auto-resume the failed turn after rotating an API key" feature. Zero
	// value = feature ENABLED — matching the spec default ("default true").
	// The frontend reads the effective value via EffectiveAutoResumeOnKeyRotation()
	// and hides the dial toggle when HARNESS_KEYCHAIN_ROTATION=off
	// (signalled by AppInfo.keychainRotationEnabled = false).
	AutoResumeOnKeyRotationDisabled bool `json:"autoResumeOnKeyRotationDisabled,omitempty"`

	// ── Multimodal output dials (multimodal-io-extended-01KQ8TD2 WP02) ──

	// AutoCaptureGeneratedImagesDisabled is the inverted persisted bit for
	// the model-generated image auto-capture pipeline. Default ON (zero-value
	// Disabled → capture enabled) — images generated by DALL-E 3,
	// gpt-image-1, Titan Image, etc. are automatically stored as
	// SourceModelOutput artifacts without any user setup. Read via the
	// AutoCaptureGeneratedImages() accessor; never read directly.
	// (multimodal-io-extended-01KQ8TD2 WP02)
	AutoCaptureGeneratedImagesDisabled bool `json:"autoCaptureGeneratedImagesDisabled,omitempty"`

	// MaxGeneratedImageBytes is the per-image byte cap for the auto-capture
	// pipeline. Images whose raw byte size exceeds this value are discarded
	// with a warning log rather than stored. Zero means "use the spec default"
	// (20 MiB). Negative values are rejected at Save. Read via the
	// EffectiveMaxGeneratedImageBytes() accessor.
	// (multimodal-io-extended-01KQ8TD2 WP02)
	MaxGeneratedImageBytes int64 `json:"maxGeneratedImageBytes,omitempty"`

	// LocalRuntimeRAMOverrideGB is a user-supplied override for the RAM
	// quantity (in gibibytes) available for local model loading. When zero,
	// the harness uses the detected SystemRAMBytes. Accepts decimals (e.g.
	// 12.5 for 12.5 GiB). Values < 0 are rejected at Save. Read via the
	// EffectiveLocalRuntimeRAMBytes(detected int64) helper.
	// (local-model-runtimes-01KQ8VMZ WP07)
	LocalRuntimeRAMOverrideGB float64 `json:"localRuntimeRAMOverrideGB,omitempty"`

	// ── Crash reporting dials (sentry-error-monitoring-01KX5R8G) ─────────

	// CrashReportingTier controls whether and how crash reports are sent.
	// One of: "off" (default, zero-value = off), "anonymous", "identified".
	// "off"        — nothing is sent; local crash reports can still be
	//                generated via Settings → Privacy → Crash Reporting.
	// "anonymous"  — structured exception data + redacted breadcrumbs are
	//                transmitted with no user identifier.
	// "identified" — same as anonymous but the user's fleet identity is
	//                attached as a Sentry user tag. Requires fleet login;
	//                auto-downgrades to "anonymous" on logout.
	// (sentry-error-monitoring-01KX5R8G WP02)
	CrashReportingTier string `json:"crashReportingTier,omitempty"`

	// SentryDSN is the Sentry Data Source Name supplied by the operator
	// or advanced user. When empty, crash reporting is inoperative even if
	// CrashReportingTier is non-"off". Self-hosted Sentry / Glitchtip DSNs
	// are accepted as long as they parse as https://<key>@<host>/<project>.
	// (sentry-error-monitoring-01KX5R8G WP02)
	SentryDSN string `json:"sentryDsn,omitempty"`

	// HasSeenCrashReportingOnboarding records whether the first-launch
	// one-time crash-reporting onboarding modal has been shown. When false
	// (default) the frontend shows the modal on first paint and sets this
	// to true after the user dismisses it.
	// (sentry-error-monitoring-01KX5R8G WP05)
	HasSeenCrashReportingOnboarding bool `json:"hasSeenCrashReportingOnboarding,omitempty"`

	// HasSeenFleetTelemetryOnboarding records whether the first-launch
	// one-time fleet-telemetry onboarding modal has been shown. When false
	// (default) the frontend shows the modal on first paint after sign-in
	// and sets this to true after the user dismisses it.
	// (fleet-otel-archival-01NDFSEX11 WP06)
	HasSeenFleetTelemetryOnboarding bool `json:"hasSeenFleetTelemetryOnboarding,omitempty"`

	// FirstRunOnboardingCompleted records whether the first-run onboarding
	// flow (harness-onboarding-01NHON01) has been completed or explicitly
	// dismissed by the user. When false (default) the harness shows the
	// full onboarding flow on first launch after at least one provider is
	// configured, or immediately when no providers are configured.
	// Persisted so the onboarding dialog does not re-show on relaunch.
	// (harness-onboarding-01NHON01 WP01)
	FirstRunOnboardingCompleted bool `json:"firstRunOnboardingCompleted,omitempty"`

	// ChatCustomInstructions is the user's free-text custom instructions
	// appended as the FINAL layer of the chat system prompt, after the
	// graph base, node role, and dynamic environment context
	// (system-prompt-layers WP04). Empty (the default) appends no user
	// layer. The chat runner reads this on every StartStream via
	// LoadChatCustomInstructions so an edit takes effect on the next turn
	// without a restart.
	ChatCustomInstructions string `json:"chatCustomInstructions,omitempty"`

	// BundleSigningPolicy controls signature-verification behaviour for
	// `harness bundle install` (bundle-download-and-verify-01PMZ909
	// UNIT-4, spec D-2). One of: "optional" (default, empty==optional —
	// verify when present, ignore absence), "required" (refuse any
	// unsigned bundle), "forbidden" (refuse any signed bundle). Default
	// is deliberately "optional": flipping the default to "required" at
	// upgrade would turn off a working feature for every user with an
	// unsigned local bundle. See EffectiveBundleSigningPolicy.
	BundleSigningPolicy string `json:"bundleSigningPolicy,omitempty"`
}

// EffectiveBundleSigningPolicy normalizes BundleSigningPolicy to one of
// the three canonical values, defaulting an empty or unrecognized
// string to "optional" — the safe default per spec D-2's own reasoning
// (a hand-edited or older settings file must never silently upgrade to
// "required").
func (s Settings) EffectiveBundleSigningPolicy() string {
	switch s.BundleSigningPolicy {
	case "required", "forbidden":
		return s.BundleSigningPolicy
	default:
		return "optional"
	}
}

// ProviderProfileRef is the wire shape that identifies a provider+model
// pair. Mirrors core/agentgraph/compaction.ProviderProfileRef exactly — the engine
// declares its own copy to avoid depending on this view package; if the
// llm package ever exports a canonical ProviderProfileRef, both copies
// can be replaced with a type alias.
//
// JSON casing follows the existing settings convention (camelCase) used
// elsewhere in this struct.
type ProviderProfileRef struct {
	ProviderID string `json:"providerId,omitempty"`
	ModelID    string `json:"modelId,omitempty"`
}

// IsZero reports whether the ref is the empty value (no provider, no
// model). Used by the chat runner to decide between the configured
// compaction model and the session's active model fallback.
func (r ProviderProfileRef) IsZero() bool {
	return r.ProviderID == "" && r.ModelID == ""
}

// DefaultCompactionArchiveDays is the spec-locked retention window for
// soft-archived original messages (plan §2.7). Reads through the
// EffectiveCompactionArchiveDays accessor.
const DefaultCompactionArchiveDays = 90

// MinCompactionArchiveDays is the lower clamp applied at save and at
// effective-read time. Below this, the sweep would race against
// still-useful reviewable history.
const MinCompactionArchiveDays = 7

// MaxCompactionArchiveDays is the upper clamp applied at save and at
// effective-read time. Above this, archived rows accumulate without
// bound and the sweep stops being a sweep.
const MaxCompactionArchiveDays = 365

// DefaultCompactionRecentWindow is the spec-locked count of most-recent
// user-assistant pairs that compaction never touches (plan §2.6).
const DefaultCompactionRecentWindow = 4

// DefaultMaxAgentTurns is the iteration cap for the chat graph's
// LoopNode body when Settings.MaxAgentTurns is unset (zero). Raised
// 25 -> 500 (owner directive 2026-08-14): autonomous work must be
// possible for hours on end; 25 capped an agentic turn at minutes.
// The doom-loop guard, the verified-exit gate, and the per-run budget
// backstops (chat_default.yaml, raised in the same change) remain the
// behavioural governors.
const DefaultMaxAgentTurns = 500

// AutoCaptureCodeBlocks reports whether the code-block detector is
// active. Default true on a fresh install (zero-value Disabled).
func (s Settings) AutoCaptureCodeBlocks() bool { return !s.AutoCaptureCodeBlocksDisabled }

// SaveArtifactEnabled reports whether the kenaz__save_artifact
// built-in is enabled. Default true on a fresh install (zero-value
// Disabled). Inverted on the wire so the JSON shape matches the
// storage contract.
func (s Settings) SaveArtifactEnabled() bool { return !s.SaveArtifactDisabled }

// AutoCaptureToolOutputs reports whether the tool-output detector is
// active. Default true on a fresh install.
func (s Settings) AutoCaptureToolOutputs() bool { return !s.AutoCaptureToolOutputsDisabled }

// FSRequestAccessEnabled reports whether kenaz__request_filesystem_access
// is enabled. Default true on a fresh install (zero-value Disabled).
func (s Settings) FSRequestAccessEnabled() bool { return !s.FSRequestAccessDisabled }

// SearchEnabled reports whether the cross-session search modal is
// enabled. Default true on a fresh install (zero-value SearchDisabled).
// (cross-session-search-01KQ8TDQ WP07)
func (s Settings) SearchEnabled() bool { return !s.SearchDisabled }

// ── Auto-update accessors / constants (auto-update-v0.4.0 WP05) ─────────

// UpdateChannelStable is the canonical value for the production release
// feed (signed releases on the GitHub release page).
const UpdateChannelStable = "stable"

// UpdateChannelPrerelease is the canonical value for the early-access
// feed (releases marked "prerelease" on GitHub).
const UpdateChannelPrerelease = "prerelease"

// DefaultUpdateCheckIntervalSec is the spec-locked poll interval the
// checker uses when UpdateCheckIntervalSec is zero. Six hours strikes
// the balance between catching same-day hotfixes and not hammering
// GitHub from every long-running session.
const DefaultUpdateCheckIntervalSec = 21600

// MinUpdateCheckIntervalSec is the lowest poll interval the user may
// set from the Settings panel — once an hour. Below this, the rate-limit
// guard would trip on bursty session restarts.
const MinUpdateCheckIntervalSec = 3600

// MaxUpdateCheckIntervalSec is the upper clamp — once a day. Above this,
// the checker is effectively off; users who want it off should toggle
// AutoCheckUpdates instead.
const MaxUpdateCheckIntervalSec = 86400

// AutoCheckUpdates reports whether the auto-update checker fires on
// schedule. Default true on a fresh install (zero-value Disabled).
func (s Settings) AutoCheckUpdates() bool { return !s.AutoCheckUpdatesDisabled }

// EffectiveUpdateChannel returns the persisted channel or "stable" when
// the field is empty / unknown. Save rejects unknown values up front; the
// fallback exists so a hand-edited settings file never bricks the panel.
func (s Settings) EffectiveUpdateChannel() string {
	switch s.UpdateChannel {
	case UpdateChannelStable, UpdateChannelPrerelease:
		return s.UpdateChannel
	default:
		return UpdateChannelStable
	}
}

// EffectiveUpdateCheckIntervalSec returns the user-tuned poll interval
// or DefaultUpdateCheckIntervalSec when zero. Out-of-range persisted
// values are clamped to [Min, Max] for defensive reads.
func (s Settings) EffectiveUpdateCheckIntervalSec() int {
	v := s.UpdateCheckIntervalSec
	if v <= 0 {
		return DefaultUpdateCheckIntervalSec
	}
	if v < MinUpdateCheckIntervalSec {
		return MinUpdateCheckIntervalSec
	}
	if v > MaxUpdateCheckIntervalSec {
		return MaxUpdateCheckIntervalSec
	}
	return v
}

// EffectiveCodeBlockMinLines returns the user-tuned threshold or the
// spec default (10) when the persisted value is zero.
func (s Settings) EffectiveCodeBlockMinLines() int {
	if s.CodeBlockMinLines <= 0 {
		return 10
	}
	return s.CodeBlockMinLines
}

// EffectiveCodeBlockMinBytes returns the user-tuned threshold or the
// spec default (200) when the persisted value is zero.
func (s Settings) EffectiveCodeBlockMinBytes() int {
	if s.CodeBlockMinBytes <= 0 {
		return 200
	}
	return s.CodeBlockMinBytes
}

// ConfirmEachEnabled is the user-facing form of the WP05 modal flag.
// Defaults to true on a fresh install (zero-value ConfirmEachDisabled)
// and inverts the persisted bit so callers don't have to think about
// the storage shape.
func (s Settings) ConfirmEachEnabled() bool { return !s.ConfirmEachDisabled }

// MoveFidelityHistoryEnabled is the user-facing form of the
// model-visible move-fidelity dial (model-moves-transcript-01PMCH01
// WP03). Defaults to true on a fresh install (zero-value
// MoveFidelityHistoryDisabled) and inverts the persisted bit so callers
// never have to reason about the storage shape.
//
// This is the LIVE half of the gate. It is read at the point of
// consumption — on every model-visible history composition — so moving
// it takes effect on the next request rather than the next launch. The
// durable half is session.Record.MoveHistoryMode; effective fidelity is
// the AND of the two, resolved fail-closed.
func (s Settings) MoveFidelityHistoryEnabled() bool { return !s.MoveFidelityHistoryDisabled }

// EffectiveMaxAgentTurns returns the user-tuned cap or the spec
// default (DefaultMaxAgentTurns) when the persisted value is zero.
// The chat-graph runner reads this on every Run start to thread the
// cap onto the LoopNode body's max_iterations.
func (s Settings) EffectiveMaxAgentTurns() int {
	if s.MaxAgentTurns <= 0 {
		return DefaultMaxAgentTurns
	}
	return s.MaxAgentTurns
}

// EffectiveReasoningBudgetTokens returns the extended-thinking budget to
// thread onto the chat graph's model node, or 0 for "reasoning off".
//
// Unlike EffectiveMaxAgentTurns there is no non-zero fallback: zero is a
// meaningful, intended value (the provider default), and substituting a
// default here would silently enable reasoning — and its cost — for
// every existing user on upgrade. A negative persisted value is clamped
// to 0 rather than passed through to the provider.
func (s Settings) EffectiveReasoningBudgetTokens() int {
	if s.ReasoningBudgetTokens <= 0 {
		return 0
	}
	return s.ReasoningBudgetTokens
}

// EffectiveCompactionAggressiveness returns the locked tier enum the
// chat runner / engine consume. Empty / unknown persisted values
// resolve to the documented default ("balanced" via compactionpolicy.Tier's
// own fallback). Returning compactionpolicy.CompactionAggressiveness directly
// keeps the call site one type-cast lighter than going through the raw
// string and lets compactionpolicy.Tier's switch handle unknown values.
func (s Settings) EffectiveCompactionAggressiveness() compactionpolicy.CompactionAggressiveness {
	if s.CompactionAggressiveness == "" {
		return compactionpolicy.AggressivenessBalanced
	}
	return compactionpolicy.CompactionAggressiveness(s.CompactionAggressiveness)
}

// EffectiveCompactionArchiveDays returns the user-tuned retention or
// DefaultCompactionArchiveDays when zero. Out-of-range persisted values
// (a hand-edited settings file, an old client) are clamped to the
// [Min, Max] range so the sweep never queries a nonsense window. Save
// validation rejects out-of-range writes so well-behaved clients never
// hit the clamp branch.
func (s Settings) EffectiveCompactionArchiveDays() int {
	d := s.CompactionArchiveDays
	if d <= 0 {
		return DefaultCompactionArchiveDays
	}
	if d < MinCompactionArchiveDays {
		return MinCompactionArchiveDays
	}
	if d > MaxCompactionArchiveDays {
		return MaxCompactionArchiveDays
	}
	return d
}

// EffectiveCompactionRecentWindow returns the user-tuned recent-window
// (number of most-recent user-assistant pairs compaction never touches)
// or DefaultCompactionRecentWindow when zero or negative.
func (s Settings) EffectiveCompactionRecentWindow() int {
	if s.CompactionRecentWindow <= 0 {
		return DefaultCompactionRecentWindow
	}
	return s.CompactionRecentWindow
}

// EffectivePermissionMode returns the persisted mode or "normal" when
// the field is empty/unknown. Valid values: "strict", "normal", "permissive".
func (s Settings) EffectivePermissionMode() string {
	switch s.PermissionMode {
	case "strict", "normal", "permissive":
		return s.PermissionMode
	default:
		return "normal"
	}
}

// ── Branch Advisor constants ─────────────────────────────────────────

// DefaultBranchAdvisorMinConfidence is the spec-locked heuristic
// threshold (Q29.1). Banner mounts only when the detector's confidence
// meets or exceeds this value.
const DefaultBranchAdvisorMinConfidence = 0.85

// DefaultBranchReintegrationMaxTokens is the spec-locked default token
// budget for the reintegration summarization call (FR-008a).
const DefaultBranchReintegrationMaxTokens = 2000

// MinBranchReintegrationMaxTokens is the lower clamp (FR-008a).
const MinBranchReintegrationMaxTokens = 500

// MaxBranchReintegrationMaxTokens is the upper clamp (FR-008a).
const MaxBranchReintegrationMaxTokens = 16000

// EffectiveBranchAdvisorMinConfidence returns the configured threshold
// or DefaultBranchAdvisorMinConfidence when zero.
func (s Settings) EffectiveBranchAdvisorMinConfidence() float64 {
	if s.BranchAdvisorMinConfidence <= 0 {
		return DefaultBranchAdvisorMinConfidence
	}
	return s.BranchAdvisorMinConfidence
}

// EffectiveBranchReintegrationMaxTokens returns the configured token
// budget or DefaultBranchReintegrationMaxTokens when zero. Out-of-range
// persisted values are clamped to [Min, Max].
func (s Settings) EffectiveBranchReintegrationMaxTokens() int {
	t := s.BranchReintegrationMaxTokens
	if t <= 0 {
		return DefaultBranchReintegrationMaxTokens
	}
	if t < MinBranchReintegrationMaxTokens {
		return MinBranchReintegrationMaxTokens
	}
	if t > MaxBranchReintegrationMaxTokens {
		return MaxBranchReintegrationMaxTokens
	}
	return t
}

// EffectiveBranchAdvisorDefaultModel returns BranchAdvisorDefaultModel
// when set, otherwise falls back to CompactionModel (which itself
// defaults to the session's active model when zero).
func (s Settings) EffectiveBranchAdvisorDefaultModel() ProviderProfileRef {
	if !s.BranchAdvisorDefaultModel.IsZero() {
		return s.BranchAdvisorDefaultModel
	}
	return s.CompactionModel
}

// MaxMonthlyCostNotifyUSD is the upper clamp on the monthly-spend
// notification dial. Values above this are rejected at Save and at
// effective-read time (token-cost-telemetry-01KQ8TD7 WP06). The cap
// keeps the dial usable for sane provider-billing budgets without
// masking absurd hand-edited values.
const MaxMonthlyCostNotifyUSD = 10000.0

// MonthlyCostNotifyEnabled reports whether the threshold scheduler
// should fire for this settings record. Zero (or any negative slip-in
// from a hand-edited file) disables the scheduler completely.
func (s Settings) MonthlyCostNotifyEnabled() bool {
	return s.MonthlyCostNotifyUSD > 0
}

// MCPAutoRestart reports whether MCP servers should be automatically
// restarted after two consecutive ping failures. Default true on a fresh
// install (zero-value Disabled → restart enabled).
// (mcp-server-health-ui-01KQ8TD6 WP06)
func (s Settings) MCPAutoRestart() bool { return !s.MCPAutoRestartDisabled }

// AutoTitleEnabled reports whether session auto-titling is on. Default
// true on a fresh install (zero-value Disabled → feature enabled).
// (p0-wiring-fixes-3TVMG0MX WP05)
func (s Settings) AutoTitleEnabled() bool { return !s.AutoTitleDisabled }

// FSReadEnabled reports whether the read-family builtin filesystem tools
// are enabled. Default false on a fresh install (zero-value Disabled →
// tools off). The user opts in from the Tools panel (WP06).
// (builtin-filesystem-tools-01KR3N4P)
func (s Settings) FSReadEnabled() bool { return !s.FSReadDisabled }

// FSWriteEnabled reports whether the write-family builtin filesystem
// tools are enabled. Default false on a fresh install (zero-value
// Disabled → tools off). The user opts in from the Tools panel (WP06).
// (builtin-filesystem-tools-01KR3N4P)
func (s Settings) FSWriteEnabled() bool { return !s.FSWriteDisabled }

// TodoEnabled reports whether the kenaz__todo_write builtin tool is
// enabled. Default false on a fresh install (zero-value Disabled →
// tool off). The user opts in from the Tools panel.
// (builtin-tools-search-and-elicitation-01KZNP3D WP07)
func (s Settings) TodoEnabled() bool { return !s.TodoDisabled }

// EditFileArtifactSyncEnabled reports whether the edit-file artifact
// sync feature is enabled for this user. Default true on a fresh install
// (zero-value Disabled → feature enabled). The env-var gate
// HARNESS_EDIT_FILE_ARTIFACT_SYNC=on must also be set for the feature
// to activate. (edit-file-artifact-sync-01KQ8TD5)
func (s Settings) EditFileArtifactSyncEnabled() bool { return !s.EditFileArtifactSyncDisabled }

// ── Branching UX constants + accessors (branching-ux-polish-01KQ8TD7 WP06) ──

// DefaultMaxVisibleBranchDepth is the spec-locked depth cap for sidebar
// branch tree indentation clamping. Default 5.
const DefaultMaxVisibleBranchDepth = 5

// DefaultAutoCollapseBranchesInSidebar is the spec-locked default for
// the collapse-on-first-load behaviour: true so the rail doesn't sprawl.
const DefaultAutoCollapseBranchesInSidebar = true

// EffectiveMaxVisibleBranchDepth returns the user-tuned depth or the
// spec default when zero or negative. No upper clamp — a user who wants
// depth=100 gets it; the frontend caps its CTE walk at 32.
func (s Settings) EffectiveMaxVisibleBranchDepth() int {
	if s.MaxVisibleBranchDepth <= 0 {
		return DefaultMaxVisibleBranchDepth
	}
	return s.MaxVisibleBranchDepth
}

// EffectiveAutoCollapseBranchesInSidebar returns the user's collapse
// preference, or DefaultAutoCollapseBranchesInSidebar when nothing has
// ever been persisted (nil pointer — a fresh install, or a settings.json
// written before controls-and-readouts-that-tell-the-truth-01PMZ808
// WP03, which never wrote this key). AutoCollapseBranchesInSidebar is a
// *bool precisely so this distinction is representable: a stored false
// is honoured as false, and only true absence falls back to the default.
func (s Settings) EffectiveAutoCollapseBranchesInSidebar() bool {
	if s.AutoCollapseBranchesInSidebar == nil {
		return DefaultAutoCollapseBranchesInSidebar
	}
	return *s.AutoCollapseBranchesInSidebar
}

// WindowSize mirrors the charter's WindowSize type.
type WindowSize struct {
	Width  int `json:"width"`
	Height int `json:"height"`
}

// SettingsStore is the persistence interface (default impl: single
// JSON file at $USER_CONFIG_DIR/kenaz-harness/settings.json).
type SettingsStore interface {
	LoadAll() (Settings, error)
	SaveAll(Settings) error
	LoadRoute() (string, error)
	SaveRoute(string) error
	LogRouteChange(from, to string) error
	LoadTheme() (string, error)
	SaveTheme(string) error
	// LoadMemory / SaveMemory expose the long-term-memory opt-in
	// independently of the full Settings round-trip so the rpc layer
	// can read it on the hot path (every send) without serializing
	// the whole record.
	LoadMemory() (bool, error)
	SaveMemory(enabled bool) error
	// LoadConfirmEach / SaveConfirmEach expose the WP05 confirm-each
	// tool-call modal opt-in independently of the full Settings
	// record. The toolloop reads this on every Run boundary so the
	// frontend toggle takes effect on the next chat without a
	// settings round-trip. Default true (modal ON unless the user
	// turns it off explicitly).
	LoadConfirmEach() (bool, error)
	SaveConfirmEach(enabled bool) error
	// LoadWebSearch / SaveWebSearch expose the local-first web search
	// built-in opt-in independently of the full Settings record. The
	// toolloop / kernel reads this on every Run boundary so the toggle
	// takes effect on the next chat. Default false (off).
	LoadWebSearch() (bool, error)
	SaveWebSearch(enabled bool) error
	// LoadBash / SaveBash expose the local-first bash built-in opt-in
	// in the same shape. Default false (off).
	LoadBash() (bool, error)
	SaveBash(enabled bool) error
	// LoadWebFetchEnabled / SaveWebFetchEnabled expose the kenaz__web_fetch
	// built-in opt-in. Default false (off) — the tool makes outbound HTTP
	// requests with @secret: substitution; the user must opt in and the Cedar
	// network gate applies regardless. (crash-recovery-tool-gating-0XQTC4RK FR-005)
	LoadWebFetchEnabled() (bool, error)
	SaveWebFetchEnabled(enabled bool) error
	// LoadSaveArtifactEnabled / SaveSaveArtifactEnabled expose the
	// kenaz__save_artifact built-in opt-in. Default true (on) — saving
	// deliverables is a low-risk primitive that should work on first
	// launch. The toolloop's EnabledFilter consults the predicate
	// (which calls LoadSaveArtifactEnabled) on every Run boundary so a
	// toggle takes effect on the next chat.
	LoadSaveArtifactEnabled() (bool, error)
	SaveSaveArtifactEnabled(enabled bool) error
	// LoadMaxAgentTurns / SaveMaxAgentTurns expose the chat-graph
	// LoopNode iteration cap independently of the full Settings record
	// so the chassis can read it on the hot path (every chat run start)
	// without serializing the whole record. Persists as the raw int;
	// zero on the wire means "use DefaultMaxAgentTurns". The frontend
	// SettingsClient surfaces this via Settings_GetMaxAgentTurns /
	// Settings_SetMaxAgentTurns.
	LoadMaxAgentTurns() (int, error)
	SaveMaxAgentTurns(turns int) error

	// ── WP08 permission dial store accessors ────────────────────────

	// LoadPermissionMode / SavePermissionMode expose the global
	// permission posture dial. Returns "normal" when unset.
	LoadPermissionMode() (string, error)
	SavePermissionMode(mode string) error
	// LoadPermissionCacheDangerousOps / SavePermissionCacheDangerousOps
	// expose the dangerous-ops override flag. Default false.
	LoadPermissionCacheDangerousOps() (bool, error)
	SavePermissionCacheDangerousOps(enabled bool) error
	// LoadBashAllowlistMigrated / SaveBashAllowlistMigrated expose the
	// WP10 migration marker. Default false.
	LoadBashAllowlistMigrated() (bool, error)
	SaveBashAllowlistMigrated(migrated bool) error
	// LoadPermissionsMigrationToastShown / SavePermissionsMigrationToastShown
	// expose the one-time toast marker. Default false.
	LoadPermissionsMigrationToastShown() (bool, error)
	SavePermissionsMigrationToastShown(shown bool) error

	// LoadCedarStrictCredentialMode / SaveCedarStrictCredentialMode
	// expose the WP05 credential-gate strictness dial. Default false
	// (lenient): NotApplicable outcomes from the Cedar gate allow
	// access. When true (strict): NotApplicable for non-mcp_spawn
	// purposes is treated as deny. The credstore.Store reads this via
	// a func() bool callback threaded through its Config so the dial
	// takes effect on the next Use call without re-creating the store.
	LoadCedarStrictCredentialMode() (bool, error)
	SaveCedarStrictCredentialMode(enabled bool) error

	// LoadCedarStrictWorkflowMode / SaveCedarStrictWorkflowMode expose
	// the Workflow-family strictness dial. Default false (permissive).
	//
	// This is the producer for the `mode` context attribute the shipped
	// `default_workflows_policy.cedar` bundle branches on. Its strict
	// arm forbids saving a workflow that carries a `shell` step; until
	// this dial existed nothing in the repo set the attribute outside
	// tests, so that arm shipped embedded in every engine and could
	// never fire.
	//
	// Read per gate call through workflowsview.Config.CedarModeFn, so a
	// change takes effect on the next run/save without an app restart —
	// the same shape as the credential dial above.
	LoadCedarStrictWorkflowMode() (bool, error)
	SaveCedarStrictWorkflowMode(enabled bool) error

	// LoadFSRequestAccessEnabled / SaveFSRequestAccessEnabled expose
	// the kenaz__request_filesystem_access built-in opt-in. Default
	// true (on) — requesting expanded filesystem access is a low-risk
	// convenience that should work on a fresh install.
	LoadFSRequestAccessEnabled() (bool, error)
	SaveFSRequestAccessEnabled(enabled bool) error

	// LoadMonthlyCostNotifyUSD / SaveMonthlyCostNotifyUSD expose the
	// monthly-spend notification threshold dial
	// (token-cost-telemetry-01KQ8TD7 WP06). Zero disables the
	// scheduler. NARROWED
	// (controls-and-readouts-that-tell-the-truth-01PMZ808 UNIT-14
	// WP19, FR-029, 2026-08-21): Save normalises a negative value to
	// zero (does not reject it) and rejects only values above
	// MaxMonthlyCostNotifyUSD — matching the FileStore/binding docs,
	// which already said "normalised to zero" correctly. The other
	// half of this doc is true and unchanged: the threshold checker
	// reads through LoadMonthlyCostNotifyUSD on every Manager.Add
	// tail (core/rpc/api.go, core/usage/usage.go).
	LoadMonthlyCostNotifyUSD() (float64, error)
	SaveMonthlyCostNotifyUSD(usd float64) error

	// ── Auto-update dial accessors (auto-update-v0.4.0 WP05) ────────────
	//
	// LoadAutoCheckUpdates / SaveAutoCheckUpdates expose the master
	// "automatically check for updates" toggle. Default true on a fresh
	// install (zero-value AutoCheckUpdatesDisabled).
	LoadAutoCheckUpdates() (bool, error)
	SaveAutoCheckUpdates(enabled bool) error

	// LoadUpdateChannel / SaveUpdateChannel expose the release channel
	// dial. Returns "stable" when unset / unknown. Save rejects unknown
	// values with ErrInvalidUpdateChannel.
	LoadUpdateChannel() (string, error)
	SaveUpdateChannel(channel string) error

	// LoadUpdateCheckInterval / SaveUpdateCheckInterval expose the poll
	// interval as a time.Duration. The wire shape is seconds; the API
	// sugar returns / accepts a Duration so callers don't have to multiply.
	// Zero on Load means "use the default"; SaveUpdateCheckInterval(0)
	// clears the override.
	LoadUpdateCheckInterval() (time.Duration, error)
	SaveUpdateCheckInterval(d time.Duration) error

	// LoadSkippedUpdateVersions returns the user's skip-list. Empty slice
	// when unset.
	LoadSkippedUpdateVersions() ([]string, error)
	// SaveSkippedUpdateVersions atomically replaces the full skip-list.
	// Used by the Settings panel's reset / batch flows; the per-version
	// helpers below dispatch through this under the hood.
	SaveSkippedUpdateVersions(versions []string) error
	// AppendSkippedUpdateVersion adds one version (idempotent). Called
	// from the UpdateMenu's "Skip this version" action.
	AppendSkippedUpdateVersion(version string) error
	// RemoveSkippedUpdateVersion removes one version (no-op if missing).
	// Called from the Settings panel's per-row "Unskip" link.
	RemoveSkippedUpdateVersion(version string) error

	// LoadAutonomyProfile / SaveAutonomyProfile expose the global
	// autonomy.Layer (autonomy-dial-01KR3M2A WP02) independently of the
	// full Settings record so the autonomy resolver can read it on the
	// hot path (every chat turn) without serializing the whole record.
	// An empty Layer (nil Level + empty Overrides) round-trips as the
	// missing/empty Autonomy field in settings.json — the resolver then
	// falls back to the tier-default. A missing settings file returns
	// the empty Layer + nil error so a fresh install boots with the
	// canonical Default tier.
	LoadAutonomyProfile() (autonomy.Layer, error)
	SaveAutonomyProfile(layer autonomy.Layer) error

	// LoadMCPAutoRestart / SaveMCPAutoRestart expose the MCP server
	// auto-restart dial (mcp-server-health-ui-01KQ8TD6 WP06). Default
	// true on a fresh install (zero-value Disabled → restart enabled).
	LoadMCPAutoRestart() (bool, error)
	SaveMCPAutoRestart(enabled bool) error

	// LoadAutoTitleEnabled / SaveAutoTitleEnabled expose the session
	// auto-title feature flag (p0-wiring-fixes-3TVMG0MX WP05). Default
	// true on a fresh install (zero-value Disabled → feature enabled).
	LoadAutoTitleEnabled() (bool, error)
	SaveAutoTitleEnabled(enabled bool) error

	// ── Builtin filesystem tool dial accessors (builtin-filesystem-tools-01KR3N4P) ──

	// LoadFSReadEnabled / SaveFSReadEnabled expose the read-family
	// filesystem tool opt-in. Default false (tools off until the user
	// enables them from the Tools panel). The toolloop's EnabledFilter
	// consults this on every Run boundary so a toggle takes effect on
	// the next chat turn.
	LoadFSReadEnabled() (bool, error)
	SaveFSReadEnabled(enabled bool) error

	// LoadFSWriteEnabled / SaveFSWriteEnabled expose the write-family
	// filesystem tool opt-in. Default false (tools off).
	LoadFSWriteEnabled() (bool, error)
	SaveFSWriteEnabled(enabled bool) error

	// LoadTodoEnabled / SaveTodoEnabled expose the kenaz__todo_write
	// builtin tool opt-in. Default false (tool off until the user enables
	// it from the Tools panel). The toolloop's EnabledFilter consults this
	// on every Run boundary so a toggle takes effect on the next chat turn.
	// (builtin-tools-search-and-elicitation-01KZNP3D WP07)
	LoadTodoEnabled() (bool, error)
	SaveTodoEnabled(enabled bool) error

	// LoadEditFileArtifactSyncEnabled / SaveEditFileArtifactSyncEnabled
	// expose the per-user opt-in for the edit-file artifact sync
	// pipeline (edit-file-artifact-sync-01KQ8TD5 WP04). Default true
	// (enabled, matching the zero-value of EditFileArtifactSyncDisabled).
	// The env-var gate HARNESS_EDIT_FILE_ARTIFACT_SYNC=on must also be
	// set for the pipeline to activate.
	LoadEditFileArtifactSyncEnabled() (bool, error)
	SaveEditFileArtifactSyncEnabled(enabled bool) error

	// ── Embedder configuration (v0.5.2 universal-embedder fix) ──────────────
	//
	// LoadEmbedderConfig returns the persisted (profileID, modelOverride)
	// pair.  Empty strings mean "use defaults" (auto-pick first eligible
	// provider, per-Kind default model).  Never returns an error for a
	// missing settings file — returns ("", "") and nil.
	LoadEmbedderConfig() (profileID, modelOverride string, err error)
	// SaveEmbedderConfig persists the embedder provider selection and
	// optional model override.  Either or both may be the empty string to
	// reset to the auto-pick / per-Kind-default behaviour.
	SaveEmbedderConfig(profileID, modelOverride string) error

	// ── Memory narrative layer dial accessors (memory-narrative-layer-01KQ8TD1) ──

	// LoadSummarizerProfileID / SaveSummarizerProfileID expose the
	// per-turn LLM synthesis model selection (WP04). Empty means
	// "auto-select cheapest".
	LoadSummarizerProfileID() (string, error)
	SaveSummarizerProfileID(profileID string) error

	// LoadChatCustomInstructions / SaveChatCustomInstructions expose the
	// user's chat custom-instructions text (system-prompt-layers WP04)
	// independently of the full Settings record so the chat runner can
	// read it on the hot path (every StartStream) without serializing the
	// whole record. Empty means "no user layer".
	LoadChatCustomInstructions() (string, error)
	SaveChatCustomInstructions(text string) error

	// LoadMemoryNarrativeEnabled / SaveMemoryNarrativeEnabled expose the
	// narrative layer opt-out dial (WP12). Default true after Phase 2.
	LoadMemoryNarrativeEnabled() (bool, error)
	SaveMemoryNarrativeEnabled(enabled bool) error

	// ── Key-rotation dial (provider-keychain-rotation-01KQ8TD9 WP07) ────────

	// LoadAutoResumeOnKeyRotation / SaveAutoResumeOnKeyRotation expose the
	// "auto-resume the paused turn after rotating an API key" dial. Default
	// true on a fresh install (zero-value AutoResumeOnKeyRotationDisabled →
	// feature enabled). The LLM RPC's TestAndRotateKey reads this on each
	// rotation to decide whether to return an AutoResumeToken. The frontend
	// toggle in Settings → Models hides when keychainRotationEnabled = false.
	LoadAutoResumeOnKeyRotation() (bool, error)
	SaveAutoResumeOnKeyRotation(enabled bool) error

	// LoadFirstRunOnboardingCompleted / SaveFirstRunOnboardingCompleted expose
	// the first-run onboarding completion flag (harness-onboarding-01NHON01
	// WP01). Default false (= show onboarding) on a fresh install. Persisted
	// so quit/relaunch resumes without re-showing the completed flow. The
	// onboarding view reads and writes this; the value is also carried in the
	// full Settings round-trip via FirstRunOnboardingCompleted.
	LoadFirstRunOnboardingCompleted() (bool, error)
	SaveFirstRunOnboardingCompleted(completed bool) error

	// LoadGraphAuthoringEnabled / SaveGraphAuthoringEnabled expose the
	// model-authored-agent-graphs consent dial
	// (model-authored-graphs-01PMGA01 UNIT-4, FR-006). Default false —
	// on a fresh install, and on any install upgraded from a build that
	// predates this field (settings.json simply lacks the key, and JSON
	// decode leaves a bool field at its Go zero value), graph.author is
	// denied until the user opts in from Settings. Read live on every
	// graph.author evaluation via GraphAuthoringEnabledFn so a toggle
	// takes effect on the next draft attempt without an app restart —
	// same shape as LoadCedarStrictWorkflowMode above. Deliberately NOT
	// added to harness.SettingsAllowlist: a session that could flip its
	// own authoring permission via harness_write_set_setting would have
	// no permission gate at all (same reasoning
	// harness-self-attach-01PMHS01 reached independently for
	// HarnessSelfMCPDisabled).
	LoadGraphAuthoringEnabled() (bool, error)
	SaveGraphAuthoringEnabled(enabled bool) error
}

// SettingsAPI is the view-scoped accessor exposed via HarnessAPI.
type SettingsAPI interface {
	Get(ctx context.Context) (Settings, error)
	Set(ctx context.Context, s Settings) error
	// LoadAutonomyProfile returns the persisted global autonomy.Layer.
	// Empty Layer means "use the tier-default fallback."
	// (autonomy-dial-01KR3M2A WP03)
	LoadAutonomyProfile(ctx context.Context) (autonomy.Layer, error)
	// SaveAutonomyProfile persists the global autonomy.Layer. An empty
	// Layer clears the field.
	SaveAutonomyProfile(ctx context.Context, layer autonomy.Layer) error
	// GetMCPAutoRestart returns whether MCP servers should auto-restart
	// after two consecutive ping failures. Default true.
	// (mcp-server-health-ui-01KQ8TD6 WP06)
	GetMCPAutoRestart(ctx context.Context) (bool, error)
	// SetMCPAutoRestart persists the MCP auto-restart dial.
	SetMCPAutoRestart(ctx context.Context, enabled bool) error
	// GetAutoTitleEnabled returns whether session auto-titling is on.
	// Default true on a fresh install (zero-value → enabled).
	// (p0-wiring-fixes-3TVMG0MX WP05)
	GetAutoTitleEnabled(ctx context.Context) (bool, error)
	// SetAutoTitleEnabled persists the auto-title feature toggle.
	SetAutoTitleEnabled(ctx context.Context, enabled bool) error
	// GetEmbedderConfig returns the persisted (profileID, modelOverride)
	// pair for the memory embedder.  Empty strings mean "auto-pick".
	// (v0.5.2 universal-embedder fix)
	GetEmbedderConfig(ctx context.Context) (profileID, modelOverride string, err error)
	// SetEmbedderConfig persists the embedder provider selection and
	// optional model override.  Empty strings reset to auto-pick /
	// per-Kind-default behaviour.
	SetEmbedderConfig(ctx context.Context, profileID, modelOverride string) error
	// GetArtifactPreview returns the runtime artifact-preview feature config:
	//   - enabled: false when HARNESS_ARTIFACT_BINARY_PREVIEW=false (default true).
	//   - maxBytes: resolved from HARNESS_ARTIFACT_PREVIEW_MAX_BYTES (default 5 MiB).
	//   - timeoutMs: preview abort timeout in milliseconds (default 2000).
	// (artifact-preview-binary-rendering-01KQ8TD5 WP07)
	GetArtifactPreview(ctx context.Context) (enabled bool, maxBytes int64, timeoutMs int64, err error)

	// ── Memory narrative layer settings (memory-narrative-layer-01KQ8TD1) ──

	// GetSummarizerProfileID returns the configured summariser provider
	// profile ID. Empty string means "auto-select cheapest" (WP04).
	GetSummarizerProfileID(ctx context.Context) (string, error)
	// SetSummarizerProfileID persists the summariser profile ID. An
	// empty string resets to auto-select.
	SetSummarizerProfileID(ctx context.Context, profileID string) error

	// GetChatCustomInstructions returns the user's chat custom-instructions
	// text appended as the final system-prompt layer. Empty means none.
	// (system-prompt-layers WP04)
	GetChatCustomInstructions(ctx context.Context) (string, error)
	// SetChatCustomInstructions persists the chat custom-instructions text.
	// An empty string clears the user layer.
	SetChatCustomInstructions(ctx context.Context, text string) error

	// GetMemoryNarrativeEnabled returns whether the narrative layer is
	// enabled in Settings (additional env-var gate applies). Default
	// true after Phase 2 rollout (WP12).
	GetMemoryNarrativeEnabled(ctx context.Context) (bool, error)
	// SetMemoryNarrativeEnabled persists the narrative-layer opt-out dial.
	SetMemoryNarrativeEnabled(ctx context.Context, enabled bool) error

	// ── Audit log settings (audit-log-enhancement-01KX5R8F WP07) ─────────────

	// GetAuditSettings returns the current audit log retention configuration.
	GetAuditSettings(ctx context.Context) (AuditSettings, error)
	// SetAuditSettings persists the audit log retention configuration.
	SetAuditSettings(ctx context.Context, s AuditSettings) error

	// ── Fleet auth (fleet-auth-foundation-01NDFSEX08 WP05) ───────────────────

	// FleetSignIn kicks off the PKCE authorization code flow. It opens the
	// system browser and waits for the callback. On success it calls enroll
	// and returns the user's Identity. Returns fleet.ErrFleetDisabled when
	// HARNESS_FLEET_DISABLED=1.
	FleetSignIn(ctx context.Context) (FleetIdentity, error)

	// FleetSignOut clears persisted tokens and the identity cache.
	// Returns fleet.ErrFleetDisabled when the kill switch is active.
	FleetSignOut(ctx context.Context) error

	// FleetSignedIn reports whether the user has a valid cached Identity.
	FleetSignedIn(ctx context.Context) (bool, error)

	// FleetRefreshIdentity re-calls the fleet enroll endpoint and updates
	// the cached identity. Returns fleet.ErrNotSignedIn if not signed in.
	FleetRefreshIdentity(ctx context.Context) (FleetIdentity, error)

	// FleetProfile returns the active env profile for UI rendering.
	// Does NOT expose ClientID, APIAudience, or any secret fields.
	FleetProfile(ctx context.Context) (FleetProfileInfo, error)

	// ── Fleet capabilities (fleet-capability-surface-01NDFSEX09 WP11) ───────

	// FleetCapabilities returns the in-memory capability snapshot.
	// Returns an empty CapabilitiesView when signed out or fleet is disabled.
	FleetCapabilities(ctx context.Context) (CapabilitiesView, error)

	// FleetRefreshCapabilities forces an immediate fetch from the fleet
	// capabilities endpoint and returns the updated snapshot.
	FleetRefreshCapabilities(ctx context.Context) (CapabilitiesView, error)

	// ── Fleet config-pull (fleet-config-pull-01NDFSEX10 WP02/WP06) ──────────

	// FleetConfigPullStatus returns the current config-pull poller state:
	// last applied bundle_id, last applied timestamp, last error (if any),
	// and source ("fleet" | "cache" | "default-deny").
	FleetConfigPullStatus(ctx context.Context) (FleetConfigPullStatusView, error)

	// FleetLockdownStatus returns the current emergency lockdown state.
	// Safe to call from any goroutine; reads the process-global atomic flag.
	// (fleet-emergency-lockdown-01NDFSEX12 WP02)
	FleetLockdownStatus(ctx context.Context) (LockdownStatusView, error)

	// FleetHealth returns a compact fleet health summary: signing-key presence,
	// config source, last error, and session state. Used by the global health
	// indicator chip (WP10 / FR-002 / FR-010).
	FleetHealth(ctx context.Context) (FleetHealthView, error)

	// FleetTelemetryOptIns returns the per-class telemetry opt-in set from the
	// fleet store (source of truth, replacing local-only JSON).
	// (harness-fleet-sync-activation-01NSYNC01 gap #4)
	FleetTelemetryOptIns(ctx context.Context) ([]TelemetryOptInView, error)

	// FleetSetTelemetryOptIn flips a single telemetry class opt-in in the fleet
	// store and refreshes the local cache.
	// (harness-fleet-sync-activation-01NSYNC01 gap #4)
	FleetSetTelemetryOptIn(ctx context.Context, class string, optedIn bool) error
}

// CapabilitiesView is the wire-safe projection of fleet.Capabilities.
// Capability keys are plain strings in the frontend; the Capability type
// is Go-internal.
type CapabilitiesView struct {
	Tier      string          `json:"tier"`
	Enabled   map[string]bool `json:"enabled"`
	FetchedAt string          `json:"fetchedAt"`
	Source    string          `json:"source"`
}

// FleetIdentity is the view-layer projection of fleet.Identity. It is safe
// to serialize to the frontend and contains no secrets.
type FleetIdentity struct {
	UserID      string   `json:"userId"`
	OrgID       string   `json:"orgId"`
	TeamID      string   `json:"teamId"`
	Email       string   `json:"email,omitempty"`
	DisplayName string   `json:"displayName,omitempty"`
	Tier        string   `json:"tier,omitempty"`
	OrgName     string   `json:"orgName,omitempty"`
	TeamName    string   `json:"teamName,omitempty"`
	Roles       []string `json:"roles,omitempty"`
}

// FleetProfileInfo is the safe projection of fleet.EnvProfile for UI
// rendering. Does NOT include ClientID, APIAudience, or any secret fields.
type FleetProfileInfo struct {
	Name         string `json:"name"`
	BadgeColor   string `json:"badgeColor"`
	FleetBaseURL string `json:"fleetBaseUrl"`
	Configured   bool   `json:"configured"`
}

// FleetConfigPullStatusView is the wire-safe projection of fleet.ConfigPollStatus.
// Mirrors fleet.ConfigPollStatus; field names are camelCase for the frontend.
type FleetConfigPullStatusView struct {
	// LastAppliedID is the bundle_id of the last successfully applied bundle, or
	// 0 if no bundle has been applied since the harness started.
	LastAppliedID int64 `json:"lastAppliedId"`
	// LastAppliedAt is the RFC3339 timestamp of the last apply, or empty string.
	LastAppliedAt string `json:"lastAppliedAt"`
	// LastError is the most recent error string, or empty when healthy.
	LastError string `json:"lastError"`
	// Source is "fleet", "cache", or "default-deny".
	Source string `json:"source"`
	// BundleChecksum is the hex SHA-256 of the last-seen bundle body (used for
	// 304 Not Modified gating).
	BundleChecksum string `json:"bundleChecksum"`
	// ConfigDistributionEnabled reports whether a fleet signing key is wired
	// in this binary (FR-002). When false the fleet config distribution is
	// silently disabled (safe fail-closed), and the UI shows a degraded state.
	ConfigDistributionEnabled bool `json:"configDistributionEnabled"`
}

// FleetHealthView is the wire-safe projection of overall fleet health
// surfaced to the global health indicator (WP10 / FR-002 / FR-010).
type FleetHealthView struct {
	// ConfigDistributionEnabled reports whether a fleet signing key is wired
	// in this binary. false means fleet config bundles can never be applied.
	ConfigDistributionEnabled bool `json:"configDistributionEnabled"`
	// ConfigSource is the last-known config source: "fleet", "stale-cache",
	// "default-deny-degraded", or "no-key".
	ConfigSource string `json:"configSource"`
	// ConfigLastError is the most recent error string from the config poller.
	ConfigLastError string `json:"configLastError"`
	// SignedIn is true when the client has a valid (non-expired) session.
	SignedIn bool `json:"signedIn"`
}

// AuditSettings holds the operator-configurable audit log retention policy.
// Default: keep_forever, so no data is ever silently dropped on first run.
type AuditSettings struct {
	// Strategy is the retention strategy.
	// One of: "keep_forever", "delete_after_window", "archive_after_window".
	Strategy string `json:"strategy,omitempty"`
	// WindowDays is the retention window in days. Only meaningful when
	// Strategy is not keep_forever.
	WindowDays int `json:"window_days,omitempty"`
	// RetentionEnforced reports whether SOMETHING actually deletes rows
	// per the strategy above — as opposed to Strategy just being a
	// setting nobody reads. False until audit-that-tells-the-truth-
	// 01PMZA10 UNIT-8 lands a real sweep; UNIT-4 wires this field itself
	// (derived from the settings API's construction-time wiring, via
	// SetAuditRetentionEnforced — never a literal inside GetAuditSettings)
	// so AuditSettingsPanel.vue can render fact-driven copy starting
	// here, with zero further frontend edit once UNIT-8 flips the
	// underlying fact (spec D-8). Named to match fleet-enforcement-
	// truth-01PMZ505's ComplianceStatus.RetentionEnforced so the audit
	// and compliance panels read the same vocabulary once both land.
	RetentionEnforced bool `json:"retention_enforced"`
}

// ── Long-session nudge constants + accessors (v0.5.6) ───────────────────────

// DefaultLongSessionNudgeTurns is the spec-locked default number of
// user-assistant turn pairs (half the message count) after which the
// long-session nudge banner fires.
const DefaultLongSessionNudgeTurns = 30

// DefaultLongSessionNudgeTokens is the spec-locked cumulative prompt-token
// threshold after which the nudge banner fires regardless of turn count.
const DefaultLongSessionNudgeTokens = 50000

// EffectiveLongSessionNudgeTurns returns the user-tuned threshold or the
// spec default (DefaultLongSessionNudgeTurns) when zero.
func (s Settings) EffectiveLongSessionNudgeTurns() int {
	if s.LongSessionNudgeTurns <= 0 {
		return DefaultLongSessionNudgeTurns
	}
	return s.LongSessionNudgeTurns
}

// EffectiveLongSessionNudgeTokens returns the user-tuned threshold or the
// spec default (DefaultLongSessionNudgeTokens) when zero.
func (s Settings) EffectiveLongSessionNudgeTokens() int {
	if s.LongSessionNudgeTokens <= 0 {
		return DefaultLongSessionNudgeTokens
	}
	return s.LongSessionNudgeTokens
}

// ── Memory narrative layer constants + accessors (memory-narrative-layer-01KQ8TD1) ──

// DefaultNarrativeRetrievalWeight is the retrieval-score multiplier
// for narrative chunks when NarrativeRetrievalWeight is zero.
const DefaultNarrativeRetrievalWeight = 1.5

// DefaultNarrativePromotionThreshold is the score floor for long-term
// promotion when NarrativePromotionThreshold is zero.
const DefaultNarrativePromotionThreshold = 10

// DefaultNarrativePromoterParallelism is the default worker count for
// the Promoter when NarrativePromoterParallelism is zero.
const DefaultNarrativePromoterParallelism = 2

// DefaultNarrativePreludeTopN is the default number of long-term chunks
// loaded into the system prompt at session start.
const DefaultNarrativePreludeTopN = 5

// EffectiveNarrativeRetrievalWeight returns the user-tuned multiplier or
// the spec default when zero.
func (s Settings) EffectiveNarrativeRetrievalWeight() float64 {
	if s.NarrativeRetrievalWeight <= 0 {
		return DefaultNarrativeRetrievalWeight
	}
	return s.NarrativeRetrievalWeight
}

// EffectiveNarrativePromotionThreshold returns the user-tuned threshold or
// the spec default when zero.
func (s Settings) EffectiveNarrativePromotionThreshold() int {
	if s.NarrativePromotionThreshold <= 0 {
		return DefaultNarrativePromotionThreshold
	}
	return s.NarrativePromotionThreshold
}

// EffectiveNarrativePromoterParallelism returns the worker count or the
// spec default when zero.
func (s Settings) EffectiveNarrativePromoterParallelism() int {
	if s.NarrativePromoterParallelism <= 0 {
		return DefaultNarrativePromoterParallelism
	}
	return s.NarrativePromoterParallelism
}

// EffectiveNarrativePreludeTopN returns the prelude size or the spec
// default when zero.
func (s Settings) EffectiveNarrativePreludeTopN() int {
	if s.NarrativePreludeTopN <= 0 {
		return DefaultNarrativePreludeTopN
	}
	return s.NarrativePreludeTopN
}

// ── Multimodal output accessors (multimodal-io-extended-01KQ8TD2 WP02) ─────

// DefaultMaxGeneratedImageBytes is the spec-locked per-image byte cap
// for the model-generated image auto-capture pipeline. 20 MiB is
// generous for all current image-generation APIs (DALL-E 3 max is
// ~4 MB; gpt-image-1 high-res is ~8 MB; Titan is similar).
const DefaultMaxGeneratedImageBytes int64 = 20 * 1024 * 1024 // 20 MiB

// AutoCaptureGeneratedImages reports whether the model-generated image
// auto-capture pipeline is active. Default true on a fresh install
// (zero-value AutoCaptureGeneratedImagesDisabled).
// (multimodal-io-extended-01KQ8TD2 WP02)
func (s Settings) AutoCaptureGeneratedImages() bool {
	return !s.AutoCaptureGeneratedImagesDisabled
}

// EffectiveMaxGeneratedImageBytes returns the user-tuned per-image byte
// cap or DefaultMaxGeneratedImageBytes when zero. Negative persisted
// values (from a hand-edited settings file) are treated as the default.
// (multimodal-io-extended-01KQ8TD2 WP02)
func (s Settings) EffectiveMaxGeneratedImageBytes() int64 {
	if s.MaxGeneratedImageBytes <= 0 {
		return DefaultMaxGeneratedImageBytes
	}
	return s.MaxGeneratedImageBytes
}

// EffectiveLocalRuntimeRAMBytes returns the RAM quantity available for
// local model loading in bytes. When LocalRuntimeRAMOverrideGB > 0, the
// override is used (converted from GiB to bytes); otherwise `detected`
// (from core/system/resources.EffectiveRAMBytes()) is returned.
//
// This helper is called by the model-fit filter (WP06) and by the
// frontend settings panel (WP07) to present a consistent effective value.
// (local-model-runtimes-01KQ8VMZ WP07)
func (s Settings) EffectiveLocalRuntimeRAMBytes(detected int64) int64 {
	if s.LocalRuntimeRAMOverrideGB > 0 {
		return int64(s.LocalRuntimeRAMOverrideGB * float64(1<<30))
	}
	return detected
}

// MultimodalInputEnabled reports whether the multimodal input feature
// (image + PDF attachments) is enabled for this user. Default true on a
// fresh install (zero-value MultimodalInputDisabled = false → feature on).
// When false, ChatInput.vue hides the paperclip button and drop overlay,
// and the paste handler ignores image/PDF clipboard items.
// The HARNESS_MULTIMODAL_IN env flag can force-disable this independently
// of the stored value — see the capabilities loader.
// (multimodal-io-01KQ8TDF WP08 / FR-023)
func (s Settings) MultimodalInputEnabled() bool { return !s.MultimodalInputDisabled }

// EffectiveAutoResumeOnKeyRotation reports whether the harness should
// automatically redrive the paused turn after the user rotates an API key.
// Default true on a fresh install (zero-value AutoResumeOnKeyRotationDisabled
// = false → feature enabled), matching the spec "default true" contract.
// The feature is additionally gated by the HARNESS_KEYCHAIN_ROTATION env
// flag — callers should also check keychainRotationFeatureEnabled().
// (provider-keychain-rotation-01KQ8TD9 WP07)
func (s Settings) EffectiveAutoResumeOnKeyRotation() bool {
	return !s.AutoResumeOnKeyRotationDisabled
}

// ShortcutsStore is the persistence interface for keyboard shortcut
// overrides. LoadShortcuts / SaveShortcuts are thin field-level accessors
// so callers can read/write just the shortcuts map without a full
// settings round-trip. Validation (validateShortcuts) still runs on every
// SaveShortcuts call via SaveAll under the hood.
// (keyboard-shortcuts-settings-01KQ8TDR plan §2.7)
type ShortcutsStore interface {
	// LoadShortcuts returns the current KeyboardShortcuts map.
	// Missing settings file → empty map (no error).
	LoadShortcuts() (map[string]string, error)
	// SaveShortcuts atomically replaces the full KeyboardShortcuts map.
	SaveShortcuts(m map[string]string) error
}
