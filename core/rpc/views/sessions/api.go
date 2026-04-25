// Package sessions defines the SessionsAPI view-scoped accessor on the
// HarnessAPI surface. Frontend mission delivers the interface shape; the
// concrete implementation is wired by the sessions feature mission.
package sessions

import "context"

// Session is the lightweight session metadata the rail consumes.
type Session struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
}

// ToolCall mirrors the frontend ToolCall shape for tool-use rendering.
type ToolCall struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	ArgsSummary string `json:"argsSummary"`
	Latency     string `json:"latency,omitempty"`
}

// Message is a single chat-message entry. NEVER carries credential fields.
type Message struct {
	ID        string     `json:"id"`
	SessionID string     `json:"sessionId"`
	Role      string     `json:"role"`
	Content   string     `json:"content"`
	CreatedAt string     `json:"createdAt"`
	Streaming bool       `json:"streaming,omitempty"`
	ToolCalls []ToolCall `json:"toolCalls,omitempty"`
}

// SessionsAPI is the view-scoped accessor for session CRUD + streams.
// Implementations MUST be safe for concurrent use.
type SessionsAPI interface {
	List(ctx context.Context) ([]Session, error)
	Get(ctx context.Context, id string) (Session, error)
	Create(ctx context.Context, name string) (Session, error)
	Rename(ctx context.Context, id, name string) error
	Delete(ctx context.Context, id string) error
	Reorder(ctx context.Context, ids []string) error
	StartStream(ctx context.Context, id string) (subscriptionID string, err error)
	StopStream(ctx context.Context, subscriptionID string) error

	// Chat-message surface (frontend-foundations chat-ui mission).
	ListMessages(ctx context.Context, id string) ([]Message, error)
	AppendMessage(ctx context.Context, id, role, content string) (Message, error)
	SaveDraft(ctx context.Context, id, draft string) error
	LoadDraft(ctx context.Context, id string) (string, error)
}
