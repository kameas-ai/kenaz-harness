package session

import (
	"time"

	"github.com/kameas-ai/kenaz-harness/core/autonomy"
	"github.com/kameas-ai/kenaz-harness/core/llm"
)

// Record is the durable representation of a chat session as the rail UI
// renders it. It carries every field needed to recreate the rail's
// view of the session, including FR-002 per-session UI state (Draft,
// ScrollPosition) so switching sessions preserves where the user
// left off.
//
// The rpc-layer wire shape (rpcsessions.Session) is a strict subset of
// Record's fields; the projection lives in the rpc/views/sessions
// package so this package has no dependency on the rpc layer
// (DIRECTIVE_001 + import-cycle safety).
type Record struct {
	ID             string
	Name           string
	CreatedAt      time.Time
	UpdatedAt      time.Time
	LastActiveAt   time.Time
	Position       int64
	Draft          string
	ScrollPosition int64
	ArchivedAt     *time.Time
	// SystemPrompt is optional starting context attached to the session.
	// When ContextKind == ContextKindSystem the manager prepends it as a
	// system role message at every send; ContextKindUserSeed leaves the
	// content empty here and persists it as the first user turn instead
	// (so the user sees it in the transcript).
	SystemPrompt string
	ContextKind  string
	// ProjectID groups the session under a project; nil means "loose"
	// (no project). The pointer matches the SQL column's nullability so
	// readers can distinguish "no project" from "project with empty id".
	ProjectID *string
	// AutoTitled is true when the auto-titling engine has written a
	// generated title to this session, or when a user has manually
	// renamed it (locking out further auto-titling).
	// Populated by migration 0311 (session-auto-titling-01KQ8TDS WP01).
	AutoTitled bool

	// Kind tags the session for capability gating. Known values:
	//   "chat"       — default user session.
	//   "onboarding" — spawned by the onboarding flow; harness-self write
	//                  tools are permitted by default policy.
	//   "system"     — reserved for future internal sessions.
	// Empty string is normalised to "chat" by the manager / store.
	// Populated by migration 0318 (harness-self-mcp-onboarding-01KQ8TDU WP03).
	Kind string

	// BranchAdvisorDismissed is set when the user clicks "Don't suggest
	// again" in the branch-advisor banner (FR-010). When true, the
	// backend skips running the detector for this session regardless of
	// the project-level setting (resolution order step 4). Persisted
	// in the sessions table via migration 0311.
	BranchAdvisorDismissed bool

	// AutonomyLevel is this session's tier override (autonomy-dial-01KR3M2A
	// WP02). nil means "inherit from the upstream layer" (project →
	// global → tier-default). Persisted in sessions.autonomy_level via
	// migration 0316. The autonomy.Layer for the session is reconstructed
	// from (AutonomyLevel, AutonomyOverrides) by the autonomy resolver.
	AutonomyLevel *autonomy.Tier `json:"autonomyLevel,omitempty"`
	// AutonomyOverrides are the per-knob overrides this session pinned.
	// Empty / nil means "no overrides at this layer." Persisted as a
	// JSON blob in sessions.autonomy_overrides via migration 0316. The
	// map key is the canonical autonomy.Knob name (matches the wire
	// shape produced by autonomy.Layer.MarshalJSON).
	AutonomyOverrides map[autonomy.Knob]any `json:"autonomyOverrides,omitempty"`

	// MoveHistoryMode records which model-visible-history mode this
	// session was CREATED under — "moves" or "classic"
	// (model-moves-transcript-01PMCH01 WP03, spec §4: "sessions record
	// which mode wrote them"). Persisted in sessions.move_history_mode
	// via migration 0334.
	//
	// EMPTY IS LOAD-BEARING: it is every session that predates the
	// migration, and it resolves fail-closed to the classic composition
	// so an in-flight conversation never changes shape underneath the
	// model mid-thread. Read it through MoveHistoryModeFromRecord
	// (moves_fidelity.go), never by comparing the string here — that
	// function is the single place that decides what the values mean.
	//
	// Stamped once at creation and never rewritten: the live dial is a
	// separate, read-at-consumption input, and this one is the memory of
	// how the transcript was actually written.
	MoveHistoryMode string `json:"moveHistoryMode,omitempty"`
}

// LastUsage is the per-session token-usage snapshot persisted after each
// completed LLM turn (backend-context-window-length-01KQ8TD3 WP02). The
// frontend reads this snapshot via the session.usage.updated broker event
// (WP03) to update the context-window indicator in near-real-time.
//
// CostSource mirrors the token-cost-telemetry taxonomy:
// "provider" | "derived" | "mixed" | "unknown".
type LastUsage struct {
	PromptTokens     int     `json:"promptTokens"`
	CompletionTokens int     `json:"completionTokens"`
	TotalTokens      int     `json:"totalTokens"`
	CostUSD          float64 `json:"costUsd"`
	CostSource       string  `json:"costSource"`
}

// ContextKind values for Record.ContextKind. Validated at the manager
// boundary so callers cannot persist unknown values.
const (
	ContextKindSystem   = "system"
	ContextKindUserSeed = "user_seed"
)

// Role enumerates the speaker of a Message.
type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleSystem    Role = "system"
	RoleTool      Role = "tool"
)

// ToolCall captures a tool invocation tied to an assistant turn. Stored
// as JSON in the session_messages.tool_calls column.
type ToolCall struct {
	ID        string         `json:"id"`
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments,omitempty"`
	Result    string         `json:"result,omitempty"`
	// UsedSecrets is true when the dispatch resolved at least one
	// @secret: reference token in the tool arguments. Never carries
	// plaintext — provenance only (model-secret-references-01KW7M5A WP14).
	UsedSecrets bool `json:"usedSecrets,omitempty"`
	// IsError marks a tool_result move whose call failed
	// (model-moves-transcript-01PMCH01 WP04). It is what makes a reloaded
	// chat chip show the same failed tool the live view showed, instead
	// of degrading every resolved call to "ok".
	//
	// It needs no migration: session_messages.tool_calls stores this
	// struct as JSON, so the field round-trips through the existing
	// column and rows written before the mission simply omit it.
	IsError bool `json:"isError,omitempty"`
}

// Message is one row in session_messages. Sequence is monotonically
// increasing within a session and dense.
//
// Persistence shape (post multimodal-io WP02):
//
//   - Content is the legacy plain-text column. Writers populate it from
//     ContentBlocks via the corellm.Message.Text() flattener so legacy
//     readers (any caller still on the pre-WP01 Content-as-string
//     contract) keep working for one release.
//   - ContentBlocks carries the canonical post-WP01 polymorphic
//     []ContentBlock shape and is JSON-serialized into the
//     session_messages.content_json column. Readers prefer
//     content_json when non-null; legacy rows synthesize a single
//     {Type:"text", Text:Content} block on read.
//
// AppendMessage callers populating only Content (string) continue to
// round-trip; the store synthesizes a single text block into
// ContentBlocks at persist time.
type Message struct {
	ID            string
	SessionID     string
	Sequence      int64
	Role          Role
	Content       string
	ContentBlocks []llm.ContentBlock
	ToolCalls     []ToolCall
	CreatedAt     time.Time
	// CompactedIntoID is the id of the synthetic summary row that
	// folded this message in. NULL on rows the compaction engine never
	// touched. Populated by migration 0310 (compaction-strategy-ui WP01).
	CompactedIntoID *string
	// CompactedAt is the unix-nanos moment the compaction engine wrote
	// the summary row that replaces this message. NULL on rows the
	// engine never touched. On the synthetic summary row itself,
	// CompactedAt is non-nil and CompactedIntoID is nil.
	CompactedAt *time.Time
	// ArchivedAt is the unix-nanos moment the compaction engine flagged
	// this row as archived (i.e. folded into a summary). NULL on live
	// rows; non-NULL rows are excluded from the default scrollback fetch
	// (Sessions.ListMessagesActive).
	ArchivedAt *time.Time

	// StreamingFailedAt is the unix-nanos moment the chat runner decided
	// the LLM stream was lost mid-turn and persisted the partial assistant
	// row (long-turn-resilience-01KR3PRS WP03). NULL on every healthy
	// assistant row. Populated by migration 0317.
	StreamingFailedAt *time.Time
	// StreamingFailureKind is the classification of the drop:
	// "transient" (network blip / 5xx after retry exhaustion), "auth"
	// (401/403 mid-flight), or "unknown" (everything else). Empty on
	// healthy rows.
	StreamingFailureKind string
	// StreamingRecoverable is true when no tool_use block executed before
	// the drop, so a continuation prompt is safe to issue. False when
	// tool calls already ran — the user must re-issue the request.
	// Carries no meaning when StreamingFailedAt is nil.
	StreamingRecoverable bool
	// ContinuationOf is the id of the partial assistant row that this row
	// continues. Empty on every original assistant row; populated only on
	// the continuation row written by Sessions_ResumeMessage so the
	// frontend can grey out the original and stitch the bubbles together.
	ContinuationOf string

	// Per-message token usage (per-message-token-meter-01KR3PQR).
	// Populated by the token-cost-telemetry migration (session_messages
	// columns: prompt_tokens, completion_tokens, cost_usd, cost_source).
	// NULL on rows that pre-date the migration or had no usage captured.
	PromptTokens     *int
	CompletionTokens *int
	CostUSD          *float64
	// CostSource mirrors the token-cost-telemetry taxonomy:
	// "provider" | "derived" | "mixed" | "unknown". Empty on rows
	// with no usage data.
	MessageCostSource string

	// ActualProvider is the provider that ultimately served this turn. When
	// empty, the primary (ProfileID-bound) provider was used. When non-empty,
	// a fallback chain hop re-routed the turn to this provider
	// (model-fallback-routing-01NDFSEX04 WP04). Stored in memory; not
	// persisted to SQL (no schema migration needed for v0.15.x).
	ActualProvider string
	// ActualModel is the model that ultimately served this turn. When empty,
	// the primary model from the provider profile was used.
	ActualModel string

	// ---- move metadata (model-moves-transcript-01PMCH01 WP01) ----
	//
	// These three are UNEXPORTED BY DESIGN. A human turn can drive many
	// model iterations; each iteration's output, tool call and tool
	// result is its own transcript entry, tagged with which turn it
	// belongs to and where in that turn it sits. Only
	// Manager.AppendTranscriptEntry may stamp them (moves.go), so the
	// compiler — not a convention — enforces the single-writer rule from
	// spec §4 ("no second history-writing path"). Read them through
	// MoveKind() / MoveIndex() / TurnSpanID().
	//
	// A row with moveKind == "" is a classic pre-moves entry and MUST
	// behave exactly as it did before this mission: the wire projection
	// omits all three, so old clients and old sessions see the shape
	// they already understand.
	moveKind       MoveKind
	moveIndex      *int
	moveTurnSpanID string

	// modelToolArgs is the MODEL LAYER's copy of a tool_call move's raw
	// arguments, keyed by tool-call id, each value the JSON the model
	// emitted (model-moves-transcript-01PMCH01 WP03, spec §4).
	//
	// UNEXPORTED for the same reason as the three above, and read
	// through ModelLayerToolArgs(). It is NOT interchangeable with
	// ToolCalls[].Arguments, which is the DISPLAY layer's field and
	// stays nil forever: see the header of migrations_move_fidelity.go
	// for why the two layers get two homes, and moves.go for the two
	// deliberately-unalike helper names.
	//
	// nil on every classic entry and on every move that is not a
	// tool_call.
	modelToolArgs map[string]string
}
