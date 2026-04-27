package agentgraph

import (
	"context"
	"errors"
	"sync"
	"time"
)

// This file defines the narrow interfaces the kernel calls into. Each
// is a seam: production wiring binds it to the real subsystem
// (`core/llm`, `core/memory`, etc.), tests bind a fake.
//
// Note: keeping the interfaces here (instead of in core/llm/etc.)
// avoids reverse-direction imports. core/agentgraph imports core/llm
// for value types; core/llm has no idea this package exists.

// ---- LLM ----

// LLMRequest is the agentgraph-level representation of one LLM call.
// Kept narrow: provider/model selection, system prompt, message
// history, optional tool allow-list. Provider-specific knobs (caching,
// reasoning, JSON mode) are left to the production wiring layer to
// translate from ModelAttrs into core/llm.GenerationRequest.
type LLMRequest struct {
	Provider     string
	Model        string
	SystemPrompt string
	Messages     []Message
	Tools        []string
	MaxTokens    int
	Temperature  *float64
}

// Message is the narrow chat-message shape the kernel works with.
// Mirrors core/llm.Message for value types but lives here to avoid
// pulling the entire llm package into every seam.
type Message struct {
	Role    string // "system" | "user" | "assistant" | "tool"
	Content string
	// Name is optional (tool name for role=tool, etc.).
	Name string
	// ToolCalls is the (subset of) tool-call records the assistant
	// emitted; populated by LLMProvider on responses.
	ToolCalls []ToolCallRequest
}

// ToolCallRequest is one model-emitted tool invocation.
type ToolCallRequest struct {
	ID        string
	Name      string
	Arguments string // JSON-encoded args
}

// LLMResponse is the narrow result returned to the kernel.
type LLMResponse struct {
	Content      string
	FinishReason string
	ToolCalls    []ToolCallRequest
	TokensUsed   int
	CostUSD      float64
}

// LLMProvider is the agentgraph-side seam onto a `core/llm.Registry`.
type LLMProvider interface {
	Generate(ctx context.Context, req LLMRequest) (LLMResponse, error)
}

// LLMProviderFunc adapts a function to the LLMProvider interface.
type LLMProviderFunc func(ctx context.Context, req LLMRequest) (LLMResponse, error)

// Generate satisfies LLMProvider.
func (f LLMProviderFunc) Generate(ctx context.Context, req LLMRequest) (LLMResponse, error) {
	return f(ctx, req)
}

// ---- Tools ----

// ToolCall is the agentgraph-side request to dispatch a tool.
type ToolCall struct {
	Name string
	Args map[string]any
}

// ToolResult is what a tool dispatch returns.
type ToolResult struct {
	Content string
	IsError bool
}

// ToolRegistry is the agentgraph-side seam onto MCP tool dispatch.
type ToolRegistry interface {
	// Has reports whether the tool is registered.
	Has(name string) bool
	// Call executes the tool. Implementations are responsible for any
	// permission gate (Cedar etc.) — the kernel does not duplicate it.
	Call(ctx context.Context, call ToolCall) (ToolResult, error)
}

// ---- Memory ----

// MemoryWrite is one write request the kernel issues to the memory
// store. Hooks issue a stream of these on every kernel boundary
// (FR-027).
type MemoryWrite struct {
	Scope     string // "global" | "project" | "session"
	ScopeID   string
	SessionID string
	ProjectID string
	Content   string
	Title     string
	Source    string // "post-llm", "post-tool", etc.
	// Pinned chunks survive the prune sweep.
	Pinned bool
}

// MemoryReadFilter narrows a memory read.
type MemoryReadFilter struct {
	Scopes  []string // which scopes to query (default: all)
	Query   string   // free-text query (embedding-driven retrieval)
	TopK    int
}

// MemoryHit is one returned chunk.
type MemoryHit struct {
	ID         string
	Content    string
	Title      string
	ScopeKind  string
	ScopeID    string
	Similarity float32
}

// MemoryStore is the kernel-side write/read seam.
type MemoryStore interface {
	Write(ctx context.Context, w MemoryWrite) (id string, deduped bool, err error)
	Read(ctx context.Context, f MemoryReadFilter) ([]MemoryHit, error)
}

// ---- Activities ----

// ActivityCatalog resolves an activity ID to its sub-graph spec. The
// activity *catalog* itself is WP05; this seam exists so an
// ActivityNode executor can spawn the sub-graph in WP02.
type ActivityCatalog interface {
	Resolve(activityID, version string) (*Graph, error)
}

// ---- Corpus ----

// CorpusBackend is the seam onto Bundle C's corpus subsystem. Nil
// disables corpus_read / corpus_write (the executors return
// ErrNotImplemented).
type CorpusBackend interface {
	Reader
	Writer
}

// Reader is the read half (split so Bundle C can implement either side
// independently in tests).
type Reader interface {
	Search(ctx context.Context, ids []string, query string, topK int) ([]CorpusHit, error)
}

// Writer is the write half.
type Writer interface {
	Enqueue(ctx context.Context, corpusID, sourcePath string) (jobID string, err error)
}

// CorpusHit is one retrieval result.
type CorpusHit struct {
	CorpusID   string
	SourcePath string
	ByteOffset int
	Score      float32
	Snippet    string
}

// ---- History ----

// HistoryReader returns the N most-recent messages for a session.
type HistoryReader interface {
	History(ctx context.Context, sessionID string, n int) ([]Message, error)
}

// HistoryReaderFunc adapts a function.
type HistoryReaderFunc func(ctx context.Context, sessionID string, n int) ([]Message, error)

// History satisfies HistoryReader.
func (f HistoryReaderFunc) History(ctx context.Context, sessionID string, n int) ([]Message, error) {
	return f(ctx, sessionID, n)
}

// ---- Attachments ----

// AttachmentResolver wraps `core/attachments` for the AttachmentNode.
type AttachmentResolver interface {
	Resolve(ctx context.Context, attachmentID string) (AttachmentBlock, error)
}

// AttachmentBlock is the resolved content the AttachmentNode flows on
// its output port. The kernel passes it through opaquely; the
// downstream LLMNode is responsible for converting it to a
// content block in its provider request.
type AttachmentBlock struct {
	MIME    string
	Data    []byte
	URI     string
	Title   string
	Inline  bool
}

// ---- Trace ----

// TraceSink is a thin OTel-shaped sink. The kernel calls Span on every
// node fire so the existing telemetry stack picks the spans up.
type TraceSink interface {
	Span(ctx context.Context, name string, attrs map[string]any) (context.Context, func(error))
}

// noopTrace is the default when no telemetry is configured.
type noopTrace struct{}

// Span is a no-op span.
func (noopTrace) Span(ctx context.Context, _ string, _ map[string]any) (context.Context, func(error)) {
	return ctx, func(error) {}
}

// ---- Ask ----

// AskBus surfaces a pending question to the chat surface and waits
// for the user's answer. The kernel returns control to its caller
// (Run returns ErrPaused) once Ask is invoked; resumption happens via
// Resume(runID, answer) on the kernel.
type AskBus interface {
	// Pending records that an Ask is waiting. Production wiring pushes
	// to the chat surface and persists; tests record the question.
	Pending(ctx context.Context, runID, nodeID, question string) error
	// LookupAnswer returns the answer the user provided, with ok=true
	// when one is available. The Ask executor calls this on its
	// second fire (after Run resumes). Empty answer + ok=false means
	// the run is still parked.
	LookupAnswer(ctx context.Context, runID, nodeID string) (string, bool)
}

// memAskBus is the in-memory default.
type memAskBus struct {
	mu      sync.Mutex
	pending map[string]string // (runID, nodeID) -> question
	answers map[string]string // (runID, nodeID) -> answer
}

// NewMemAskBus returns a process-local AskBus suitable for tests.
func NewMemAskBus() *memAskBus {
	return &memAskBus{pending: map[string]string{}, answers: map[string]string{}}
}

// Pending records a pending question.
func (b *memAskBus) Pending(_ context.Context, runID, nodeID, q string) error {
	b.mu.Lock()
	b.pending[runID+":"+nodeID] = q
	b.mu.Unlock()
	return nil
}

// LookupAnswer reads back any provided answer.
func (b *memAskBus) LookupAnswer(_ context.Context, runID, nodeID string) (string, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	a, ok := b.answers[runID+":"+nodeID]
	return a, ok
}

// Answer is a test helper that injects a user answer.
func (b *memAskBus) Answer(runID, nodeID, ans string) {
	b.mu.Lock()
	b.answers[runID+":"+nodeID] = ans
	b.mu.Unlock()
}

// PendingQuestion is a test helper.
func (b *memAskBus) PendingQuestion(runID, nodeID string) (string, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	q, ok := b.pending[runID+":"+nodeID]
	return q, ok
}

// ---- TransformRegistry ----

// TransformFunc is a single named pure transform.
type TransformFunc func(ctx context.Context, in PortValues, params map[string]any) (PortValues, error)

// TransformRegistry maps transform names to implementations. The
// registry is goroutine-safe so test setup can register helpers from
// multiple goroutines.
type TransformRegistry struct {
	mu  sync.RWMutex
	fns map[string]TransformFunc
}

// NewTransformRegistry returns an empty registry. Call BuiltinTransforms
// to install the WP02 default transforms (concat, json_extract, etc.).
func NewTransformRegistry() *TransformRegistry {
	return &TransformRegistry{fns: make(map[string]TransformFunc)}
}

// Register adds a named transform. Last-write-wins on collision.
func (r *TransformRegistry) Register(name string, fn TransformFunc) {
	r.mu.Lock()
	r.fns[name] = fn
	r.mu.Unlock()
}

// Lookup returns the transform with the given name.
func (r *TransformRegistry) Lookup(name string) (TransformFunc, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	fn, ok := r.fns[name]
	return fn, ok
}

// Names returns the registered transform names (sorted).
func (r *TransformRegistry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.fns))
	for k := range r.fns {
		out = append(out, k)
	}
	return out
}

// ---- Compactor ----

// CompactionSite labels the kernel firing point that triggers a
// compaction call. Mirrors the Site enum in core/agentgraph/compaction
// without importing it (keeps the cycle direction one-way).
type CompactionSite string

const (
	// CompactionSitePreCall fires before an LLMNode dispatches its
	// request when the prepared input would exceed the token budget
	// threshold (FR-041).
	CompactionSitePreCall CompactionSite = "pre_call"
	// CompactionSitePostTool fires after a ToolNode returns when the
	// result payload exceeds tool_result_max_bytes (FR-041).
	CompactionSitePostTool CompactionSite = "post_tool"
	// CompactionSiteManual fires from the user-facing manual trigger.
	CompactionSiteManual CompactionSite = "manual"
)

// CompactionInput is the value-shape the kernel hands to a Compactor
// at a fire site. The kernel populates it from the active LLM request
// or tool result; the Compactor returns its compacted equivalent.
type CompactionInput struct {
	// Site identifies the firing point.
	Site CompactionSite
	// RunID + NodeID flag the originating node for telemetry.
	RunID  string
	NodeID string
	// SessionID + ProjectID drive cascading-config resolution.
	SessionID string
	ProjectID string
	// SystemPrompt is preserved untouched across compaction.
	SystemPrompt string
	// Messages is the slice the compactor will shrink. The kernel
	// passes the LLM-bound messages at pre_call; at post_tool the
	// slice contains a single message representing the tool result.
	Messages []Message
	// TargetTokens is the upper bound the compactor must aim to land
	// under. Zero means "let the compactor pick from cascading config".
	TargetTokens int
	// CurrentTokens is the kernel's estimate of the input size.
	CurrentTokens int
}

// CompactionOutput is what the compactor returns. Skipped=true means
// the input is unchanged (e.g. cascading config disabled the site,
// or recursion-depth tripped). Strategies do not surface errors for
// the recursion bound; the kernel treats Skipped as a soft no-op.
type CompactionOutput struct {
	Messages    []Message
	TokensAfter int
	Skipped     bool
	Reason      string
}

// Compactor is the kernel-side seam for the configurable compaction
// subsystem (mission agent-kernel-graph; Bundle D). Production wiring
// binds this to *compaction.Pipeline. Nil disables compaction without
// breaking the kernel.
type Compactor interface {
	// Compact runs one compaction request at the given site. The
	// returned CompactionOutput carries either the compacted
	// messages or a Skipped=true passthrough.
	Compact(ctx context.Context, in CompactionInput) (CompactionOutput, error)
}

// ---- Sentinels for stub seams ----

// ErrNoLLM is returned by the default LLMProvider stub.
var ErrNoLLM = errors.New("agentgraph: no LLM provider configured")

// ErrNoTools is returned by the default ToolRegistry stub.
var ErrNoTools = errors.New("agentgraph: no tool registry configured")

// ErrNoMemory is returned by the default MemoryStore stub.
var ErrNoMemory = errors.New("agentgraph: no memory store configured")

// ErrNoCorpus is returned by the default CorpusBackend stub.
var ErrNoCorpus = errors.New("agentgraph: no corpus backend configured")

// ErrNoActivities is returned by the default ActivityCatalog stub.
var ErrNoActivities = errors.New("agentgraph: no activity catalog configured")

// nilLLM is a placeholder LLMProvider that errors on every call.
type nilLLM struct{}

func (nilLLM) Generate(_ context.Context, _ LLMRequest) (LLMResponse, error) {
	return LLMResponse{}, ErrNoLLM
}

// nilTools is a placeholder ToolRegistry that has no tools.
type nilTools struct{}

func (nilTools) Has(string) bool { return false }
func (nilTools) Call(_ context.Context, _ ToolCall) (ToolResult, error) {
	return ToolResult{}, ErrNoTools
}

// nilMemory is a placeholder MemoryStore that errors on every call.
type nilMemory struct{}

func (nilMemory) Write(_ context.Context, _ MemoryWrite) (string, bool, error) {
	return "", false, ErrNoMemory
}
func (nilMemory) Read(_ context.Context, _ MemoryReadFilter) ([]MemoryHit, error) {
	return nil, ErrNoMemory
}

// nilActivities is a placeholder ActivityCatalog that has no activities.
type nilActivities struct{}

func (nilActivities) Resolve(_, _ string) (*Graph, error) { return nil, ErrNoActivities }

// nilCorpus stubs the corpus seam for builds that haven't shipped Bundle C.
type nilCorpus struct{}

func (nilCorpus) Search(_ context.Context, _ []string, _ string, _ int) ([]CorpusHit, error) {
	return nil, ErrNoCorpus
}
func (nilCorpus) Enqueue(_ context.Context, _, _ string) (string, error) {
	return "", ErrNoCorpus
}

// nilHistory returns an empty history.
type nilHistory struct{}

func (nilHistory) History(_ context.Context, _ string, _ int) ([]Message, error) {
	return nil, nil
}

// nilAttachments returns ErrNotImplemented.
type nilAttachments struct{}

func (nilAttachments) Resolve(_ context.Context, _ string) (AttachmentBlock, error) {
	return AttachmentBlock{}, ErrNotImplemented
}

// applyEnvDefaults fills missing seams with safe stubs so executors
// don't need to nil-check every dependency.
func applyEnvDefaults(env *Env) {
	if env.LLM == nil {
		env.LLM = nilLLM{}
	}
	if env.Tools == nil {
		env.Tools = nilTools{}
	}
	if env.Memory == nil {
		env.Memory = nilMemory{}
	}
	if env.Activities == nil {
		env.Activities = nilActivities{}
	}
	if env.Corpus == nil {
		env.Corpus = nilCorpus{}
	}
	if env.History == nil {
		env.History = nilHistory{}
	}
	if env.Attachments == nil {
		env.Attachments = nilAttachments{}
	}
	if env.Trace == nil {
		env.Trace = noopTrace{}
	}
	if env.Ask == nil {
		env.Ask = NewMemAskBus()
	}
	if env.Counters == nil {
		env.Counters = &RunCounters{WallclockStart: time.Now().UnixNano()}
	}
	if env.State == nil {
		env.State = NewRunState()
	}
	if env.Hooks == nil {
		env.Hooks = NewHookManager(env.Memory, env.SessionID, env.ProjectID)
	}
	if env.Transforms == nil {
		env.Transforms = NewTransformRegistry()
		BuiltinTransforms(env.Transforms)
	}
	// Compactor is intentionally not stubbed: nil compactor === no-op
	// at every fire site. Production wiring installs the real
	// pipeline; tests that don't care leave it nil.
	if env.Branch == nil {
		env.Branch = nilBranchSeam{}
	}
}
