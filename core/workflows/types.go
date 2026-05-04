// Package workflows is the v0.3.0-beta scaffold for the agentic
// workflows subsystem (mission workflows-01KQ8TDG).
//
// The full plan describes 10+ step kinds with Cedar gates, audit
// emission, history pruning, and a YAML editor. The beta scope
// shipped here is intentionally minimal:
//
//   - Workflow / Step / Input value types (WP01).
//   - YAML loader + per-field validator (WP01).
//   - Sequential Engine that resolves ${input.x} / ${step.x.output}
//     and runs each step against a kind-keyed StepRunner registry
//     (WP02).
//   - Two built-in StepRunner kinds — `model_turn` and `shell` — that
//     are stubbed to return text outputs without dispatching against
//     the real LLM/tool stack. The real dispatch lands in WP03+.
//   - One bundled example workflow YAML so the frontend has something
//     to list and run end-to-end.
//
// The package-level API is deliberately small. Future WPs add Step
// kinds by registering with RegisterStepRunner, and add Cedar /
// audit hooks via Engine fields without breaking the public surface.
package workflows

import (
	"context"
	"errors"
	"time"
)

// ValueType is the closed enum of TypedValue payload shapes.
type ValueType string

const (
	ValueTypeText       ValueType = "text"
	ValueTypeJSON       ValueType = "json"
	ValueTypeBytes      ValueType = "bytes"
	ValueTypeArtifactID ValueType = "artifact_id"
	ValueTypeError      ValueType = "error"
)

// TypedValue is the in-flight representation of a step input or
// output. Exactly one of the payload fields is meaningful for a given
// Type.
type TypedValue struct {
	Type       ValueType `json:"type"`
	Text       string    `json:"text,omitempty"`
	JSON       any       `json:"json,omitempty"`
	Bytes      []byte    `json:"bytes,omitempty"`
	ArtifactID string    `json:"artifactId,omitempty"`
}

// StepKind is the discriminator on Step.Kind. The full plan defines
// 10 kinds; the beta scaffold validates the closed set but only
// dispatches model_turn + shell at runtime.
type StepKind string

const (
	StepKindModelTurn     StepKind = "model_turn"
	StepKindToolCall      StepKind = "tool_call"
	StepKindMCPCall       StepKind = "mcp_call"
	StepKindHTTPRequest   StepKind = "http_request"
	StepKindShell         StepKind = "shell"
	StepKindReadArtifact  StepKind = "read_artifact"
	StepKindWriteArtifact StepKind = "write_artifact"
	StepKindTransform     StepKind = "transform"
	StepKindConditional   StepKind = "conditional"
)

// AllStepKinds is the closed enum the loader validates against.
func AllStepKinds() []StepKind {
	return []StepKind{
		StepKindModelTurn, StepKindToolCall, StepKindMCPCall,
		StepKindHTTPRequest, StepKindShell, StepKindReadArtifact,
		StepKindWriteArtifact, StepKindTransform, StepKindConditional,
	}
}

// InputKind is the closed enum on Workflow.Inputs[].Kind.
type InputKind string

const (
	InputKindString      InputKind = "string"
	InputKindMultiline   InputKind = "multiline"
	InputKindEnum        InputKind = "enum"
	InputKindFile        InputKind = "file"
	InputKindArtifactRef InputKind = "artifact_ref"
	InputKindProjectRef  InputKind = "project_ref"
)

// Input declares a typed parameter on a workflow.
type Input struct {
	Name     string    `yaml:"name" json:"name"`
	Kind     InputKind `yaml:"kind" json:"kind"`
	Required bool      `yaml:"required,omitempty" json:"required,omitempty"`
	Default  string    `yaml:"default,omitempty" json:"default,omitempty"`
	Options  []string  `yaml:"options,omitempty" json:"options,omitempty"`
}

// Step is a single executable node in a workflow.
type Step struct {
	Name string   `yaml:"name" json:"name"`
	Kind StepKind `yaml:"kind" json:"kind"`

	// model_turn fields
	UserPrompt string   `yaml:"user_prompt,omitempty" json:"userPrompt,omitempty"`
	AllowTools []string `yaml:"allow_tools,omitempty" json:"allowTools,omitempty"`

	// shell / tool_call fields
	Cmd       string            `yaml:"cmd,omitempty" json:"cmd,omitempty"`
	Args      []string          `yaml:"args,omitempty" json:"args,omitempty"`
	ToolName  string            `yaml:"tool_name,omitempty" json:"toolName,omitempty"`
	ToolArgs  map[string]any    `yaml:"tool_args,omitempty" json:"toolArgs,omitempty"`
	Env       map[string]string `yaml:"env,omitempty" json:"env,omitempty"`
	Cwd       string            `yaml:"cwd,omitempty" json:"cwd,omitempty"`
	TimeoutMS int               `yaml:"timeout_ms,omitempty" json:"timeoutMs,omitempty"`

	// http_request fields
	Method  string            `yaml:"method,omitempty" json:"method,omitempty"`
	URL     string            `yaml:"url,omitempty" json:"url,omitempty"`
	Headers map[string]string `yaml:"headers,omitempty" json:"headers,omitempty"`
	Body    string            `yaml:"body,omitempty" json:"body,omitempty"`

	// write_artifact / read_artifact fields
	Title         string `yaml:"title,omitempty" json:"title,omitempty"`
	ContentRef    string `yaml:"content_ref,omitempty" json:"contentRef,omitempty"`
	MimeType      string `yaml:"mime_type,omitempty" json:"mimeType,omitempty"`
	ArtifactIDRef string `yaml:"artifact_id_ref,omitempty" json:"artifactIdRef,omitempty"`
}

// Workflow is the in-memory representation of one workflow YAML file.
type Workflow struct {
	ID            string  `yaml:"id" json:"id"`
	Name          string  `yaml:"name" json:"name"`
	Description   string  `yaml:"description,omitempty" json:"description,omitempty"`
	Version       int     `yaml:"version" json:"version"`
	InlineRun     bool    `yaml:"inline_run,omitempty" json:"inlineRun,omitempty"`
	RerunPolicy   string  `yaml:"rerun_policy,omitempty" json:"rerunPolicy,omitempty"`
	SlashCommand  string  `yaml:"slash_command,omitempty" json:"slashCommand,omitempty"`
	Inputs        []Input `yaml:"inputs,omitempty" json:"inputs,omitempty"`
	Steps         []Step  `yaml:"steps" json:"steps"`

	// Storage-layer metadata. Populated by Store.Load / Store.Save so
	// callers can round-trip the canonical yaml_source and surface
	// version-bookkeeping fields to the UI without making them part of
	// the YAML schema. Unexported so the YAML/JSON marshallers don't
	// touch them.
	yamlSource string
	hash       string
	createdAt  time.Time
	updatedAt  time.Time
}

// YAMLSource returns the canonical yaml_source bytes the workflow was
// loaded from. Empty for workflows constructed in memory and never
// persisted.
func (w Workflow) YAMLSource() string { return w.yamlSource }

// Hash returns the sha256 hex digest of YAMLSource. Empty for
// in-memory-only workflows.
func (w Workflow) Hash() string { return w.hash }

// CreatedAt returns the storage-layer creation timestamp. Zero for
// in-memory-only workflows.
func (w Workflow) CreatedAt() time.Time { return w.createdAt }

// UpdatedAt returns the storage-layer last-modified timestamp. Zero
// for in-memory-only workflows.
func (w Workflow) UpdatedAt() time.Time { return w.updatedAt }

// Run is the result of an Engine.Run invocation.
type Run struct {
	ID         string                `json:"id"`
	WorkflowID string                `json:"workflowId"`
	Status     string                `json:"status"` // running | completed | failed | interrupted
	StartedAt  time.Time             `json:"startedAt"`
	EndedAt    time.Time             `json:"endedAt,omitempty"`
	Steps      []StepResult          `json:"steps"`
	Outputs    map[string]TypedValue `json:"-"`
	Err        string                `json:"error,omitempty"`
}

// StepResult records the execution of one step in a Run.
type StepResult struct {
	Name      string    `json:"name"`
	Kind      StepKind  `json:"kind"`
	Status    string    `json:"status"` // running | completed | failed | skipped
	Output    string    `json:"output,omitempty"`
	Err       string    `json:"error,omitempty"`
	StartedAt time.Time `json:"startedAt"`
	EndedAt   time.Time `json:"endedAt,omitempty"`
}

// RunOptions tunes a single invocation.
type RunOptions struct {
	// ParentSessionID is set for inline_run dispatches. Beta engine
	// records it but doesn't yet wire it into a session manager.
	ParentSessionID string
	// ProgressSink, when non-nil, receives one event per step
	// transition. The RPC layer fans these onto the broker topic.
	ProgressSink func(ProgressEvent)
	// SkipCache, when true, bypasses the rerun_policy cache check
	// even if the workflow declares a policy. Used by the
	// confirmation-prompt path: the user said "yes, re-run anyway",
	// so the second invocation needs to dispatch fresh.
	SkipCache bool
}

// ProgressEvent is published once per step transition.
type ProgressEvent struct {
	RunID      string    `json:"runId"`
	WorkflowID string    `json:"workflowId"`
	Step       string    `json:"step"`
	Kind       StepKind  `json:"kind"`
	Status     string    `json:"status"`
	Output     string    `json:"output,omitempty"`
	Err        string    `json:"error,omitempty"`
	At         time.Time `json:"at"`
	// Inline is true when the event was emitted by InlineRun. The
	// chat composer uses this flag to route events to the inline
	// transcript instead of spawning a workflow_run session row.
	Inline bool `json:"inline,omitempty"`
}

// StepRunner is the per-kind execution interface. Beta ships
// implementations for model_turn + shell only; future WPs register
// the rest.
type StepRunner interface {
	Validate(step Step) error
	Run(ctx context.Context, step Step, rc *RunContext) (TypedValue, error)
}

// Sentinels.
var (
	ErrFeatureDisabled  = errors.New("workflows: feature disabled")
	ErrUnknownStepKind  = errors.New("workflows: unknown step kind")
	ErrInlineMultiStep  = errors.New("workflows: inline_run requires exactly one model_turn step")
	ErrForwardStepRef   = errors.New("workflows: step references a later step")
	ErrFileTooLarge     = errors.New("workflows: file exceeds 256 KiB")
	ErrInvalidID        = errors.New("workflows: invalid id")
	ErrUnknownReference = errors.New("workflows: unknown reference")
	ErrCancelled        = errors.New("workflows: run cancelled")
	ErrWorkflowNotFound = errors.New("workflows: workflow not found")
	// ErrRerunPolicyAsk is the typed envelope returned by ResolveRerun
	// when policy=prompt and a cached identical run exists. Callers
	// catch this and surface a confirm prompt to the user; the user's
	// choice routes back through Run with RunOptions.SkipCache=true.
	ErrRerunPolicyAsk = errors.New("workflows: rerun_policy=prompt requires user confirmation")
)
