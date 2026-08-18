package agentgraph

import (
	"context"
	"errors"
	"fmt"
	"sort"
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
	// Backtrack, when non-nil, asks the kernel to rewind execution to
	// an earlier, already-completed node (autonomy-recovery-runtime-
	// 01PMDL03 WP01): clear its completed flag, re-queue it, and
	// re-fire everything downstream of it. This is the typed successor
	// to the legacy should_retry/retry_target Outputs convention
	// ReviewNode's fail path uses (exec_compute.go) — the kernel still
	// honors that convention for back-compat (see resolveBacktrack in
	// kernel.go), but new node kinds should set this field directly.
	Backtrack *BacktrackRequest
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

// EdgeRouter is the OPTIONAL half of the Executor contract: an executor
// implements it when the node kind it runs performs conditional
// execution, i.e. when completing the node must promote some of its
// out-edges and skip the rest (agentgraph-total-convergence-01PMGX01
// WP11a; design in agentic-turn-routing-01PMAG01 §3.2).
//
// This is the single seam for conditional promotion. The kernel's
// promotion worklist asks the completing node's executor for a
// predicate and treats a non-implementer — which is every kind except
// `decision` and `router`, plus every WithExecutor test override — as
// "all out-edges live", which is byte-for-byte today's traversal for
// every graph the repo ships.
//
// WHY AN INTERFACE RATHER THAN A `Result` FIELD. Two reasons, both
// load-bearing:
//
//  1. Liveness has to be re-derivable AFTER THE FACT. The kernel's
//     backtrack refire recompute rebuilds `liveIn` for the rewound set
//     from each predecessor's *recorded outputs*; the Result those
//     outputs came from was discarded when the node completed. Passing
//     (node, recorded outputs) here makes the same answer available at
//     both times, from state that survives.
//  2. Liveness is not always expressible as a set of live PORTS. WP02c
//     made `decision`'s next_true / next_false the primary routing
//     authority, so a graph may legally hang both successors off one
//     audit port and be routed by destination node — see
//     TestKernel_DecisionRoutesOnNextAttrsNotJustPorts. A predicate
//     over the whole Edge covers that; a []string of port names cannot.
//
// And why an interface rather than the kind switch WP02b shipped in
// kernel.go: the executor registry accepts registrations at run time
// (ExecutorRegistry.Register), so a kind the binary never heard of at
// compile time can still be dispatched. Such a kind can never add a
// case to a switch in the kernel, which would have made conditional
// routing permanently unavailable to exactly the extension path WP03
// built.
//
// Implementations MUST be pure with respect to (nd, outs): the kernel
// may call LiveOutEdges more than once for the same completion, and
// calls it from goroutines, so it must not mutate shared state.
type EdgeRouter interface {
	// LiveOutEdges returns a predicate reporting whether one out-edge
	// of nd delivered a live value, given the outputs nd recorded.
	// Returning nil means "every out-edge is live" — the same
	// unconditional promotion a non-implementer gets.
	LiveOutEdges(nd *Node, outs PortValues) func(Edge) bool
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

	// ToolResultCap bounds the bytes a single tool result may contribute
	// to the context, applied at dispatch before the result becomes a
	// Message (turn-context-runway-01PMAG03 WP01). The zero value
	// selects the package defaults (DefaultToolResultMaxBytes etc.); a
	// negative MaxBytes disables the cap.
	ToolResultCap ToolResultCap

	// ToolOutputArchive persists the full payload of a truncated tool
	// result so the elision marker can name a handle the model can
	// re-read. Nil keeps the truncation (bounding the context is the
	// point) but the marker says the bytes were not retained.
	ToolOutputArchive ToolOutputArchive
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

	// TaskState is the structured goal / completed-step summary /
	// forbidden-actions object (autonomy-recovery-runtime-01PMDL03
	// WP03) — re-injected as a pinned system-context block on every
	// compute re-entry via graphBaseOf (exec_compute.go). nil is
	// equivalent to "empty" (every TaskState method tolerates a nil
	// receiver); applyEnvDefaults installs an empty one so production
	// runs never see nil here. Mutated + persisted via the Kernel's
	// SetTaskGoal / AddTaskCompletedStep / AddTaskForbidden helpers
	// (kernel.go) so changes survive Kernel.RebuildState on resume.
	TaskState *TaskState

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

	// AutoCompaction is the growth watermark that gates the kernel's
	// automatic pre_call/post_tool compaction sites
	// (turn-context-runway-01PMAG03 WP02). It latches the run's live
	// token count on its first observation and admits an automatic site
	// only once the live count has grown past that baseline by the
	// policy margin — so the first pre-call site is a no-op by
	// construction (the double-fire compaction-convergence-01PMDL05
	// found) while a site fifteen iterations later, after the turn has
	// actually accumulated context, is free to fire.
	//
	// nil == no watermark == every automatic site is ungated, which is
	// the correct reading for a graph-authored run with no pre-send
	// compactor in front of it. The chat surface arms one on every Env
	// it builds; that is what replaced the unconditional suppression
	// boolean this Env used to carry.
	AutoCompaction *CompactionWatermark

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

	// TierSource resolves a model tier for (providerKind, modelID) pairs
	// (versioned-model-profile-01PMDL04 WP04/WP05). The Planner/Review/
	// Reflect executors consult it to derive a Verbosity / MaxIterations
	// default when a node leaves the attr unset, instead of hardcoding
	// one tier's behaviour for every model. nil (test runs, scripted
	// activities) makes every lookup resolve to ModelTierMedium via
	// resolveNodeTier's fallback — this is the pre-WP05 behaviour, so
	// omitting a TierSource never changes an existing graph's output.
	TierSource TierSource

	// ContextWindows resolves the active model's max context length so
	// the compaction pipeline can evaluate PreCallThreshold against a
	// real denominator (autonomy/compaction convergence). nil means
	// "unknown window", which makes the automatic compaction sites skip
	// — the safe reading, since compacting to a guessed target is worse
	// than not compacting.
	ContextWindows ContextWindowSource

	// PromptTemplates resolves the per-family prompt-rendering template
	// (llm.PromptTemplateRef, versioned-model-profile-01PMDL04) for a
	// node's active (providerKind, modelID) pair
	// (per-family-message-shaping-01PMDL06 WP01). Mirrors TierSource /
	// ContextWindows: the frozen core asks this data question through a
	// seam instead of string-matching model-family names itself. nil
	// (every run today — nothing resolves a live ModelProfile yet; see
	// resolvePromptTemplate) makes every lookup resolve to nil, which
	// selects prompts.Compose's default renderer — today's exact
	// behaviour, unchanged, until a concrete adapter is wired.
	PromptTemplates PromptTemplateSource

	// MergeSuggester powers the kernel's "merge?" toast
	// (branches:merge-suggested, consumed by useEventToasts.ts's
	// "Merge now" action). nil disables the suggestion stream — the
	// default for hand-built Envs and any Kernel.Run caller besides
	// the production chat wiring.
	//
	// Assigned by core/rpc/views/agentgraph.EnvDeps.MergeSuggester
	// (constructed at core/rpc/api.go's newGraphManagerWithDeps
	// alongside the Branch seam) and evaluated after a chat run
	// completes cleanly, not from inside this package: a branch's
	// child session is chatted through the ordinary ChatRunner path,
	// so "the child run finished" is an observable driveRun exit, not
	// a kernel-internal event. See ChatRunner.fireMergeSuggestion
	// (core/rpc/views/agentgraph/chat/merge_suggestion.go), gated on
	// BranchSeam.ActiveBranchForChildSession so it only fires for a
	// still-active branch's child session
	// (engineer-truth-pass-01PMTP01 WP08, finding B16).
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

	// NodeErrorPolicy governs how a Loop body node's failure is
	// handled (autonomy-knobs-live-01PMAG02 WP04, continueOnError).
	// The zero value (NodeErrorPolicyStop) is today's behaviour: the
	// error propagates immediately and terminates the run. Chat
	// wiring translates the resolved continueOnError autonomy knob
	// into this field at Env-construction time so core/agentgraph
	// itself stays autonomy-agnostic — any caller of Kernel.Run, not
	// just the chat path, can set it. See loopExecutor.Execute
	// (exec_control.go) for where this is consulted; ErrPaused and
	// ErrBudgetExceeded stay terminal under every policy.
	NodeErrorPolicy NodeErrorPolicy

	// TaskStateArming governs WHEN the kernel commits the run's goal
	// and completed-step trail to TaskState
	// (agentgraph-total-convergence-01PMGX01 WP11b; design in
	// agentic-turn-routing-01PMAG01 §3.5).
	//
	// The zero value is today's behaviour and stays the default for
	// every Kernel.Run caller: TaskState arms only once the run has hit
	// a node error or an honored backtrack, so a run that never fails
	// composes a byte-identical system prompt.
	//
	// TaskStateArmAlways is what a verified exit needs. `review` checks
	// the draft against the run's GOAL, and a run that succeeded all
	// the way to the exit gate has by definition never armed recovery —
	// so under the old rule the gate would be checking the answer
	// against nothing (01PMAG01 G5). This field is the seam that keeps
	// core/agentgraph autonomy- and settings-agnostic while letting the
	// chat surface tie the change to its launch gate: the routed
	// topology and always-armed TaskState flip together, so one
	// settings flag reverts both.
	TaskStateArming TaskStateArming

	// AskPolicy governs whether a kernel-side verification or recovery
	// step may turn a failure into a QUESTION for the user
	// (agentgraph-total-convergence-01PMGX01 WP11b, closing the
	// autonomy-knobs review's finding F7).
	//
	// The zero value is today's behaviour. AskPolicyWithhold is what
	// the chat surface sets when the resolved askOnAmbiguity knob is
	// `proceed` or `never` — the two modes that already withhold
	// kenaz__ask_user_question from the model's catalogue entirely
	// (chat.withholdsAskTool). Without this, those two modes had a hole
	// the size of the whole recovery path: the model could not ask, but
	// an exit-gate FAIL at its iteration cap, or an escalation ladder
	// reaching its final rung, would surface a question anyway — the
	// user having explicitly asked not to be interrupted, and the
	// question arriving from a code path they never saw. Under
	// Withhold, a FAIL re-enters the loop while budget remains and
	// otherwise returns the best draft.
	//
	// Same shape as NodeErrorPolicy: an autonomy-agnostic enum, so
	// core/agentgraph never imports the autonomy domain and any
	// Kernel.Run caller can set it.
	AskPolicy AskPolicy

	// registry is the executor lookup table the control executors
	// (Loop, Retry, Parallel) use to dispatch into peer nodes. The
	// kernel sets this on entry to Run(); leaving it nil falls back
	// to a fresh package-default registry.
	registry *ExecutorRegistry
}

// NodeErrorPolicy is the generic (autonomy-agnostic) enum
// core/agentgraph exposes for the continueOnError knob
// (autonomy-knobs-live-01PMAG02 WP04). Defined here rather than
// imported from core/autonomy so this package has no dependency on
// the autonomy domain — the chat surface (core/rpc/views/agentgraph/
// chat) is the only place that translates autonomy.ErrorMode into
// this type.
type NodeErrorPolicy string

const (
	// NodeErrorPolicyStop is the zero value and today's behaviour: the
	// first non-pause, non-budget body-node error terminates the run.
	NodeErrorPolicyStop NodeErrorPolicy = ""
	// NodeErrorPolicyRetryOnce re-fires the failed body node once,
	// with the same inputs, before falling back to Stop semantics.
	//
	// "Once" is per (loop-iteration, body-node) firing, not once for
	// the node's whole lifetime in this Loop: the retry attempt is not
	// tracked across iterations, so a body node that keeps failing on
	// every iteration gets one extra attempt on EACH iteration —
	// worst case 2×MaxIterations total executions of that node across
	// the run, not MaxIterations+1. The failed first attempt's error is
	// not discarded: loopExecutor appends an EventNodeError
	// (retried=true) for it before firing the retry, so the audit trail
	// shows both the original failure and (if the retry also fails) the
	// error that actually propagates.
	NodeErrorPolicyRetryOnce NodeErrorPolicy = "retry-once"
	// NodeErrorPolicyAdapt converts the body-node error into a message
	// folded into the loop's current payload and continues iterating,
	// letting the model route around the failure. ErrPaused,
	// ErrBudgetExceeded, and ctx cancellation/deadline stay terminal
	// regardless — see isTerminalNodeError in exec_control.go.
	NodeErrorPolicyAdapt NodeErrorPolicy = "adapt"
)

// TaskStateArming is the generic (autonomy- and settings-agnostic) enum
// core/agentgraph exposes for WHEN a run commits its goal and
// completed-step trail to TaskState. See Env.TaskStateArming.
type TaskStateArming string

const (
	// TaskStateArmOnFailure is the zero value and today's behaviour:
	// nothing is committed to TaskState until the run hits a node error
	// or an honored backtrack. A run that never fails therefore
	// composes a byte-identical system prompt, which is the invariant
	// TestKernel_ZeroFailureRunComposesByteIdenticalPrompt pins.
	TaskStateArmOnFailure TaskStateArming = ""
	// TaskStateArmAlways commits the goal as soon as it is derivable
	// and every meaningful node completion as it happens, on success
	// paths as well as failure ones. This is what makes a completion
	// check possible: without it, a run that succeeded has no recorded
	// goal for the exit gate to check the answer against (01PMAG01 G5).
	//
	// The goal is still derived LAZILY where the run already loads the
	// history: a graph carrying a `history_read` node (every chat
	// graph) arms off that node's own output rather than issuing a
	// second unbounded History(..., 0) read. Only a graph with no
	// history_read pays a read of its own, and in that case nothing
	// else was going to read it.
	TaskStateArmAlways TaskStateArming = "always"
)

// AskPolicy is the generic (autonomy-agnostic) enum core/agentgraph
// exposes for "may this run interrupt the user with a question?".
// See Env.AskPolicy.
type AskPolicy string

const (
	// AskPolicyAllow is the zero value and today's behaviour: a
	// verification or recovery step may escalate into a question.
	AskPolicyAllow AskPolicy = ""
	// AskPolicyWithhold forbids it. Verification still runs and still
	// rewinds; what it may not do is convert a verdict into a prompt
	// for the user. Callers set this when the user has said, through
	// whatever dial they own, that they do not want to be asked.
	AskPolicyWithhold AskPolicy = "withhold"
)

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
	// BacktracksUsed counts kernel backtrack primitive rewinds fired
	// this run (autonomy-recovery-runtime-01PMDL03 WP01). See
	// AddBacktrack / BacktracksSnapshot.
	BacktracksUsed int
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

// AddBacktrack bumps the run's backtrack counter and returns the new
// total (autonomy-recovery-runtime-01PMDL03 WP01). Kept on RunCounters
// rather than RunState so it lives alongside the other budget-style
// counters checkBudget's pattern mirrors, even though the kernel checks
// it reactively (at backtrack time) rather than pre-emptively at fire
// time like checkBudget's other caps.
func (c *RunCounters) AddBacktrack() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.BacktracksUsed++
	return c.BacktracksUsed
}

// BacktracksSnapshot returns the current backtrack count.
func (c *RunCounters) BacktracksSnapshot() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.BacktracksUsed
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

	// failureAnnotations accumulates the structured rejection records
	// the kernel backtrack primitive attaches on every rewind
	// (autonomy-recovery-runtime-01PMDL03 WP01). Read by graphBaseOf
	// (exec_compute.go) so every compute executor re-grounds its next
	// model call with explicit memory of what was already tried and
	// rejected.
	failureAnnotations []FailureAnnotation
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

// AddFailureAnnotation appends a backtrack rejection record. Called by
// the kernel (kernel.go) when a BacktrackRequest is honored.
func (s *RunState) AddFailureAnnotation(ann FailureAnnotation) {
	s.mu.Lock()
	s.failureAnnotations = append(s.failureAnnotations, ann)
	s.mu.Unlock()
}

// FailureAnnotations returns a snapshot copy of the accumulated
// backtrack rejection records, oldest first.
func (s *RunState) FailureAnnotations() []FailureAnnotation {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]FailureAnnotation, len(s.failureAnnotations))
	copy(out, s.failureAnnotations)
	return out
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

// CompareAndSetOnce atomically claims a boolean "fired" flag stored
// under key, returning true only for the caller that flips it from
// unset to set (every subsequent caller sees it already set and gets
// false). Added for the escalation ladder's run-global escalation gate
// (autonomy-recovery-runtime-01PMDL03 WP04 review follow-up): the
// prior code read Outputs(key), ran a slow, unlocked
// env.LLM.Generate call, and only then wrote the flag back — a TOCTOU
// window in which two concurrent EscalationLadder nodes could both
// observe "not yet escalated" and both fire a real, costed escalation
// call against the run's single shared slot.
//
// Callers must claim the flag with this method *before* starting the
// gated work, and call ReleaseOnce if that work subsequently fails, so
// a failed attempt doesn't permanently strand the slot for the rest of
// the run.
func (s *RunState) CompareAndSetOnce(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if fired, _ := s.values[key]["fired"].(bool); fired {
		return false
	}
	s.values[key] = PortValues{"fired": true}
	s.completed[key] = true
	return true
}

// ReleaseOnce undoes a CompareAndSetOnce claim. Intended for a caller
// whose gated work failed after claiming the flag: the slot must
// remain available for the next attempt (this node's own retry, or
// another node racing for the same key) rather than being permanently
// burned by one failed call.
func (s *RunState) ReleaseOnce(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.values, key)
	delete(s.completed, key)
}

// ---- registry ----
//
// agentgraph-total-convergence-01PMGX01 WP03 (spec §4.2).
//
// Executor dispatch used to be a hardcoded table built once inside
// newExecutorRegistry() and never touched again. That table was the
// blocker for the mission's "tools are nodes" goal: builtin tools are
// compile-time known and can be code-generated into it, but **MCP tool
// schemas arrive over stdio after process start** and therefore cannot
// be build-time manifests. The resolution the spec mandates is a
// shared, runtime-extensible registry rather than a second dispatch
// mechanism living alongside the first.
//
// So the table is now the *seed* of an ExecutorRegistry that accepts
// registrations at any time:
//
//   - build-time / static kinds feed it at construction, via
//     builtinExecutors();
//   - runtime discovery feeds it through Kernel.Executors().Register(),
//     which is safe to call while the kernel is running (every lookup
//     takes an RLock).
//
// Registration is duplicate-rejecting on purpose: two executors
// claiming one NodeKind is exactly the "parallel implementations of the
// same concept" failure this mission exists to eliminate, and silently
// letting the second win is how it stays invisible. Deliberate
// replacement — the WithExecutor test/override seam — goes through the
// explicitly-named Replace instead, so an override reads as an override
// at the call site.
//
// NOTE for a future WP: materializing MCP-discovered tools as node
// kinds also needs a manifest/attrs path (ResolvedManifests + a decoder
// for the kind's attrs). This WP builds the executor half of that seam
// only; it does not implement MCP discovery.

// ErrExecutorRegistered is returned by ExecutorRegistry.Register when a
// kind already has an executor. Callers that mean to replace an
// existing binding must say so via Replace.
var ErrExecutorRegistered = errors.New("agentgraph: executor already registered for kind")

// ExecutorRegistry is the kind→Executor map. Executor instances are
// stateless; one registry is shared by a kernel and every nested
// dispatch inside its run (the kernel pins it onto Env.registry).
//
// All methods are safe for concurrent use: the kernel fires nodes from
// bare goroutines, so a runtime Register racing an in-flight run must
// not corrupt the map.
type ExecutorRegistry struct {
	mu     sync.RWMutex
	byKind map[NodeKind]Executor
}

// NewExecutorRegistry returns an empty registry. Callers that want the
// shipped static kinds should use the kernel (NewKernel seeds one) or
// seed it themselves from builtinExecutors().
func NewExecutorRegistry() *ExecutorRegistry {
	return &ExecutorRegistry{byKind: make(map[NodeKind]Executor, 40)}
}

// builtinExecutors returns the build-time executor set: one instance
// per statically-declared (manifest-backed) node kind. This is the list
// codegen would emit if/when the manifest `executor:` field becomes
// generated dispatch; until then it is hand-maintained and pinned by
// the I1 convergence gate (convergence_gates_test.go), which fails when
// a manifest kind has no entry here.
func builtinExecutors() []Executor {
	out := []Executor{
		// Compute (FR-029..FR-039).
		&modelExecutor{},
		&toolDispatchExecutor{},
		&transformExecutor{},
		&activityExecutor{},
		&reflectExecutor{},
		&reviewExecutor{},
		&plannerExecutor{},
		&routerExecutor{},
		&askExecutor{},
		&escalateExecutor{},
		&compactExecutor{},

		// Control (FR-040..FR-048).
		&decisionExecutor{},
		&parallelExecutor{},
		&joinExecutor{},
		&loopExecutor{},
		&retryExecutor{},
		&branchExecutor{},
		&mergeExecutor{},
		&approvalExecutor{},
		&escalationLadderExecutor{},

		// State (FR-051..FR-058).
		&memoryExecutor{},
		&corpusReadExecutor{},
		&corpusWriteExecutor{},
		&attachmentExecutor{},
		&historyReadExecutor{},
		&traceWriteExecutor{},
		&checkpointExecutor{},
		&artifactExecutor{},
		&readFileExecutor{},
		&readBashOutputExecutor{},
		&writeFileExecutor{},
		&sessionWriteExecutor{},
	}

	// Builtin tool kinds (the `tool` archetype). Derived from the
	// resolved manifest catalog rather than hand-listed: WP04's contract
	// is that declaring a manifest with `extends: tool` is all it takes
	// to get a node kind with a working executor. A hand-maintained
	// second list here is exactly the drift the I1/I2 gates exist to
	// catch, so there isn't one.
	return append(out, builtinToolExecutors()...)
}

// ToolArchetypeID is the manifest ID of the shared builtin-tool
// archetype (_archetype.tool.yaml). A callable kind whose inheritance
// chain contains it is a tool node.
const ToolArchetypeID = "tool"

// builtinToolExecutors returns one builtinToolExecutor per callable
// kind that extends the `tool` archetype, sorted by kind for
// deterministic registration order.
func builtinToolExecutors() []Executor {
	kinds := make([]NodeKind, 0, 4)
	for kind, rm := range ResolvedManifests {
		if rm == nil {
			continue
		}
		for _, ancestor := range rm.Chain {
			if ancestor == ToolArchetypeID {
				kinds = append(kinds, kind)
				break
			}
		}
	}
	sort.Slice(kinds, func(i, j int) bool { return kinds[i] < kinds[j] })
	out := make([]Executor, 0, len(kinds))
	for _, k := range kinds {
		out = append(out, builtinToolExecutor{kind: k, toolName: builtinToolNameFor(k)})
	}
	return out
}

// newExecutorRegistry constructs a registry seeded with every static
// kind. A duplicate in builtinExecutors() is a build-time programming
// error, so it panics rather than returning an error nobody can act on.
func newExecutorRegistry() *ExecutorRegistry {
	r := NewExecutorRegistry()
	for _, e := range builtinExecutors() {
		r.MustRegister(e)
	}
	return r
}

// Register binds e to its Kind(). It returns ErrExecutorRegistered when
// the kind already has an executor — the registry never silently
// replaces a binding. This is the API runtime discovery (MCP tools)
// calls; it is safe to call while a run is in flight.
func (r *ExecutorRegistry) Register(e Executor) error {
	if e == nil {
		return errors.New("agentgraph: register: nil executor")
	}
	k := e.Kind()
	if k == "" {
		return errors.New("agentgraph: register: executor reports empty kind")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.byKind[k]; exists {
		return fmt.Errorf("%w: %q", ErrExecutorRegistered, k)
	}
	r.byKind[k] = e
	return nil
}

// MustRegister is Register for callers that treat a duplicate as a
// programming error (the static seed set).
func (r *ExecutorRegistry) MustRegister(e Executor) {
	if err := r.Register(e); err != nil {
		panic(err.Error())
	}
}

// Replace binds e to its Kind(), overwriting any existing binding. This
// is the deliberate-override path behind WithExecutor; production
// wiring should use Register so a collision surfaces.
func (r *ExecutorRegistry) Replace(e Executor) {
	if e == nil || e.Kind() == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.byKind[e.Kind()] = e
}

// Lookup returns the executor bound to k, and whether one exists.
func (r *ExecutorRegistry) Lookup(k NodeKind) (Executor, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	e, ok := r.byKind[k]
	return e, ok
}

// Has reports whether k has a registered executor. This is the query
// the I1 convergence gate uses.
func (r *ExecutorRegistry) Has(k NodeKind) bool {
	_, ok := r.Lookup(k)
	return ok
}

// Len returns the number of registered kinds.
func (r *ExecutorRegistry) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.byKind)
}

// Kinds returns every registered kind, sorted, for diagnostics and the
// convergence gates.
func (r *ExecutorRegistry) Kinds() []NodeKind {
	r.mu.RLock()
	out := make([]NodeKind, 0, len(r.byKind))
	for k := range r.byKind {
		out = append(out, k)
	}
	r.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// lookup is the kernel-internal accessor that keeps the historical
// error string ("no executor for kind %q") the kernel surfaces to
// callers and tests.
func (r *ExecutorRegistry) lookup(k NodeKind) (Executor, error) {
	e, ok := r.Lookup(k)
	if !ok {
		return nil, fmt.Errorf("agentgraph: no executor for kind %q", k)
	}
	return e, nil
}
