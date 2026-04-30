package session

import (
	"time"

	"github.com/sigil-tech/kaneaz-harness/core/llm"
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
}
