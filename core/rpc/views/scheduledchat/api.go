// Package scheduledchat is the view-scoped RPC accessor for the
// scheduled-chat-runs feature (mission scheduled-chat-runs-01KX5R8B,
// v0.10.0).
//
// The frontend ScheduledChatsPanel reads the catalog through List/Get
// and creates/edits via Create/Update. RunNow dispatches an immediate
// off-schedule run; History returns recent run summaries for the inbox.
//
// This surface is intentionally separate from the Workflows surface
// (core/rpc/views/workflows) so each can evolve without cross-mission
// coupling.
package scheduledchat

import (
	"context"
	"time"
)

// ChatRunEntry is the full wire shape for one scheduled chat run.
type ChatRunEntry struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	PromptTemplate string `json:"promptTemplate"`
	Cron           string `json:"cron"`
	Timezone       string `json:"timezone,omitempty"`
	Model          string `json:"model,omitempty"`
	OutputSink     string `json:"outputSink"`
	Enabled        bool   `json:"enabled"`
	CreatedAt      string `json:"createdAt"` // ISO 8601
	UpdatedAt      string `json:"updatedAt"` // ISO 8601
}

// RunSummary is one row in the History result.
type RunSummary struct {
	ID            string     `json:"id"`
	ChatRunID     string     `json:"chatRunId"`
	SessionID     string     `json:"sessionId,omitempty"`
	Status        string     `json:"status"` // completed | failed | running
	StartedAt     time.Time  `json:"startedAt"`
	EndedAt       *time.Time `json:"endedAt,omitempty"`
	OutputSnippet string     `json:"outputSnippet,omitempty"`
	Error         string     `json:"error,omitempty"`
}

// CreateInput is the wire shape for Create.
type CreateInput struct {
	Name           string `json:"name"`
	PromptTemplate string `json:"promptTemplate"`
	Cron           string `json:"cron"`
	Timezone       string `json:"timezone,omitempty"`
	Model          string `json:"model,omitempty"`
	OutputSink     string `json:"outputSink,omitempty"`
	Enabled        bool   `json:"enabled"`
}

// UpdateInput is the wire shape for Update. ID is required.
type UpdateInput struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	PromptTemplate string `json:"promptTemplate"`
	Cron           string `json:"cron"`
	Timezone       string `json:"timezone,omitempty"`
	Model          string `json:"model,omitempty"`
	OutputSink     string `json:"outputSink,omitempty"`
	Enabled        bool   `json:"enabled"`
}

// ScheduledChatAPI is the view-scoped accessor.
//
// A nil store is allowed — methods return ErrStoreUnavailable so the
// frontend can render an empty state without crashing.
type ScheduledChatAPI interface {
	// Create persists a new scheduled chat run.
	// Returns ErrStoreUnavailable when no store is wired.
	Create(ctx context.Context, in CreateInput) (ChatRunEntry, error)

	// Update replaces all mutable fields of an existing scheduled chat run.
	// Returns ErrNotFound when no run with in.ID exists.
	Update(ctx context.Context, in UpdateInput) (ChatRunEntry, error)

	// Delete removes a scheduled chat run by id. Returns nil if the id
	// is unknown (idempotent).
	Delete(ctx context.Context, id string) error

	// List returns all scheduled chat runs ordered by creation time.
	List(ctx context.Context) ([]ChatRunEntry, error)

	// Get returns the full entry for id.
	// Returns ErrNotFound when no run with id exists.
	Get(ctx context.Context, id string) (ChatRunEntry, error)

	// RunNow dispatches an immediate off-schedule run.
	// Returns ErrStoreUnavailable when no store is wired.
	RunNow(ctx context.Context, id string) (RunSummary, error)

	// History returns up to limit recent RunSummary records for id in
	// reverse-chronological order.
	History(ctx context.Context, id string, limit int) ([]RunSummary, error)

	// SetEnabled flips the enabled flag for id.
	// Returns ErrNotFound when no run with id exists.
	SetEnabled(ctx context.Context, id string, enabled bool) error
}
