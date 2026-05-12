// Package workflows is the view-scoped RPC accessor for the agentic
// workflows subsystem (mission workflows-01KQ8TDG, v0.3.0 beta).
//
// The frontend WorkflowsView reads the catalog through List/Get and
// invokes a workflow via Run. RunResult carries the synchronous
// outcome plus a slice of StepRun records the UI uses to render the
// per-step transcript. Streaming progress is published on the
// `workflows:run-progress` broker topic; the wire shape there mirrors
// ProgressEvent on this surface.
//
// WP02 (workflows-agentic-01KW2D3X) adds scheduler methods:
// ScheduleSet, ScheduleClear, ScheduleList, and RunNow.
package workflows

import (
	"context"
	"time"
)

// Summary is the lightweight catalog entry returned by List.
type Summary struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Version     int    `json:"version"`
	StepCount   int    `json:"stepCount"`
	Source      string `json:"source"` // "builtin" | "user" | "project"
}

// Input mirrors core/workflows.Input on the wire.
type Input struct {
	Name     string   `json:"name"`
	Kind     string   `json:"kind"`
	Required bool     `json:"required,omitempty"`
	Default  string   `json:"default,omitempty"`
	Options  []string `json:"options,omitempty"`
}

// Step mirrors core/workflows.Step on the wire (subset; beta needs
// only the rendered fields).
type Step struct {
	Name       string   `json:"name"`
	Kind       string   `json:"kind"`
	UserPrompt string   `json:"userPrompt,omitempty"`
	Cmd        string   `json:"cmd,omitempty"`
	Args       []string `json:"args,omitempty"`
}

// Workflow is the full wire shape returned by Get.
type Workflow struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Description string  `json:"description,omitempty"`
	Version     int     `json:"version"`
	Inputs      []Input `json:"inputs,omitempty"`
	Steps       []Step  `json:"steps"`
}

// StepRun is one row in a RunResult's transcript.
type StepRun struct {
	Name   string `json:"name"`
	Kind   string `json:"kind"`
	Status string `json:"status"`
	Output string `json:"output,omitempty"`
	Err    string `json:"error,omitempty"`
}

// RunResult is the synchronous result of a Run invocation.
type RunResult struct {
	RunID      string    `json:"runId"`
	WorkflowID string    `json:"workflowId"`
	Status     string    `json:"status"`
	Steps      []StepRun `json:"steps"`
	Err        string    `json:"error,omitempty"`
}

// SaveInput is the wire shape for Save. Exactly one of YAML or
// Workflow must be populated. YAML wins when both are set so the
// import-from-clipboard path keeps its byte-for-byte fidelity.
//
// When YAML is non-empty it is routed through ImportYAML so the
// resulting workflow row gets a fresh id (the share/import safety net
// from the storage layer). When Workflow is set the caller is
// updating a known id and the storage layer hash-dedupes idempotent
// re-saves.
type SaveInput struct {
	YAML     string    `json:"yaml,omitempty"`
	Workflow *Workflow `json:"workflow,omitempty"`
}

// SaveOutput mirrors the persisted record on the wire so the UI can
// pick up the freshly assigned version + timestamps without a
// follow-up Get.
type SaveOutput struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Version   int    `json:"version"`
	Hash      string `json:"hash"`
	YAML      string `json:"yaml"`
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
}

// RunRequest is the WP08 envelope shape callers pass to RunWithOptions
// when they need to opt into the inline-dispatch path or override the
// rerun_policy confirmation gate. The legacy positional Run(id, inputs)
// signature is preserved for back-compat.
type RunRequest struct {
	ID     string            `json:"id"`
	Inputs map[string]string `json:"inputs,omitempty"`
	// Inline, when true, routes through workflows.InlineRun instead
	// of the spawned-session path. The progress events flow on the
	// same broker topic but each carries Inline=true so the chat
	// renderer can append them to the current session transcript
	// rather than spawning a new workflow_run row.
	Inline bool `json:"inline,omitempty"`
	// SkipCache, when true, bypasses the rerun_policy cache check.
	// Used after the user confirms a "rerun_policy=prompt" gate so
	// the second invocation dispatches fresh.
	SkipCache bool `json:"skipCache,omitempty"`
}

// ScheduleSetInput is the wire shape for ScheduleSet.
type ScheduleSetInput struct {
	WorkflowID string `json:"workflowId"`
	// Cron is a standard 5-field cron expression (minute hour dom month dow).
	Cron string `json:"cron"`
	// Timezone is an IANA timezone name (e.g. "America/New_York").
	// Empty falls back to UTC.
	Timezone string `json:"timezone,omitempty"`
}

// ScheduleEntry is the wire shape returned by ScheduleList.
type ScheduleEntry struct {
	WorkflowID string `json:"workflowId"`
	Cron       string `json:"cron"`
	Timezone   string `json:"timezone,omitempty"`
	Enabled    bool   `json:"enabled"`
}

// RunSummary is returned by RunNow and embedded in ScheduleHistory.
type RunSummary struct {
	RunID      string    `json:"runId"`
	WorkflowID string    `json:"workflowId"`
	Status     string    `json:"status"` // completed | failed | running
	StartedAt  time.Time `json:"startedAt"`
	EndedAt    time.Time `json:"endedAt,omitempty"`
	Err        string    `json:"error,omitempty"`
	Scheduled  bool      `json:"scheduled"`
}

// --- Catalog types (workflows-agentic-01KW2D3X WP03) ---

// CatalogEntry is the wire shape for one card in the catalog grid.
type CatalogEntry struct {
	ID                  string   `json:"id"`
	Name                string   `json:"name"`
	Description         string   `json:"description,omitempty"`
	Source              string   `json:"source"` // "builtin" | "user"
	Version             string   `json:"version"`
	Icon                string   `json:"icon,omitempty"`
	RequiresCedarGrants []string `json:"requiresCedarGrants,omitempty"`
	RequiresCredentials []string `json:"requiresCredentials,omitempty"`
	EstimatedCostUSD    float64  `json:"estimatedCostUSD"`
	InstallStatus       string   `json:"installStatus"`
}

// CatalogPreview is the full payload returned by Catalog_Get.
type CatalogPreview struct {
	Entry      CatalogEntry `json:"entry"`
	YAMLSource string       `json:"yamlSource"`
}

// CatalogInstallResult is the result of Catalog_Install.
type CatalogInstallResult struct {
	WorkflowID         string   `json:"workflowId"`
	Scheduled          bool     `json:"scheduled"`
	MissingCredentials []string `json:"missingCredentials,omitempty"`
}

// WorkflowsAPI is the view-scoped accessor.
//
// A nil engine is allowed — methods return ErrEngineUnavailable so
// the frontend can render an empty state without the chassis crashing.
type WorkflowsAPI interface {
	// List returns every workflow the engine has loaded. Beta scope
	// includes only the bundled builtins.
	List(ctx context.Context) ([]Summary, error)
	// Get returns the full Workflow shape (inputs + steps) for id.
	Get(ctx context.Context, id string) (Workflow, error)
	// Run executes the workflow synchronously and returns the full
	// transcript. Long-running steps in production WPs will move to
	// async + broker progress; the beta engine completes inline.
	Run(ctx context.Context, id string, inputs map[string]string) (RunResult, error)
	// RunWithOptions is the WP08 entrypoint that honours the inline
	// + skip-cache flags. The legacy Run is wired as a thin shim.
	RunWithOptions(ctx context.Context, req RunRequest) (RunResult, error)
	// Save persists a user workflow via the WP06 storage layer.
	// Returns ErrStorageUnavailable when no Store is wired.
	// Save is idempotent on yaml hash: re-saving identical canonical
	// YAML returns the unchanged stored record.
	Save(ctx context.Context, in SaveInput) (SaveOutput, error)
	// Delete removes a stored workflow by id. Returns
	// corewf.ErrWorkflowNotFound when the id is unknown and
	// ErrStorageUnavailable when no Store is wired.
	Delete(ctx context.Context, id string) error

	// --- Scheduler methods (workflows-agentic-01KW2D3X WP02) ---

	// ScheduleSet registers or replaces a cron schedule for the
	// workflow identified by in.WorkflowID. The schedule is persisted
	// in DB so it survives chassis restarts. Returns
	// ErrSchedulerUnavailable when no scheduler is wired.
	ScheduleSet(ctx context.Context, in ScheduleSetInput) error
	// ScheduleClear removes the cron schedule for the given workflow.
	// Returns nil if no schedule was registered.
	ScheduleClear(ctx context.Context, workflowID string) error
	// ScheduleList returns all registered schedules.
	ScheduleList(ctx context.Context) ([]ScheduleEntry, error)
	// RunNow dispatches an immediate off-schedule run. Does not
	// require an active cron entry. Returns ErrSchedulerUnavailable
	// when no scheduler is wired.
	RunNow(ctx context.Context, workflowID string) (RunSummary, error)

	// --- Scheduled-inbox methods (workflow-extensions-01KW2D3Y WP01) ---

	// ScheduleRunHistory returns up to limit recent run summaries for
	// workflowID in reverse-chronological order. Returns
	// ErrSchedulerUnavailable when no scheduler is wired.
	ScheduleRunHistory(ctx context.Context, workflowID string, limit int) ([]RunSummary, error)

	// ScheduleNextFire returns the next scheduled fire time for
	// workflowID as a UTC time.Time. A zero Time is returned when no
	// cron schedule is registered for the workflow. Returns
	// ErrSchedulerUnavailable when no scheduler is wired.
	ScheduleNextFire(ctx context.Context, workflowID string) (time.Time, error)

	// CancelRun signals cancellation of an in-flight run identified by
	// runID. Returns ErrEngineUnavailable when no engine is wired and
	// corewf.ErrRunNotFound when the run ID is unknown on this engine.
	CancelRun(ctx context.Context, runID string) error

	// --- Catalog methods (workflows-agentic-01KW2D3X WP03) ---

	// Catalog_List returns every Entry the workflow catalog exposes,
	// with per-entry InstallStatus reflecting the current storage state.
	Catalog_List(ctx context.Context) ([]CatalogEntry, error)

	// Catalog_Get returns the full YAML + Entry metadata for id.
	// Returns ErrCatalogUnavailable when no catalog is wired or
	// corewf.ErrWorkflowNotFound when the id is unknown.
	Catalog_Get(ctx context.Context, id string) (CatalogPreview, error)

	// Catalog_Install copies the workflow from the catalog to user
	// storage, optionally arms a cron schedule, and returns a ref
	// describing what happened. On missing credentials the install
	// still succeeds — callers inspect MissingCredentials and may
	// redirect to /providers/add.
	Catalog_Install(ctx context.Context, id string) (CatalogInstallResult, error)
}
