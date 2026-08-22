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
	// CreatedBy is "user" or "model" (model-scheduled-jobs-01PMSJ01 WP09,
	// FR-005). Read-only: there is no corresponding field on
	// CreateInput/UpdateInput. Always "user" for every row created before
	// this WP's migration (sessions/0340) — the column default backfills
	// them, correctly, since Create was the only entry point that
	// existed.
	CreatedBy string `json:"createdBy"`
	// ToolAllowlist is the tool-name allowlist declared at creation time.
	// Empty means "no allowlist declared." Owner ruling B-3: a
	// CreatedBy=="model" row with an empty ToolAllowlist must never
	// execute (see core/policy/cedar's GateScheduledChatExecute).
	ToolAllowlist []string `json:"toolAllowlist,omitempty"`
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
//
// Deliberately absent: a "createdBy" field. FR-005 requires the
// provenance marker be "stamped server-side... never taken from caller
// input" — the enforcement mechanism IS the absence of the field, not a
// validated-and-ignored one. Create always stamps
// scheduler.ScheduledRunCreatedByUser; CreateAsModel (below) always
// stamps scheduler.ScheduledRunCreatedByModel. Neither reads a
// caller-supplied value because neither has one to read.
type CreateInput struct {
	Name           string `json:"name"`
	PromptTemplate string `json:"promptTemplate"`
	Cron           string `json:"cron"`
	Timezone       string `json:"timezone,omitempty"`
	Model          string `json:"model,omitempty"`
	OutputSink     string `json:"outputSink,omitempty"`
	Enabled        bool   `json:"enabled"`
	// ToolAllowlist declares the tool-name allowlist enforced against
	// this schedule's runs. Optional for a user-created schedule
	// (unrestricted). REQUIRED (non-empty) for CreateAsModel — see that
	// method's doc.
	ToolAllowlist []string `json:"toolAllowlist,omitempty"`
}

// UpdateInput is the wire shape for Update. ID is required.
type UpdateInput struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	PromptTemplate string   `json:"promptTemplate"`
	Cron           string   `json:"cron"`
	Timezone       string   `json:"timezone,omitempty"`
	Model          string   `json:"model,omitempty"`
	OutputSink     string   `json:"outputSink,omitempty"`
	Enabled        bool     `json:"enabled"`
	ToolAllowlist  []string `json:"toolAllowlist,omitempty"`
}

// Registrar is the cron-arming seam into a chat-run cron engine (mission
// model-scheduled-jobs-01PMSJ01, WP03). The concrete implementation is
// core/scheduler.ChatCronEngine; this interface exists so the view package
// does not import the engine's concrete type, and so tests can inject a
// stub without booting robfig/cron.
type Registrar interface {
	// Sync re-reads the row for id from the store and arms or disarms the
	// cron entry to match its current Enabled / Cron / Timezone fields.
	// Called after Create, Update and SetEnabled.
	Sync(ctx context.Context, id string) error
	// Unregister disarms the cron entry for id. Called after Delete, when
	// the row Sync would read no longer exists.
	Unregister(ctx context.Context, id string) error
}

// ScheduledChatAPI is the view-scoped accessor.
//
// A nil store is allowed — methods return ErrStoreUnavailable so the
// frontend can render an empty state without crashing.
type ScheduledChatAPI interface {
	// Create persists a new scheduled chat run, stamped
	// created_by="user" server-side. Returns ErrStoreUnavailable when no
	// store is wired.
	Create(ctx context.Context, in CreateInput) (ChatRunEntry, error)

	// CreateAsModel persists a new scheduled chat run stamped
	// created_by="model" server-side (FR-005, mission
	// model-scheduled-jobs-01PMSJ01 WP09/WP10). It requires
	// in.ToolAllowlist to be non-empty and returns ErrInvalidInput
	// otherwise — per owner ruling B-3 ("PERMIT ONLY WITHIN A TOOL
	// ALLOWLIST"), a model-created schedule with no declared allowlist
	// must never be creatable in a state that could later execute
	// unrestricted; core/policy/cedar's GateScheduledChatExecute
	// enforces the same rule again at fire time as defense in depth
	// (a row's allowlist can be emptied by a later Update).
	//
	// NOT REACHABLE from any production wiring as of WP09. The model-
	// facing entry point (harness_write_create_scheduled_run) is WP10,
	// which is HARD-BLOCKED on harness-self-attach-01PMHS01 WP04+WP06
	// per owner rulings B-2/B-3 (spec.md §9, §6.1). This method is the
	// mechanism WP10 wires a tool handler onto; until then it exists,
	// is tested, and has no caller outside this package's own tests —
	// a dated, named gap, not a silent one. See docs/unwired-ledger.md.
	CreateAsModel(ctx context.Context, in CreateInput) (ChatRunEntry, error)

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
