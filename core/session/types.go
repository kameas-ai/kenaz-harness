package session

import (
	"time"
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
}

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
type Message struct {
	ID        string
	SessionID string
	Sequence  int64
	Role      Role
	Content   string
	ToolCalls []ToolCall
	CreatedAt time.Time
}
