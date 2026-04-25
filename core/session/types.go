package session

import (
	"time"

	rpcsessions "github.com/sigil-tech/kaneaz-harness/core/rpc/views/sessions"
)

// Record is the durable representation of a chat session as the rail UI
// renders it. It is a superset of the lightweight rpcsessions.Session
// that crosses the Wails boundary; ToView projects to the wire shape.
//
// Per FR-002 the Record carries per-session UI state (Draft,
// ScrollPosition) so switching sessions preserves where the user left
// off.
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

// ToView projects a Record into the rpc-layer shape the frontend
// consumes. Timestamps render as RFC3339 UTC for byte-stable JSON.
func (r Record) ToView() rpcsessions.Session {
	return rpcsessions.Session{
		ID:        r.ID,
		Name:      r.Name,
		CreatedAt: r.CreatedAt.UTC().Format(time.RFC3339Nano),
		UpdatedAt: r.UpdatedAt.UTC().Format(time.RFC3339Nano),
	}
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
