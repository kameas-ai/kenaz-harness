// Package workflows is the view-scoped RPC accessor for the agentic
// workflows subsystem (mission workflows-01KQ8TDG, v0.3.0 beta).
//
// The frontend WorkflowsView reads the catalog through List/Get and
// invokes a workflow via Run. RunResult carries the synchronous
// outcome plus a slice of StepRun records the UI uses to render the
// per-step transcript. Streaming progress is published on the
// `workflows:run-progress` broker topic; the wire shape there mirrors
// ProgressEvent on this surface.
package workflows

import (
	"context"
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
}
