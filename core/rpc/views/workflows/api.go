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
}
