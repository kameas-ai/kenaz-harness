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
	"fmt"
	"time"

	contextaudit "github.com/kameas-ai/kenaz-harness/core/context/audit"
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
	// WP05 — external-network step kinds.
	StepKindWebFetch  StepKind = "web_fetch"
	StepKindWebScrape StepKind = "web_scrape"
	// WP06 — control-flow step kinds.
	StepKindNotify    StepKind = "notify"
	StepKindWaitUntil StepKind = "wait_until"
	StepKindAggregate StepKind = "aggregate"
)

// AllStepKinds is the closed enum the loader validates against.
func AllStepKinds() []StepKind {
	return []StepKind{
		StepKindModelTurn, StepKindToolCall, StepKindMCPCall,
		StepKindHTTPRequest, StepKindShell, StepKindReadArtifact,
		StepKindWriteArtifact, StepKindTransform, StepKindConditional,
		StepKindWebFetch, StepKindWebScrape,
		StepKindNotify, StepKindWaitUntil, StepKindAggregate,
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

// StepToolsSpec is the value type for a model_turn step's `tools:`
// field. YAML supports two forms:
//
//	tools: all              # string scalar → AllAll = true
//	tools: [bash, search]  # sequence      → Names = ["bash","search"]
//
// The zero value (neither set) means no tools — byte-identical to the
// previous no-tools behaviour (FR-005).
type StepToolsSpec struct {
	// All, when true, means "expose all discovered tools to the model".
	All bool
	// Names, when non-empty (and All is false), restricts the tool set
	// to the listed names (matched against discovered tool names).
	Names []string
}

// IsEmpty reports whether the spec requests no tool access (zero value).
func (s StepToolsSpec) IsEmpty() bool { return !s.All && len(s.Names) == 0 }

// UnmarshalYAML supports both scalar ("all") and sequence ([...]) forms.
func (s *StepToolsSpec) UnmarshalYAML(unmarshal func(any) error) error {
	// Try scalar first.
	var str string
	if err := unmarshal(&str); err == nil {
		if str == "all" {
			s.All = true
			return nil
		}
		return fmt.Errorf("workflows: tools: string value must be %q (got %q)", "all", str)
	}
	// Try sequence.
	var names []string
	if err := unmarshal(&names); err != nil {
		return fmt.Errorf("workflows: tools: must be %q or a list of tool names", "all")
	}
	for _, n := range names {
		if n == "all" {
			s.All = true
			s.Names = nil
			return nil
		}
	}
	s.Names = names
	return nil
}

// MarshalJSON emits "all" for All=true, a list for Names, or nil for
// the empty spec so JSON round-trips cleanly.
func (s StepToolsSpec) MarshalJSON() ([]byte, error) {
	if s.IsEmpty() {
		return []byte("null"), nil
	}
	if s.All {
		return []byte(`"all"`), nil
	}
	// Encode as JSON array.
	out := `[`
	for i, n := range s.Names {
		if i > 0 {
			out += ","
		}
		out += `"` + n + `"`
	}
	out += `]`
	return []byte(out), nil
}

// Step is a single executable node in a workflow.
type Step struct {
	Name string   `yaml:"name" json:"name"`
	Kind StepKind `yaml:"kind" json:"kind"`

	// InputsFrom declares explicit upstream dependencies for DAG
	// semantics. When non-empty the loader uses these edges (instead
	// of implicit linear order) to topologically sort the step graph.
	// Each entry must be the Name of another step that appears in the
	// same workflow; forward references are detected during Validate and
	// cycles are rejected with ErrWorkflowCycle.
	InputsFrom []string `yaml:"inputs_from,omitempty" json:"inputsFrom,omitempty"`

	// model_turn fields
	UserPrompt string   `yaml:"user_prompt,omitempty" json:"userPrompt,omitempty"`
	AllowTools []string `yaml:"allow_tools,omitempty" json:"allowTools,omitempty"`
	// Tools controls tool access for this model_turn step.
	// "all" enables all discovered tools; a list enables the named
	// subset; absent (empty) disables tool calling entirely (default,
	// byte-identical to the previous behaviour — FR-005).
	// Valid only on model_turn steps; ignored on all other kinds.
	Tools StepToolsSpec `yaml:"tools,omitempty" json:"tools,omitempty"`
	// MaxToolIterations caps the model→tool→model loop for this step.
	// 0 means use the engine default (DefaultMaxToolIterations = 8).
	MaxToolIterations int `yaml:"max_tool_iterations,omitempty" json:"maxToolIterations,omitempty"`
	// Profile is the LLM provider profile id; the registry resolves
	// it to a kind+model. Empty falls back to Engine.Deps.DefaultLLMProfile.
	Profile string `yaml:"profile,omitempty" json:"profile,omitempty"`
	// Model overrides the profile's default model for this turn.
	Model string `yaml:"model,omitempty" json:"model,omitempty"`

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

	// mcp_call fields
	Server string `yaml:"server,omitempty" json:"server,omitempty"`

	// write_artifact / read_artifact fields
	Title         string `yaml:"title,omitempty" json:"title,omitempty"`
	ContentRef    string `yaml:"content_ref,omitempty" json:"contentRef,omitempty"`
	MimeType      string `yaml:"mime_type,omitempty" json:"mimeType,omitempty"`
	ArtifactIDRef string `yaml:"artifact_id_ref,omitempty" json:"artifactIdRef,omitempty"`
	// Content is the inline content body for write_artifact (template
	// expanded at run time).
	Content string `yaml:"content,omitempty" json:"content,omitempty"`

	// transform fields
	Template string `yaml:"template,omitempty" json:"template,omitempty"`

	// conditional fields
	If       string `yaml:"if,omitempty" json:"if,omitempty"`
	ThenStep string `yaml:"then_step,omitempty" json:"thenStep,omitempty"`
	ElseStep string `yaml:"else_step,omitempty" json:"elseStep,omitempty"`

	// web_fetch fields (WP05).
	// URL is shared with http_request. UserAgent overrides the default.
	// MinIntervalMS is the per-host rate-limit floor in milliseconds.
	UserAgent     string `yaml:"user_agent,omitempty" json:"userAgent,omitempty"`
	MinIntervalMS int    `yaml:"min_interval_ms,omitempty" json:"minIntervalMs,omitempty"`

	// web_scrape fields (WP05).
	// Mode selects the extraction engine: "css" (default) or "llm".
	// For "css": Extractors declares the CSS-selector rules.
	// For "llm": ExtractWithModel + ExtractPrompt configure LLM-driven
	// extraction; internally calls the model_turn runner.
	Mode             string         `yaml:"mode,omitempty" json:"mode,omitempty"`
	Extractors       []any          `yaml:"extractors,omitempty" json:"extractors,omitempty"`
	ExtractWithModel string         `yaml:"extract_with_model,omitempty" json:"extractWithModel,omitempty"`
	ExtractPrompt    string         `yaml:"extract_prompt,omitempty" json:"extractPrompt,omitempty"`

	// notify fields (WP06)
	// NotifyTitle is the short title shown on each notification surface.
	NotifyTitle string `yaml:"notify_title,omitempty" json:"notifyTitle,omitempty"`
	// NotifyBody is the full notification body. NEVER put in audit attrs.
	NotifyBody string `yaml:"notify_body,omitempty" json:"notifyBody,omitempty"`
	// Surface is the list of targets: os, slack, email, push.
	Surface []string `yaml:"surface,omitempty" json:"surface,omitempty"`

	// wait_until fields (WP06)
	// Until is an RFC 3339 absolute wall-clock time to wait until.
	Until string `yaml:"until,omitempty" json:"until,omitempty"`
	// WaitDuration is a relative duration string (e.g. "5m").
	WaitDuration string `yaml:"duration,omitempty" json:"duration,omitempty"`
	// Condition is a workflow expression polled until truthy.
	Condition string `yaml:"condition,omitempty" json:"condition,omitempty"`

	// aggregate fields (WP06) — Strategy, Separator reuse inputs_from (InputsFrom).
	// Strategy is one of "merge", "array", "concat".
	Strategy string `yaml:"strategy,omitempty" json:"strategy,omitempty"`
	// Separator is used by the "concat" strategy (default ",").
	Separator string `yaml:"separator,omitempty" json:"separator,omitempty"`
}

// WorkflowSecretRef declares one @secret: locator that a workflow needs
// at runtime. The Locator is checked against the live ExposureIndex when
// the run starts; the run fails fast if the locator is not exposed.
//
// No plaintext is stored here — this is a manifest entry only.
// (model-secret-references-01KW7M5A WP12)
type WorkflowSecretRef struct {
	// Locator is the bare locator (e.g. "user:github-pat"), without
	// the leading @secret: prefix.
	Locator string `yaml:"locator" json:"locator"`
	// Description is a human-readable hint shown in validation errors.
	Description string `yaml:"description,omitempty" json:"description,omitempty"`
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
	// Schedule is a 5-field cron expression (e.g. "0 7 * * *") that the
	// scheduler uses to fire the workflow automatically. Empty means no
	// recurring schedule. Introduced in WP04 (starter library).
	Schedule string `yaml:"schedule,omitempty" json:"schedule,omitempty"`
	// Timezone is an IANA timezone name (e.g. "America/New_York") paired
	// with Schedule. Defaults to UTC when empty.
	Timezone string `yaml:"timezone,omitempty" json:"timezone,omitempty"`
	Inputs        []Input `yaml:"inputs,omitempty" json:"inputs,omitempty"`
	// Secrets declares the set of @secret: locators this workflow needs
	// at runtime. Each entry names a locator from the session-scoped
	// ExposureIndex; when the workflow engine starts a run it asserts
	// all declared locators are exposed and fails fast if any are missing,
	// so the model never reaches a step that would fail mid-run due to a
	// missing credential.
	//
	// The entries do NOT carry plaintext — they are merely a manifest
	// that the run validator checks against the live ExposureIndex.
	//
	// Example YAML:
	//   secrets:
	//     - locator: user:github-pat
	//       description: "GitHub Personal Access Token"
	//
	// (model-secret-references-01KW7M5A WP12)
	Secrets       []WorkflowSecretRef `yaml:"secrets,omitempty" json:"secrets,omitempty"`
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
// implementations for every declared kind, but runners that need an
// external dependency (LLMRegistry, ToolRegistry, MCPCaller,
// ArtifactsManager) error at Run time when their dep is missing.
type StepRunner interface {
	Validate(step Step) error
	Run(ctx context.Context, step Step, rc *RunContext) (TypedValue, error)
}

// LLMStreamer is the narrow interface model_turn dispatches against.
// Mirrors core/llm/registry.Registry.Stream so the workflows package
// stays import-clean.
type LLMStreamer interface {
	Stream(ctx context.Context, req LLMRequest) (LLMStream, error)
}

// ToolSpec declares one callable tool the model may invoke. It mirrors
// core/llm.ToolSpec but is kept local so core/workflows stays
// import-clean (DIRECTIVE_001).
type ToolSpec struct {
	Name        string
	Description string
	// InputSchema is JSON-Schema bytes describing the tool's arguments.
	InputSchema []byte
}

// ToolUseCall is a single tool invocation emitted by the model.
// It mirrors core/llm.ToolUse without importing that package.
type ToolUseCall struct {
	ID    string
	Name  string
	Input []byte // raw JSON
}

// HistoryMessage is one turn in the multi-turn conversation built by
// the bounded tool loop. Role is "user", "assistant", or "tool".
type HistoryMessage struct {
	Role    string
	Text    string       // non-empty for user/assistant text turns
	ToolUses []ToolUseCall // populated for assistant tool-call turns
	// ToolResults carries results back to the model (role="tool").
	ToolResults []ToolCallResult
}

// ToolCallResult is the response from a single tool dispatch.
type ToolCallResult struct {
	ToolUseID string
	Content   string
	IsError   bool
}

// LLMRequest is the workflows-side mirror of llm.GenerationRequest,
// trimmed to the fields model_turn actually populates.
//
// When Tools is non-empty the registry is expected to advertise tool
// calling capability and return ToolUses via LLMStream.ToolCalls().
// History carries prior turns when running the bounded tool loop.
type LLMRequest struct {
	ProfileID string
	Model     string
	Prompt    string
	// Tools, when non-empty, enables tool-calling mode. The registry
	// forwards these specs to the provider.
	Tools []ToolSpec
	// History carries the prior conversation turns for the bounded
	// tool loop. nil/empty means a single-turn (plain completion).
	History []HistoryMessage
}

// LLMStream is the narrow streaming surface model_turn consumes.
// It mirrors llm.Stream's Events / Final pair so callers can adapt
// the registry's stream with a thin shim.
//
// When the stream terminates with tool calls instead of (or in
// addition to) text, callers should type-assert to ToolCallStream
// to retrieve the tool-use blocks.
type LLMStream interface {
	Events() <-chan LLMStreamEvent
	Final() (string, error)
}

// ToolCallStream is an optional extension of LLMStream that returns
// any tool-use calls the model emitted during the turn. Adapters that
// support tool calling implement this interface; callers type-assert
// before entering the bounded loop.
type ToolCallStream interface {
	LLMStream
	// ToolCalls returns the tool-use calls emitted by the model in
	// this turn. Must be called after the Events channel closes.
	ToolCalls() []ToolUseCall
}

// LLMStreamEvent is the single text-delta envelope model_turn cares
// about; non-text events are dropped by the adapter shim.
type LLMStreamEvent struct {
	Text string
	Err  string
}

// ToolDiscoverer resolves the live set of tools available to a
// model_turn step. It mirrors core/llm.ToolDiscoverer without
// importing that package. The wiring layer (core/rpc) adapts the
// same chat-path discoverer onto this interface so model_turn steps
// share one catalog and one permission filter with the chat surface.
//
// When Tools is "all", Discover() is called with no name filter.
// When Tools is a name list, Discover() is called and the result is
// filtered to the named subset.
type ToolDiscoverer interface {
	// Discover returns all currently-available tools. The caller
	// filters by name when the step specifies an explicit list.
	// A nil return is valid (no tools configured).
	Discover(ctx context.Context) ([]ToolSpec, error)
}

// ToolDispatcher dispatches a single tool call through the same
// permission/Cedar path as chat. It is analogous to the chat-path
// BuiltinPool.Call / MCPPool.Call but exposed as a single seam so
// the workflows package does not need to know about MCP servers or
// builtin registries.
//
// name is the fully-qualified namespaced name as discovered (e.g.
// "kenaz__bash" or "myserver__my_tool"). input is the raw JSON
// argument object from the model.
type ToolDispatcher interface {
	Dispatch(ctx context.Context, name string, input []byte) (content string, isError bool, err error)
}

// ToolCaller is the interface tool_call dispatches against. Mirrors
// agentgraph.ToolRegistry's Call surface.
type ToolCaller interface {
	Call(ctx context.Context, name string, args map[string]any) (ToolResult, error)
}

// ToolResult is what a tool call returns.
type ToolResult struct {
	Content string
	IsError bool
}

// MCPCaller is the interface mcp_call dispatches against. Mirrors
// transport/stdio.Pool.Call's surface.
type MCPCaller interface {
	Call(ctx context.Context, server, tool string, args map[string]any) (string, error)
}

// ArtifactsReadWriter is the interface read_artifact / write_artifact
// dispatch against. Mirrors the relevant subset of
// core/artifacts.Manager + Store + MediaStore.
type ArtifactsReadWriter interface {
	Read(ctx context.Context, id string) (ArtifactView, error)
	Write(ctx context.Context, in ArtifactWrite) (string, error)
}

// ArtifactView is what read_artifact returns: bytes + metadata.
type ArtifactView struct {
	ID       string
	Title    string
	MimeType string
	Content  []byte
}

// ArtifactWrite is what write_artifact submits.
type ArtifactWrite struct {
	SessionID string
	Title     string
	MimeType  string
	Content   []byte
}

// NetworkAuthorizer is the Cedar gate for external-network step kinds.
// Before any web_fetch or web_scrape step makes a network request, the
// engine calls Authorize with action "workflow.network.fetch" and
// resourceID set to the RUNNING WORKFLOW's id (not the step name) — the
// production adapter (automation-actually-runs-01PMZ404 UNIT-7) gates
// a Cedar Workflow::"<id>" resource, matching GateWorkflowRun /
// GateWorkflowSave / GateWorkflowDelete's shape. A non-nil error aborts
// the step with a policy_denied classification.
//
// nil is a no-op (permit by default) so test harnesses and the chassis
// boot path can run without a wired Cedar engine.
type NetworkAuthorizer interface {
	Authorize(ctx context.Context, action, resourceID string) error
}

// Notifier is the interface the notify runner dispatches OS notifications
// through. Inject a fake in tests; the production implementation wraps
// the Wails runtime.
type Notifier interface {
	Notify(ctx context.Context, title, body string) error
}

// AuditEmitter is the narrow slice of audit.Emitter the workflow runners
// consume. Keeping it local avoids a hard import of the audit package from
// the runner files while preserving testability.
type AuditEmitter interface {
	EmitNotifySent(ctx context.Context, target, title string) error
}

// DefaultMaxToolIterations is the default cap on the model→tool→model
// loop inside a model_turn step that has tools enabled. It mirrors the
// chat/agent-graph counting so a runaway model cannot spin forever.
const DefaultMaxToolIterations = 8

// Deps bundles the optional external dependencies workflow runners
// need. nil entries cause the corresponding runner to error at Run
// time with a clear "dependency unavailable" message — keeping
// NewEngine() callable from boot paths that haven't wired everything.
type Deps struct {
	LLM               LLMStreamer
	DefaultLLMProfile string
	// DefaultProfileFunc, when non-nil, is called at Run time to resolve
	// the active LLM profile when neither the step nor DefaultLLMProfile
	// supply one. It is evaluated lazily so adding a first provider after
	// launch makes workflows runnable without an app restart.
	// When both DefaultLLMProfile and DefaultProfileFunc are set,
	// DefaultLLMProfile wins (for back-compat with existing tests).
	DefaultProfileFunc func() string
	// ToolDiscoverer, when non-nil, provides the live tool catalog for
	// model_turn steps that opt into tool access (FR-002). Wired from
	// the same discoverer chat uses (one catalog, one permission filter).
	// nil disables tool calling in model_turn regardless of Step.Tools.
	ToolDiscoverer ToolDiscoverer
	// ToolDispatcher, when non-nil, dispatches tool calls through the
	// same permission/Cedar path as chat (FR-003). nil means the tool
	// loop returns an error for any step with tools enabled.
	ToolDispatcher ToolDispatcher
	Tools              ToolCaller
	MCP                MCPCaller
	Artifacts         ArtifactsReadWriter
	// SessionID is the fallback session id threaded into write_artifact
	// rows when the run itself carries none. The run-scoped value —
	// RunOptions.ParentSessionID, surfaced to the runner via
	// RunContext.ParentSessionID — wins when non-empty (UNIT-5: the
	// session id a slash-dispatched /wf run carries via
	// slashcmd.Env.SessionID varies per invocation, so it cannot be
	// bound once at Deps-construction time the way this field is).
	// Empty on both disables artifact writes (write_artifact returns an
	// error naming the gap).
	SessionID string
	// NetAuthz, when non-nil, gates web_fetch and web_scrape steps via
	// Cedar action "workflow.network.fetch". nil permits all fetches.
	NetAuthz NetworkAuthorizer
	// Notifier is the OS notification dispatch surface for the notify
	// runner. nil disables the "os" surface without failing the run.
	Notifier Notifier
	// Audit, when non-nil, receives notify.sent events. nil is a no-op.
	Audit AuditEmitter
	// NetworkAudit, when non-nil, receives KindWorkflowNetworkFetch
	// events — one per web_fetch/web_scrape step that completes a
	// network request (core/context/audit/audit.go:108,
	// audit-that-tells-the-truth-01PMZA10 UNIT-5). A SEPARATE field
	// from Audit above, deliberately: Audit is the narrow, notify-only
	// AuditEmitter defined in this file (Shape 2 in the mission's
	// vocabulary — out of scope here, owned by automation-actually-
	// runs-01PMZ404 UNIT-8); NetworkAudit is the general-purpose
	// contextaudit.Emitter (Shape 1) core/workflows/audit.go's
	// EmitExecuted/EmitStepFailures/EmitSaved/EmitDeleted already use,
	// threaded here so the runner layer (runners.go) can reach it too.
	NetworkAudit contextaudit.Emitter
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
	// ErrRunNotFound is returned by Engine.Cancel when the run ID is
	// not registered in the active-runs registry (already completed or
	// never started on this engine instance).
	ErrRunNotFound = errors.New("workflows: run not found")
	ErrWorkflowNotFound = errors.New("workflows: workflow not found")
	// ErrWorkflowCycle is returned by the loader when it detects a
	// cycle in the inputs_from dependency graph. The error message
	// includes the offending cycle path.
	ErrWorkflowCycle = errors.New("workflows: cycle detected in inputs_from graph")
	// ErrRerunPolicyAsk is the typed envelope returned by ResolveRerun
	// when policy=prompt and a cached identical run exists. Callers
	// catch this and surface a confirm prompt to the user; the user's
	// choice routes back through Run with RunOptions.SkipCache=true.
	ErrRerunPolicyAsk = errors.New("workflows: rerun_policy=prompt requires user confirmation")
	// ErrBlockedByRobots is re-exported from the web sub-package so
	// callers can identify robots.txt refusals without importing
	// core/workflows/web directly.
	ErrBlockedByRobots = errors.New("web: URL blocked by robots.txt")
	// ErrNetworkPolicyDenied is returned when the Cedar gate refuses a
	// web_fetch or web_scrape step.
	ErrNetworkPolicyDenied = errors.New("workflows: network fetch denied by policy")
	// ErrNotifyTargetUnconfigured is returned by a notify runner when a
	// requested surface (e.g. "slack") is not configured. The run
	// continues with any successfully dispatched surfaces.
	ErrNotifyTargetUnconfigured = errors.New("workflows: notify target not configured")
)
