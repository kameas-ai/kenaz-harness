package agentgraph

import (
	"errors"
	"fmt"
)

// This file defines the typed attribute payloads for each NodeKind.
// Each *Attrs type implements NodeAttrs and owns its own per-kind
// validation rules. The validator (validator.go) wraps any returned
// error with the offending node ID.

// ---- Compute attrs ----

// LLMAttrs is the payload for `kind: llm` (FR-004).
type LLMAttrs struct {
	Provider       string         `json:"provider,omitempty" yaml:"provider,omitempty"`
	Model          string         `json:"model" yaml:"model"`
	SystemPrompt   string         `json:"system_prompt,omitempty" yaml:"system_prompt,omitempty"`
	ToolAllowlist  []string       `json:"tool_allowlist,omitempty" yaml:"tool_allowlist,omitempty"`
	MaxTokens      int            `json:"max_tokens,omitempty" yaml:"max_tokens,omitempty"`
	Temperature    *float64       `json:"temperature,omitempty" yaml:"temperature,omitempty"`
	JSONSchema     map[string]any `json:"json_schema,omitempty" yaml:"json_schema,omitempty"`
	StreamToChat   bool           `json:"stream_to_chat,omitempty" yaml:"stream_to_chat,omitempty"`
}

func (LLMAttrs) nodeAttrsMarker() {}

// Validate enforces FR-004 minimums: a model must be named.
func (a LLMAttrs) Validate() error {
	if a.Model == "" {
		return errors.New("llm: model is required")
	}
	if a.MaxTokens < 0 {
		return errors.New("llm: max_tokens must be non-negative")
	}
	if a.Temperature != nil && (*a.Temperature < 0 || *a.Temperature > 2) {
		return fmt.Errorf("llm: temperature %.3f out of range [0,2]", *a.Temperature)
	}
	return nil
}

// ToolAttrs is the payload for `kind: tool` (FR-005). Name is the
// canonical `<server>__<tool>` namespaced form the toolloop uses.
type ToolAttrs struct {
	Name string         `json:"name" yaml:"name"`
	Args map[string]any `json:"args,omitempty" yaml:"args,omitempty"`
}

func (ToolAttrs) nodeAttrsMarker() {}

func (a ToolAttrs) Validate() error {
	if a.Name == "" {
		return errors.New("tool: name is required")
	}
	return nil
}

// TransformAttrs is the payload for `kind: transform` (FR-006). Name is
// looked up in the transform registry at execution time; WP01 only
// validates non-empty.
type TransformAttrs struct {
	Name   string         `json:"name" yaml:"name"`
	Params map[string]any `json:"params,omitempty" yaml:"params,omitempty"`
}

func (TransformAttrs) nodeAttrsMarker() {}

func (a TransformAttrs) Validate() error {
	if a.Name == "" {
		return errors.New("transform: name is required")
	}
	return nil
}

// ActivityAttrs is the payload for `kind: activity` (FR-007).
type ActivityAttrs struct {
	ActivityID string         `json:"activity_id" yaml:"activity_id"`
	Version    string         `json:"version,omitempty" yaml:"version,omitempty"`
	Inputs     map[string]any `json:"inputs,omitempty" yaml:"inputs,omitempty"`
}

func (ActivityAttrs) nodeAttrsMarker() {}

func (a ActivityAttrs) Validate() error {
	if a.ActivityID == "" {
		return errors.New("activity: activity_id is required")
	}
	return nil
}

// ReflectAttrs is the payload for `kind: reflect` (FR-008).
type ReflectAttrs struct {
	Model              string  `json:"model,omitempty" yaml:"model,omitempty"`
	SeverityThreshold  string  `json:"severity_threshold,omitempty" yaml:"severity_threshold,omitempty"`
	IncludeTrace       bool    `json:"include_trace,omitempty" yaml:"include_trace,omitempty"`
	// MaxIterations is the cap when this Reflect is composed inside a
	// Loop. Validator enforces presence in that context (FR-047) — see
	// validator.go enclosing-loop-cap rule.
	MaxIterations int `json:"max_iterations,omitempty" yaml:"max_iterations,omitempty"`
}

func (ReflectAttrs) nodeAttrsMarker() {}

func (a ReflectAttrs) Validate() error {
	if a.MaxIterations < 0 {
		return errors.New("reflect: max_iterations must be non-negative")
	}
	switch a.SeverityThreshold {
	case "", "low", "medium", "high":
	default:
		return fmt.Errorf("reflect: severity_threshold %q (want low|medium|high)", a.SeverityThreshold)
	}
	return nil
}

// ReviewAttrs is the payload for `kind: review` (FR-009).
type ReviewAttrs struct {
	Model         string `json:"model,omitempty" yaml:"model,omitempty"`
	UpstreamNode  string `json:"upstream_node" yaml:"upstream_node"`
	MaxIterations int    `json:"max_iterations" yaml:"max_iterations"`
	OnCapHit      string `json:"on_cap_hit,omitempty" yaml:"on_cap_hit,omitempty"`
}

func (ReviewAttrs) nodeAttrsMarker() {}

// Validate: ReviewNode mandates max_iterations (FR-009 + FR-047).
func (a ReviewAttrs) Validate() error {
	if a.UpstreamNode == "" {
		return errors.New("review: upstream_node is required")
	}
	if a.MaxIterations <= 0 {
		return errors.New("review: max_iterations must be > 0 (FR-009 mandates a cap)")
	}
	switch a.OnCapHit {
	case "", "escalate", "halt":
	default:
		return fmt.Errorf("review: on_cap_hit %q (want escalate|halt)", a.OnCapHit)
	}
	return nil
}

// PlanAttrs is the payload for `kind: plan` (FR-010).
type PlanAttrs struct {
	Verbosity      string `json:"verbosity,omitempty" yaml:"verbosity,omitempty"`
	PlannerModel   string `json:"planner_model,omitempty" yaml:"planner_model,omitempty"`
	ThresholdInput string `json:"threshold_input,omitempty" yaml:"threshold_input,omitempty"`
}

func (PlanAttrs) nodeAttrsMarker() {}

func (a PlanAttrs) Validate() error {
	switch a.Verbosity {
	case "", "terse", "standard", "verbose":
	default:
		return fmt.Errorf("plan: verbosity %q (want terse|standard|verbose)", a.Verbosity)
	}
	return nil
}

// AskAttrs is the payload for `kind: ask` (FR-011).
type AskAttrs struct {
	Question string `json:"question" yaml:"question"`
}

func (AskAttrs) nodeAttrsMarker() {}

func (a AskAttrs) Validate() error {
	if a.Question == "" {
		return errors.New("ask: question is required")
	}
	return nil
}

// EscalateAttrs is the payload for `kind: escalate` (FR-012).
type EscalateAttrs struct {
	TargetModel       string  `json:"target_model" yaml:"target_model"`
	UpstreamNode      string  `json:"upstream_node" yaml:"upstream_node"`
	ConfidenceFloor   float64 `json:"confidence_floor,omitempty" yaml:"confidence_floor,omitempty"`
	OneEscalationOnly bool    `json:"one_escalation_only,omitempty" yaml:"one_escalation_only,omitempty"`
}

func (EscalateAttrs) nodeAttrsMarker() {}

func (a EscalateAttrs) Validate() error {
	if a.TargetModel == "" {
		return errors.New("escalate: target_model is required")
	}
	if a.UpstreamNode == "" {
		return errors.New("escalate: upstream_node is required")
	}
	if a.ConfidenceFloor < 0 || a.ConfidenceFloor > 1 {
		return fmt.Errorf("escalate: confidence_floor %.3f out of [0,1]", a.ConfidenceFloor)
	}
	return nil
}

// ---- Control attrs ----

// BranchAttrs is the payload for `kind: branch` (FR-013). Condition is
// the source text for the tiny expression evaluator the kernel will
// implement (open question §10/1 — eq/lt/gt/and/or/not v1; CEL deferred).
type BranchAttrs struct {
	Condition string `json:"condition" yaml:"condition"`
	NextTrue  string `json:"next_true" yaml:"next_true"`
	NextFalse string `json:"next_false" yaml:"next_false"`
}

func (BranchAttrs) nodeAttrsMarker() {}

func (a BranchAttrs) Validate() error {
	if a.Condition == "" {
		return errors.New("branch: condition is required")
	}
	if a.NextTrue == "" || a.NextFalse == "" {
		return errors.New("branch: next_true and next_false are required")
	}
	return nil
}

// ParallelAttrs is the payload for `kind: parallel` (FR-014).
type ParallelAttrs struct {
	FanOut         int      `json:"fan_out,omitempty" yaml:"fan_out,omitempty"`
	Targets        []string `json:"targets" yaml:"targets"`
	MaxConcurrency int      `json:"max_concurrency,omitempty" yaml:"max_concurrency,omitempty"`
}

func (ParallelAttrs) nodeAttrsMarker() {}

func (a ParallelAttrs) Validate() error {
	if len(a.Targets) == 0 {
		return errors.New("parallel: at least one target is required")
	}
	if a.MaxConcurrency < 0 {
		return errors.New("parallel: max_concurrency must be non-negative")
	}
	return nil
}

// JoinAttrs is the payload for `kind: join` (FR-014).
type JoinAttrs struct {
	From  []string `json:"from" yaml:"from"`
	Order string   `json:"order,omitempty" yaml:"order,omitempty"`
}

func (JoinAttrs) nodeAttrsMarker() {}

func (a JoinAttrs) Validate() error {
	if len(a.From) == 0 {
		return errors.New("join: from is required")
	}
	switch a.Order {
	case "", "declared", "first_done":
	default:
		return fmt.Errorf("join: order %q (want declared|first_done)", a.Order)
	}
	return nil
}

// LoopAttrs is the payload for `kind: loop` (FR-015). MaxIterations is
// MANDATORY: validator rejects unbounded loops at load time (NFR-004).
type LoopAttrs struct {
	MaxIterations int    `json:"max_iterations" yaml:"max_iterations"`
	Condition     string `json:"condition,omitempty" yaml:"condition,omitempty"`
	Body          []string `json:"body" yaml:"body"`
}

func (LoopAttrs) nodeAttrsMarker() {}

func (a LoopAttrs) Validate() error {
	if a.MaxIterations <= 0 {
		return errors.New("loop: max_iterations must be > 0 (NFR-004 / FR-015 mandate a cap)")
	}
	if len(a.Body) == 0 {
		return errors.New("loop: body must reference at least one node")
	}
	return nil
}

// RetryAttrs is the payload for `kind: retry` (FR-016). MaxAttempts is
// MANDATORY (NFR-004).
type RetryAttrs struct {
	MaxAttempts int    `json:"max_attempts" yaml:"max_attempts"`
	BackoffBase int    `json:"backoff_base_ms,omitempty" yaml:"backoff_base_ms,omitempty"`
	BackoffMax  int    `json:"backoff_max_ms,omitempty" yaml:"backoff_max_ms,omitempty"`
	Body        []string `json:"body" yaml:"body"`
}

func (RetryAttrs) nodeAttrsMarker() {}

func (a RetryAttrs) Validate() error {
	if a.MaxAttempts <= 0 {
		return errors.New("retry: max_attempts must be > 0 (NFR-004 / FR-016 mandate a cap)")
	}
	if len(a.Body) == 0 {
		return errors.New("retry: body must reference at least one node")
	}
	if a.BackoffBase < 0 || a.BackoffMax < 0 {
		return errors.New("retry: backoff durations must be non-negative")
	}
	return nil
}

// ForkAttrs is the payload for `kind: fork` (FR-017). Real impl in WP08;
// WP01 only validates the schema.
type ForkAttrs struct {
	Title         string `json:"title" yaml:"title"`
	ParentLeaf    string `json:"parent_leaf,omitempty" yaml:"parent_leaf,omitempty"`
	ModelOverride string `json:"model_override,omitempty" yaml:"model_override,omitempty"`
	ToolAllowlist []string `json:"tool_allowlist,omitempty" yaml:"tool_allowlist,omitempty"`
	MessageSubset []string `json:"message_subset,omitempty" yaml:"message_subset,omitempty"`
}

func (ForkAttrs) nodeAttrsMarker() {}

func (a ForkAttrs) Validate() error {
	if a.Title == "" {
		return errors.New("fork: title is required")
	}
	return nil
}

// MergeAttrs is the payload for `kind: merge` (FR-018).
type MergeAttrs struct {
	BranchID string `json:"branch_id" yaml:"branch_id"`
	Mode     string `json:"mode,omitempty" yaml:"mode,omitempty"`
}

func (MergeAttrs) nodeAttrsMarker() {}

func (a MergeAttrs) Validate() error {
	if a.BranchID == "" {
		return errors.New("merge: branch_id is required")
	}
	switch a.Mode {
	case "", "append", "summarize_append", "replace_last_turn":
	default:
		return fmt.Errorf("merge: mode %q (want append|summarize_append|replace_last_turn)", a.Mode)
	}
	return nil
}

// ---- State attrs ----

// MemoryAttrs is the payload for `kind: memory` (FR-019). Mode = read |
// write | upsert; Scope = global | project | session.
type MemoryAttrs struct {
	Mode    string `json:"mode" yaml:"mode"`
	Scope   string `json:"scope,omitempty" yaml:"scope,omitempty"`
	Query   string `json:"query,omitempty" yaml:"query,omitempty"`
	Content string `json:"content,omitempty" yaml:"content,omitempty"`
	TopK    int    `json:"top_k,omitempty" yaml:"top_k,omitempty"`
	Pin     bool   `json:"pin,omitempty" yaml:"pin,omitempty"`
}

func (MemoryAttrs) nodeAttrsMarker() {}

func (a MemoryAttrs) Validate() error {
	switch a.Mode {
	case "read", "write", "upsert":
	default:
		return fmt.Errorf("memory: mode %q (want read|write|upsert)", a.Mode)
	}
	switch a.Scope {
	case "", "global", "project", "session":
	default:
		return fmt.Errorf("memory: scope %q (want global|project|session)", a.Scope)
	}
	if a.Mode == "read" && a.TopK < 0 {
		return errors.New("memory: top_k must be non-negative")
	}
	if (a.Mode == "write" || a.Mode == "upsert") && a.Content == "" {
		return errors.New("memory: content is required for mode=write|upsert")
	}
	return nil
}

// CorpusReadAttrs is the payload for `kind: corpus_read` (FR-020).
type CorpusReadAttrs struct {
	CorpusIDs        []string `json:"corpus_ids" yaml:"corpus_ids"`
	Query            string   `json:"query,omitempty" yaml:"query,omitempty"`
	TopK             int      `json:"top_k,omitempty" yaml:"top_k,omitempty"`
	SourcePathPrefix string   `json:"source_path_prefix,omitempty" yaml:"source_path_prefix,omitempty"`
	MIMETypes        []string `json:"mime_types,omitempty" yaml:"mime_types,omitempty"`
	ScoreThreshold   float64  `json:"score_threshold,omitempty" yaml:"score_threshold,omitempty"`
	MaxBytes         int      `json:"max_bytes,omitempty" yaml:"max_bytes,omitempty"`
}

func (CorpusReadAttrs) nodeAttrsMarker() {}

func (a CorpusReadAttrs) Validate() error {
	if len(a.CorpusIDs) == 0 {
		return errors.New("corpus_read: at least one corpus_id is required")
	}
	if a.TopK < 0 {
		return errors.New("corpus_read: top_k must be non-negative")
	}
	if a.ScoreThreshold < 0 || a.ScoreThreshold > 1 {
		return fmt.Errorf("corpus_read: score_threshold %.3f out of [0,1]", a.ScoreThreshold)
	}
	if a.MaxBytes < 0 {
		return errors.New("corpus_read: max_bytes must be non-negative")
	}
	return nil
}

// CorpusWriteAttrs is the payload for `kind: corpus_write` (FR-021).
type CorpusWriteAttrs struct {
	CorpusID   string `json:"corpus_id" yaml:"corpus_id"`
	SourcePath string `json:"source_path" yaml:"source_path"`
}

func (CorpusWriteAttrs) nodeAttrsMarker() {}

func (a CorpusWriteAttrs) Validate() error {
	if a.CorpusID == "" {
		return errors.New("corpus_write: corpus_id is required")
	}
	if a.SourcePath == "" {
		return errors.New("corpus_write: source_path is required")
	}
	return nil
}

// AttachmentAttrs is the payload for `kind: attachment` (FR-022).
type AttachmentAttrs struct {
	AttachmentID string `json:"attachment_id" yaml:"attachment_id"`
	Inline       bool   `json:"inline,omitempty" yaml:"inline,omitempty"`
}

func (AttachmentAttrs) nodeAttrsMarker() {}

func (a AttachmentAttrs) Validate() error {
	if a.AttachmentID == "" {
		return errors.New("attachment: attachment_id is required")
	}
	return nil
}

// HistoryReadAttrs is the payload for `kind: history_read` (FR-023).
type HistoryReadAttrs struct {
	N        int    `json:"n,omitempty" yaml:"n,omitempty"`
	BranchID string `json:"branch_id,omitempty" yaml:"branch_id,omitempty"`
}

func (HistoryReadAttrs) nodeAttrsMarker() {}

func (a HistoryReadAttrs) Validate() error {
	if a.N < 0 {
		return errors.New("history_read: n must be non-negative")
	}
	return nil
}

// TraceWriteAttrs is the payload for `kind: trace_write` (FR-024).
type TraceWriteAttrs struct {
	Severity string         `json:"severity,omitempty" yaml:"severity,omitempty"`
	Message  string         `json:"message" yaml:"message"`
	Attrs    map[string]any `json:"attrs,omitempty" yaml:"attrs,omitempty"`
}

func (TraceWriteAttrs) nodeAttrsMarker() {}

func (a TraceWriteAttrs) Validate() error {
	if a.Message == "" {
		return errors.New("trace_write: message is required")
	}
	switch a.Severity {
	case "", "debug", "info", "warn", "error":
	default:
		return fmt.Errorf("trace_write: severity %q (want debug|info|warn|error)", a.Severity)
	}
	return nil
}

// CheckpointAttrs is the payload for `kind: checkpoint` (FR-025). The
// kernel auto-checkpoints between every node-fire; this node lets a
// graph author force a checkpoint at a logical boundary.
type CheckpointAttrs struct {
	Label string `json:"label,omitempty" yaml:"label,omitempty"`
}

func (CheckpointAttrs) nodeAttrsMarker() {}

func (CheckpointAttrs) Validate() error { return nil }

// emptyAttrs is a zero-config Attrs struct used for kinds that have no
// per-kind payload required at WP01 (none currently — every kind owns
// its own struct, but emptyAttrs is here as a defensive default in case
// future authors omit `attrs:` on a no-config kind).
type emptyAttrs struct{}

func (emptyAttrs) nodeAttrsMarker() {}
func (emptyAttrs) Validate() error  { return nil }

// defaultAttrsFor returns a zero-value *Attrs struct for the kind, used
// when the YAML omits the `attrs:` block. Returns nil for unknown kinds
// (validator catches that earlier).
func defaultAttrsFor(kind NodeKind) NodeAttrs {
	switch kind {
	case NodeKindLLM:
		return LLMAttrs{}
	case NodeKindTool:
		return ToolAttrs{}
	case NodeKindTransform:
		return TransformAttrs{}
	case NodeKindActivity:
		return ActivityAttrs{}
	case NodeKindReflect:
		return ReflectAttrs{}
	case NodeKindReview:
		return ReviewAttrs{}
	case NodeKindPlan:
		return PlanAttrs{}
	case NodeKindAsk:
		return AskAttrs{}
	case NodeKindEscalate:
		return EscalateAttrs{}
	case NodeKindBranch:
		return BranchAttrs{}
	case NodeKindParallel:
		return ParallelAttrs{}
	case NodeKindJoin:
		return JoinAttrs{}
	case NodeKindLoop:
		return LoopAttrs{}
	case NodeKindRetry:
		return RetryAttrs{}
	case NodeKindFork:
		return ForkAttrs{}
	case NodeKindMerge:
		return MergeAttrs{}
	case NodeKindMemory:
		return MemoryAttrs{}
	case NodeKindCorpusRead:
		return CorpusReadAttrs{}
	case NodeKindCorpusWrite:
		return CorpusWriteAttrs{}
	case NodeKindAttachment:
		return AttachmentAttrs{}
	case NodeKindHistoryRead:
		return HistoryReadAttrs{}
	case NodeKindTraceWrite:
		return TraceWriteAttrs{}
	case NodeKindCheckpoint:
		return CheckpointAttrs{}
	}
	return nil
}

// defaultPortsFor returns the canonical input/output ports for each
// node-kind. Authors can override per-node by listing `inputs:` /
// `outputs:` explicitly. The validator falls back to these when the
// node omits a port surface.
//
// The shapes are intentionally loose (`any`) so this WP01 layer does
// not foreclose on the kernel's runtime choices — WP02..WP04 will
// tighten typing where the implementation demands it.
func defaultPortsFor(kind NodeKind) (inputs, outputs []Port) {
	switch kind {
	case NodeKindLLM:
		return []Port{
				{Name: "messages", Type: PortTypeMessages, Required: true},
			}, []Port{
				{Name: "response", Type: PortTypeMessages},
			}
	case NodeKindTool:
		return []Port{
				{Name: "args", Type: PortTypeJSON},
			}, []Port{
				{Name: "result", Type: PortTypeToolResult},
			}
	case NodeKindTransform:
		return []Port{
				{Name: "in", Type: PortTypeAny, Required: true},
			}, []Port{
				{Name: "out", Type: PortTypeAny},
			}
	case NodeKindActivity:
		return []Port{
				{Name: "in", Type: PortTypeAny},
			}, []Port{
				{Name: "out", Type: PortTypeAny},
			}
	case NodeKindReflect:
		return []Port{
				{Name: "draft", Type: PortTypeMessages, Required: true},
			}, []Port{
				{Name: "critique", Type: PortTypeJSON},
				{Name: "revision", Type: PortTypeMessages},
			}
	case NodeKindReview:
		return []Port{
				{Name: "draft", Type: PortTypeAny, Required: true},
			}, []Port{
				{Name: "verdict", Type: PortTypeJSON},
				{Name: "approved", Type: PortTypeAny},
			}
	case NodeKindPlan:
		return []Port{
				{Name: "task", Type: PortTypeText, Required: true},
			}, []Port{
				{Name: "plan", Type: PortTypeMessages},
			}
	case NodeKindAsk:
		return []Port{
				{Name: "context", Type: PortTypeAny},
			}, []Port{
				{Name: "answer", Type: PortTypeText},
			}
	case NodeKindEscalate:
		return []Port{
				{Name: "trigger", Type: PortTypeAny},
			}, []Port{
				{Name: "result", Type: PortTypeAny},
			}
	case NodeKindBranch:
		return []Port{
				{Name: "in", Type: PortTypeAny, Required: true},
			}, []Port{
				{Name: "true", Type: PortTypeAny},
				{Name: "false", Type: PortTypeAny},
			}
	case NodeKindParallel:
		return []Port{
				{Name: "in", Type: PortTypeAny},
			}, []Port{
				{Name: "out", Type: PortTypeAny},
			}
	case NodeKindJoin:
		return []Port{
				{Name: "in", Type: PortTypeAny},
			}, []Port{
				{Name: "out", Type: PortTypeAny},
			}
	case NodeKindLoop:
		return []Port{
				{Name: "in", Type: PortTypeAny},
			}, []Port{
				{Name: "out", Type: PortTypeAny},
			}
	case NodeKindRetry:
		return []Port{
				{Name: "in", Type: PortTypeAny},
			}, []Port{
				{Name: "out", Type: PortTypeAny},
			}
	case NodeKindFork:
		return []Port{
				{Name: "parent", Type: PortTypeAny},
			}, []Port{
				{Name: "branch_id", Type: PortTypeBranchID},
			}
	case NodeKindMerge:
		return []Port{
				{Name: "branch", Type: PortTypeBranchID, Required: true},
			}, []Port{
				{Name: "merged", Type: PortTypeMessages},
			}
	case NodeKindMemory:
		return []Port{
				{Name: "in", Type: PortTypeAny},
			}, []Port{
				{Name: "out", Type: PortTypeMemoryChunk},
			}
	case NodeKindCorpusRead:
		return []Port{
				{Name: "query", Type: PortTypeText},
			}, []Port{
				{Name: "hits", Type: PortTypeCorpusHits},
			}
	case NodeKindCorpusWrite:
		return []Port{
				{Name: "in", Type: PortTypeAny},
			}, []Port{
				{Name: "job_handle", Type: PortTypeJSON},
			}
	case NodeKindAttachment:
		return []Port{
				{Name: "in", Type: PortTypeAny},
			}, []Port{
				{Name: "block", Type: PortTypeAttachment},
			}
	case NodeKindHistoryRead:
		return nil, []Port{
			{Name: "messages", Type: PortTypeMessages},
		}
	case NodeKindTraceWrite:
		return []Port{
				{Name: "in", Type: PortTypeAny},
			}, []Port{
				{Name: "ack", Type: PortTypeTrace},
			}
	case NodeKindCheckpoint:
		return []Port{
				{Name: "in", Type: PortTypeAny},
			}, []Port{
				{Name: "ack", Type: PortTypeAny},
			}
	}
	return nil, nil
}
