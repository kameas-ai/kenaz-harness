package agentgraph

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// PortValues is the typed-loose carrier for input/output port data
// flowing along edges. The kernel hands a node its inputs as a
// PortValues map keyed by port name; the node returns its outputs the
// same way.
//
// Why `map[string]any` rather than a tighter shape: the agentgraph
// type system is nominal (PortType strings) — concrete value types are
// the kernel's runtime concern. Forcing `[]llm.Message` here would
// pull every callsite into a heavy wire shape; keeping `any` lets each
// executor own its own marshalling.
type PortValues map[string]any

// Get returns the value at port `name` and a presence bool.
func (p PortValues) Get(name string) (any, bool) {
	v, ok := p[name]
	return v, ok
}

// GetString returns the value at `name` as a string when possible.
func (p PortValues) GetString(name string) (string, bool) {
	v, ok := p[name]
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	return s, ok
}

// Clone returns a shallow copy.
func (p PortValues) Clone() PortValues {
	if p == nil {
		return nil
	}
	out := make(PortValues, len(p))
	for k, v := range p {
		out[k] = v
	}
	return out
}

// ErrPaused is returned by an executor when it parks a run pending
// external input (e.g. AskNode waiting for the user). The kernel
// surfaces this to its caller and persists state so the run can resume.
var ErrPaused = errors.New("agentgraph: run paused")

// ErrBudgetExceeded is returned when a per-run budget cap fires.
var ErrBudgetExceeded = errors.New("agentgraph: budget exceeded")

// ErrNotImplemented marks executors that exist only as stubs in this
// bundle (Fork/Merge real impl lands in Bundle B; Corpus real impl in
// Bundle C). Tests rely on the sentinel to assert the kernel surfaced
// the placeholder rather than silently no-op'd.
var ErrNotImplemented = errors.New("agentgraph: executor not implemented in this bundle")

// Result is what an Executor returns on a successful fire.
type Result struct {
	// Outputs are the produced port values, keyed by port name. The
	// kernel propagates them along outgoing edges.
	Outputs PortValues
	// Events are the side-effect records the executor wants persisted
	// to the EventLog. They are committed atomically with the kernel's
	// own NodeStart/NodeComplete pair.
	Events EventBatch
	// Pause, when set, signals the kernel to halt the run with
	// ErrPaused. The Outputs map is still propagated (so e.g. an Ask
	// that wants to surface its question on a port can do so).
	Pause bool
	// PauseReason is a human-readable explanation; persisted in the
	// EventLog as the `pending_*` event payload.
	PauseReason string
}

// NewResult is the canonical constructor.
func NewResult() Result {
	return Result{Outputs: PortValues{}}
}

// Executor is the kernel-side runtime for one node-kind. The kernel
// dispatches to an executor by NodeKind via the global registry built
// in NewKernel; per-test injection is supported via WithExecutor.
type Executor interface {
	// Kind returns the node-kind this executor handles. Must match the
	// Node.Kind values it is registered for.
	Kind() NodeKind
	// Execute fires the node. inputs carries the port values produced
	// by upstream nodes; the implementation returns its outputs +
	// pending events. Execute MUST honor ctx cancellation.
	Execute(ctx context.Context, env *Env, node *Node, inputs PortValues) (Result, error)
}

// Env is the execution environment passed to every Executor. It is the
// narrowest possible surface — packages outside `agentgraph` would not
// be able to fake all of it, so we keep field-level dependency
// injection (LLMProvider, ToolRegistry, MemoryStore, ...).
type Env struct {
	// Run identifies the active run.
	RunID string
	// SessionID identifies the owning session; used for memory scopes,
	// telemetry tags, etc.
	SessionID string
	// ProjectID is the optional project the session belongs to.
	ProjectID string

	// Graph is the spec being executed. Read-only; nodes look up their
	// peers (e.g. Loop bodies) by ID.
	Graph *Graph

	// LLM is the provider seam. The default is a stub that errors;
	// production wiring installs a real Registry.
	LLM LLMProvider
	// Tools is the tool dispatch seam.
	Tools ToolRegistry
	// Transforms is the transform-name registry.
	Transforms *TransformRegistry
	// Memory is the memory store seam (kernel-owned write path).
	Memory MemoryStore
	// Activities resolves ActivityNode references to sub-graphs.
	Activities ActivityCatalog
	// Corpus is the optional corpus reader/writer; nil disables
	// corpus_read / corpus_write executors.
	Corpus CorpusBackend
	// History is the session-history reader.
	History HistoryReader
	// HistoryWriter is the optional persistence half of the history
	// seam, consumed by SessionWriteNode. nil installs the
	// nilHistoryWriter stub which errors on every call so a graph that
	// fires session_write without wiring fails fast rather than
	// silently dropping the assistant turn.
	HistoryWriter HistoryWriter
	// Attachments resolves an attachment ID to a content block.
	Attachments AttachmentResolver
	// AttachmentRegistry is the optional registration half of the
	// attachment seam. read_file consults it when its `as_attachment`
	// attr is true; nil disables the as_attachment path (the executor
	// falls back to inline content).
	AttachmentRegistry AttachmentRegistrar
	// BashOutput is the cached-bash-output store, consulted by
	// read_bash_output. Nil installs a stub that always returns
	// ErrNoBashOutput.
	BashOutput BashOutputStore
	// Policy is the kernel-side filesystem/state gate. Nil installs
	// the AllowAll fallback (every check passes).
	Policy PolicyGate
	// Trace is an optional telemetry sink. Nil disables span emission.
	Trace TraceSink

	// AskInbox surfaces pending Ask questions and resumes them when the
	// user answers. Tests pass a fake; production wiring connects it
	// to the chat surface.
	Ask AskBus

	// StreamSink, when non-nil, receives streaming deltas (token text,
	// tool-use records, usage, finish reason, error) as the LLMNode's
	// upstream provider stream is consumed. The chassis wires a sink
	// that bridges to the existing `llm:stream-chunk` broker topic so
	// the chat surface keeps receiving byte-equal deltas without
	// needing a frontend change. Nil disables forwarding — the
	// LLMNode still lands its terminal response on the output ports;
	// only the per-token surface goes silent (kernel tests, scripted
	// runs, batch executions).
	StreamSink StreamSink

	// Budget aggregates the per-run hard caps (FR-048).
	Budget Budget
	// Counters track running budget consumption.
	Counters *RunCounters

	// In-memory ContextGraph — values produced so far this run, keyed
	// by (nodeID, portName). Executors can read upstream state here
	// without hauling it through inputs (e.g. Reflect wants the
	// recent trace).
	State *RunState

	// Hooks fires greedy memory-write hooks at kernel boundaries.
	Hooks *HookManager

	// LifecycleHooks fires v2 lifecycle hooks (pre_tool_use, post_tool_use,
	// etc.) via core/hooks.Runner. Nil disables v2 lifecycle hooks; the
	// existing memory-write hooks still fire through Hooks.
	LifecycleHooks LifecycleHookRunner

	// PendingContext receives additional system-context injected by hooks
	// (e.g. additional_context from pre_tool_use). Nil drops the context
	// (it is still logged but not forwarded to the next LLM turn).
	PendingContext PendingContextAppender

	// Compactor is the optional compaction pipeline (Bundle D). The
	// kernel calls it at LLMNode pre-call and ToolNode post-call
	// firing sites when the relevant threshold is crossed. Nil
	// disables compaction; the rest of the pipeline runs unchanged.
	Compactor Compactor

	// Branch is the BranchSeam for ForkNode/MergeNode. nil installs the
	// nilBranchSeam stub which errors with ErrNoBranchSeam — the
	// chat surface surfaces this as "branching not enabled in this build".
	Branch BranchSeam

	// JournalWriter persists memory hook journal entries. The kernel
	// threads it onto the lazily-constructed HookManager via
	// SetJournalWriter. nil disables on-disk persistence; the
	// HookManager's in-memory ring buffer keeps working.
	JournalWriter JournalWriter

	// Recommender suggests a (provider, model) for forks. nil disables
	// the recommendation chip; ForkNode falls back to ModelOverride or
	// the parent model.
	Recommender *BranchRecommender

	// MergeSuggester powers the kernel's "merge?" toast. nil disables
	// the suggestion stream.
	MergeSuggester *MergeSuggester

	// ToolSchemas maps tool name → JSON-Schema bytes for the tools the
	// model was given this run. Populated by the LLMProviderAdapter at
	// StartStream time from the discovered ToolSpec slice.
	//
	// The tool_dispatch executor reads this map at dispatch time to
	// validate model-supplied arguments before forwarding them to the
	// tool implementation (FR-006 / agent-loop-robustness-parity WP06).
	// nil means no schema validation — the executor degrades to well-
	// formedness-only checking (JSON must parse as an object).
	ToolSchemas map[string][]byte // toolName → JSON Schema bytes

	// ToolCallTimeout is the per-call deadline applied by tool_dispatch.
	// Zero means no timeout (the existing behaviour). When set, any tool
	// call that does not complete within this duration receives a timeout
	// is_error result so the model sees the failure rather than the loop
	// hanging indefinitely (FR-007 / agent-loop-robustness-parity WP07).
	ToolCallTimeout time.Duration

	// MutatingTools is the set of tool names that MUST NOT run concurrently
	// with other mutating tools (FR-007 mutation-safety). When non-empty,
	// the dispatcher serialises all calls whose name is in MutatingTools;
	// read-only tools still run in parallel up to MaxConcurrent.
	// nil means all tools are treated as read-only (parallel by default).
	MutatingTools map[string]bool

	// registry is the executor lookup table the control executors
	// (Loop, Retry, Parallel) use to dispatch into peer nodes. The
	// kernel sets this on entry to Run(); leaving it nil falls back
	// to a fresh package-default registry.
	registry *executorRegistry
}

// RunCounters track in-run budget consumption. Updated by executors
// (LLMNode bumps token + call counts; ToolNode bumps tool calls; etc.)
// and inspected by the kernel before each fire to enforce caps.
type RunCounters struct {
	mu             sync.Mutex
	LLMTokensUsed  int
	LLMCallsMade   int
	ToolCallsMade  int
	CostUSD        float64
	WallclockStart int64 // unix nanos
}

// AddLLM bumps the token + call counter atomically.
func (c *RunCounters) AddLLM(tokens int) {
	c.mu.Lock()
	c.LLMTokensUsed += tokens
	c.LLMCallsMade++
	c.mu.Unlock()
}

// AddTool bumps the tool counter.
func (c *RunCounters) AddTool() {
	c.mu.Lock()
	c.ToolCallsMade++
	c.mu.Unlock()
}

// AddCost bumps the running cost counter.
func (c *RunCounters) AddCost(usd float64) {
	c.mu.Lock()
	c.CostUSD += usd
	c.mu.Unlock()
}

// Snapshot returns a copy of the counter values.
func (c *RunCounters) Snapshot() (tokens, calls, tools int, cost float64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.LLMTokensUsed, c.LLMCallsMade, c.ToolCallsMade, c.CostUSD
}

// RunState is the in-memory ContextGraph projection — what each node
// produced this run, plus completion flags. It is the slice of
// EventLog state the executors actually need at runtime; the EventLog
// itself is the source of truth and can rebuild this on resume.
type RunState struct {
	mu        sync.RWMutex
	values    map[string]PortValues // nodeID → outputs
	completed map[string]bool       // nodeID → did it complete
	failed    map[string]error      // nodeID → error if it failed
	// userAnswer is set by the kernel when an AskNode resumes; the
	// AskNode reads it on its second fire.
	userAnswer string

	// toolCalls backs the doom-loop guard (autonomy-recovery-runtime-
	// 01PMDL03 WP02): a bounded LRU of (tool name, normalized-arg-hash)
	// repeat counts, scoped to this run. The pointer is fixed at
	// construction and never reassigned, so reads are safe without s.mu
	// — toolCallHistory guards its own internal state.
	toolCalls *toolCallHistory
}

// NewRunState returns a fresh state.
func NewRunState() *RunState {
	return &RunState{
		values:    make(map[string]PortValues),
		completed: make(map[string]bool),
		failed:    make(map[string]error),
		toolCalls: newToolCallHistory(doomLoopHistoryCapacity),
	}
}

// RecordToolCall registers one more occurrence of a (toolName, args) pair
// for the doom-loop guard and returns the updated repeat count (1 on
// first sighting). Bounded — see toolCallHistory / doomLoopHistoryCapacity.
// Safe to call with a nil *RunState (returns 1, i.e. "never repeated") so
// executors that fire without a run-scoped state (rare, mostly tests)
// degrade to "guard disabled" rather than panicking.
func (s *RunState) RecordToolCall(toolName string, args map[string]any) int {
	if s == nil || s.toolCalls == nil {
		return 1
	}
	return s.toolCalls.touch(doomLoopKey(toolName, args))
}

// SetOutputs records the outputs of a node fire.
func (s *RunState) SetOutputs(nodeID string, outputs PortValues) {
	s.mu.Lock()
	s.values[nodeID] = outputs.Clone()
	s.completed[nodeID] = true
	s.mu.Unlock()
}

// Outputs reads back a node's recorded outputs (zero map if none).
func (s *RunState) Outputs(nodeID string) PortValues {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.values[nodeID].Clone()
}

// Completed reports whether nodeID has been fully executed.
func (s *RunState) Completed(nodeID string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.completed[nodeID]
}

// MarkFailed records that a node fire produced an error.
func (s *RunState) MarkFailed(nodeID string, err error) {
	s.mu.Lock()
	s.failed[nodeID] = err
	s.mu.Unlock()
}

// Failed returns the recorded error for a node (nil if it succeeded
// or never fired).
func (s *RunState) Failed(nodeID string) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.failed[nodeID]
}

// SetUserAnswer is called by the kernel when an Ask resumes.
func (s *RunState) SetUserAnswer(answer string) {
	s.mu.Lock()
	s.userAnswer = answer
	s.mu.Unlock()
}

// UserAnswer reads the pending user answer (empty string when none).
func (s *RunState) UserAnswer() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.userAnswer
}

// AllCompleted returns the set of nodes that have been fired.
func (s *RunState) AllCompleted() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]string, 0, len(s.completed))
	for k, v := range s.completed {
		if v {
			out = append(out, k)
		}
	}
	return out
}

// ---- registry ----

// executorRegistry is the kind→Executor map. Executor instances are
// stateless; the kernel uses a single shared registry for the whole
// process.
type executorRegistry struct {
	byKind map[NodeKind]Executor
}

// newExecutorRegistry constructs the default registry with one
// implementation per kind. Tests can override via WithExecutor.
func newExecutorRegistry() *executorRegistry {
	r := &executorRegistry{byKind: make(map[NodeKind]Executor, 32)}
	// Compute (FR-029..FR-039).
	r.register(&modelExecutor{})
	r.register(&toolExecutor{})
	r.register(&toolDispatchExecutor{})
	r.register(&transformExecutor{})
	r.register(&activityExecutor{})
	r.register(&reflectExecutor{})
	r.register(&reviewExecutor{})
	r.register(&plannerExecutor{})
	r.register(&askExecutor{})
	r.register(&escalateExecutor{})
	r.register(&compactExecutor{})

	// Control (FR-040..FR-048).
	r.register(&decisionExecutor{})
	r.register(&parallelExecutor{})
	r.register(&joinExecutor{})
	r.register(&loopExecutor{})
	r.register(&retryExecutor{})
	r.register(&branchExecutor{})
	r.register(&mergeExecutor{})
	r.register(&approvalExecutor{})

	// State (FR-051..FR-058).
	r.register(&memoryExecutor{})
	r.register(&corpusReadExecutor{})
	r.register(&corpusWriteExecutor{})
	r.register(&attachmentExecutor{})
	r.register(&historyReadExecutor{})
	r.register(&traceWriteExecutor{})
	r.register(&checkpointExecutor{})
	r.register(&artifactExecutor{})
	r.register(&readFileExecutor{})
	r.register(&readBashOutputExecutor{})
	r.register(&writeFileExecutor{})
	r.register(&sessionWriteExecutor{})
	return r
}

func (r *executorRegistry) register(e Executor) {
	r.byKind[e.Kind()] = e
}

func (r *executorRegistry) lookup(k NodeKind) (Executor, error) {
	e, ok := r.byKind[k]
	if !ok {
		return nil, fmt.Errorf("agentgraph: no executor for kind %q", k)
	}
	return e, nil
}
