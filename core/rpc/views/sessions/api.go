// Package sessions defines the SessionsAPI view-scoped accessor on the
// HarnessAPI surface. Frontend mission delivers the interface shape; the
// concrete implementation is wired by the sessions feature mission.
package sessions

import (
	"context"

	"github.com/sigil-tech/kaneaz-harness/core/llm"
)

// ContentBlock mirrors core/llm.ContentBlock on the rpc wire so the
// frontend (multimodal-io WP04) can hand assembled image / document
// blocks to SendMessageWithBlocks without spelling out the connector
// shape itself. Field names match corellm.ContentBlock exactly.
type ContentBlock = llm.ContentBlock

// Session is the lightweight session metadata the rail consumes.
type Session struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
	// SystemPrompt + ContextKind surface the per-session starting
	// context (Mission A). Empty SystemPrompt + ContextKind="system"
	// is the default for sessions created before the feature landed.
	SystemPrompt string `json:"systemPrompt"`
	ContextKind  string `json:"contextKind"`
	// ProjectID is the session's project membership; empty string for
	// loose sessions. Mirrors session.Record.ProjectID.
	ProjectID string `json:"projectId,omitempty"`
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

// DeleteOptions configures the artifacts-storage cascade extension
// (FR-014). The zero value is the default behaviour: artifacts get
// deleted alongside the session, no promotion to project scope.
type DeleteOptions struct {
	// DeleteArtifacts controls whether the session's artifacts are
	// removed during the cascade. Default true (zero-value Preserve
	// inverts to Delete=true — see DeleteArtifactsCascade). Setting
	// PreserveArtifacts=true forces the caller to either promote the
	// artifacts to the session's project (PromoteArtifactsToProject)
	// or accept the ErrSessionHasArtifacts error.
	PreserveArtifacts bool `json:"preserveArtifacts,omitempty"`
	// PromoteArtifactsToProject is meaningful only when
	// PreserveArtifacts is true AND the session has a project. When
	// set, every artifact is moved to project scope before the
	// session row is deleted.
	PromoteArtifactsToProject bool `json:"promoteArtifactsToProject,omitempty"`
}

// DeleteArtifactsCascade returns the user-facing flag that drives
// session-delete cascade. Inverts the persisted PreserveArtifacts so
// the zero value means "delete artifacts alongside the session" (the
// FR-014 default).
func (o DeleteOptions) DeleteArtifactsCascade() bool { return !o.PreserveArtifacts }

// SessionsAPI is the view-scoped accessor for session CRUD + streams.
// Implementations MUST be safe for concurrent use.
type SessionsAPI interface {
	List(ctx context.Context) ([]Session, error)
	Get(ctx context.Context, id string) (Session, error)
	Create(ctx context.Context, name string) (Session, error)
	Rename(ctx context.Context, id, name string) error
	Delete(ctx context.Context, id string) error
	// DeleteWithOptions is the FR-014 variant: opts drive the
	// artifacts cascade (delete vs preserve, promote-to-project on
	// preserve). The bare Delete keeps the pre-WP02 contract for
	// callers that don't yet thread options.
	DeleteWithOptions(ctx context.Context, id string, opts DeleteOptions) error
	Reorder(ctx context.Context, ids []string) error
	StartStream(ctx context.Context, id string) (subscriptionID string, err error)
	StopStream(ctx context.Context, subscriptionID string) error

	// Chat-message surface (frontend-foundations chat-ui mission).
	ListMessages(ctx context.Context, id string) ([]Message, error)
	AppendMessage(ctx context.Context, id, role, content string) (Message, error)
	// SendMessageWithBlocks persists a user turn carrying polymorphic
	// content blocks (text + image + document). The legacy text-only
	// AppendMessage path is left untouched for callers that don't need
	// multimodal input. contentBlocks must be non-empty (multimodal-io
	// WP03 / FR-013).
	SendMessageWithBlocks(ctx context.Context, id string, contentBlocks []ContentBlock) (Message, error)
	SaveDraft(ctx context.Context, id, draft string) error
	LoadDraft(ctx context.Context, id string) (string, error)

	// SetSystemPrompt persists per-session starting context. kind is
	// 'system' (invisible, prepended on every send) or 'user_seed'
	// (visible — the caller is responsible for also appending the
	// content as a user message via AppendMessage).
	SetSystemPrompt(ctx context.Context, id, content, kind string) error

	// MoveToProject sets the session's project membership. An empty
	// projectID detaches the session and makes it loose.
	MoveToProject(ctx context.Context, id, projectID string) error
}
