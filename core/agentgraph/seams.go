package agentgraph

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/elicitation"
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

	// The following fields widen the ModelAttrs knob surface
	// (model-request-path-live-01PMDL01 WP05): the rest of
	// core/llm.RequestKnobs plus stop sequences. Nil/zero means "no
	// override; let the provider/profile default apply," matching
	// MaxTokens/Temperature's existing zero-value semantics.
	TopP              *float64
	TopK              *int
	FrequencyPenalty  *float64
	PresencePenalty   *float64
	Seed              *int
	ParallelToolCalls *bool
	StopSequences     []string

	// ReasoningBudgetTokens threads the model node's reasoning_budget_tokens
	// attr (model-request-path-live-01PMDL01 WP06b) through to
	// GenerationRequest.Reasoning. Nil means "no override; reasoning stays
	// off unless the profile/provider default enables it," matching the
	// rest of this knob surface's zero-means-unset convention.
	ReasoningBudgetTokens *int

	// ResponseSchema carries the model node's json_schema attr
	// (structured-output-is-reachable-01PMZE14 WP02) through to
	// GenerationRequest.ResponseFormat. Empty/nil means "no override; the
	// call is free-form text," matching this seam's other zero-means-unset
	// fields. json.RawMessage rather than map[string]any because
	// llm.ResponseFormat.Schema is already json.RawMessage and
	// structured.InjectAdditionalProperties consumes bytes — the marshal
	// from the authored map happens once, at the executor, not once per
	// adapter.
	ResponseSchema json.RawMessage

	// FallbackChainId carries the model/escalate node's fallback_chain_id
	// attr (model-request-path-live-01PMDL01 WP07) through to the
	// Generate() seam. When non-empty, Generate() routes the call through
	// a fallback.Runner instead of calling the registry directly. Empty
	// means "no node-level override" — the existing direct-registry path
	// is unchanged.
	FallbackChainId string

	// StreamToChat carries ModelAttrs.stream_to_chat down to the
	// provider seam (model-moves-transcript-01PMCH01 WP02).
	//
	// The attr has existed since the chat migration and had no reader:
	// it declared "this node's output IS the chat's assistant turn" and
	// nothing acted on it. It is the discriminator the chat runner
	// needs. env.LLM.Generate is called by SEVEN different executors —
	// the model node, the fused router, the review gate, both rungs of
	// the escalation ladder, the summarise compaction strategy and the
	// task-state gate — and only the first of those produces text the
	// user is reading. Recording every Generate as a transcript move
	// would persist the exit gate's private verdict as an assistant
	// message.
	//
	// Only modelExecutor sets it, from the node's own attr; every other
	// call site leaves it false and is therefore invisible to the move
	// journal.
	StreamToChat bool
}

// Message is the narrow chat-message shape the kernel works with.
// Mirrors core/llm.Message for value types but lives here to avoid
// pulling the entire llm package into every seam.
type Message struct {
	Role    string // "system" | "user" | "assistant" | "tool"
	Content string
	// Name is optional (tool name for role=tool, etc.).
	Name string
	// ToolCallID links a tool-result message back to the assistant's
	// tool_use that produced it. Required for OpenAI Chat Completions
	// wire format (`tool_call_id` field on tool messages); without it
	// upstream providers reject multi-turn tool conversations because
	// the dangling tool_use has no matching tool result.
	ToolCallID string
	// ToolCalls is the (subset of) tool-call records the assistant
	// emitted; populated by LLMProvider on responses.
	ToolCalls []ToolCallRequest
	// IsError marks a Role="tool" message as carrying a failed tool
	// result (tool-error-legibility-01PMDL02 WP01). Threaded from
	// ToolResult.IsError in exec_dispatch.go so the LLM provider adapter
	// can surface it on the wire (Anthropic tool_result.is_error, etc.)
	// instead of silently rendering a failure as a normal success.
	IsError bool
}

// ToolCallRequest is one model-emitted tool invocation.
type ToolCallRequest struct {
	ID        string
	Name      string
	Arguments string // JSON-encoded args
	// IsError is set only on the ToolCallRequest a HistoryEntry of kind
	// "tool_result" carries, where it means "the call this entry answers
	// failed" (model-moves-transcript-01PMCH01 WP04). It is the durable
	// half of the chat chip's running→ok/error transition, so a reloaded
	// session shows the same failure the live view showed. Always false
	// on a request the model actually issued.
	IsError bool
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

// StreamEventKind classifies a stream-bridge chunk. Mirrors the
// core/llm.StreamEventKind enum exactly so the chassis adapter can
// translate by string-equality without importing core/llm into the
// kernel (DIRECTIVE_001).
type StreamEventKind string

const (
	StreamEventText      StreamEventKind = "text"
	StreamEventTool      StreamEventKind = "tool"
	StreamEventReasoning StreamEventKind = "reasoning"
	StreamEventUsage     StreamEventKind = "usage"
	StreamEventFinish    StreamEventKind = "finish"
	StreamEventError     StreamEventKind = "error"
	// StreamEventMoveStart opens a new model move on the stream
	// (model-moves-transcript-01PMCH01 WP02). See MoveIndex / MoveKind
	// below for the contract; the chat runner's move journal is the only
	// emitter and WP04's chat surface is the consumer.
	StreamEventMoveStart StreamEventKind = "move_start"
)

// StreamEvent is one delta the chassis-bound LLM provider hands the
// kernel's StreamSink as the upstream provider stream is consumed.
// The shape mirrors core/llm.StreamEvent (intentionally so) but lives
// in agentgraph to keep the import direction one-way: the chassis
// adapter that bridges core/llm.Stream → kernel.StreamSink translates
// between the two value types.
//
// ToolName / ToolID / ToolArgs carry the tool-use accumulator state
// when Kind == StreamEventTool. Provider adapters typically deliver
// tool deltas across multiple chunks; the chassis adapter is
// responsible for emitting the accumulated record on finalisation,
// not raw deltas — this matches the existing pump shape on the
// llm:stream-chunk topic.
type StreamEvent struct {
	Kind StreamEventKind `json:"kind"`
	// Text is the next text-delta chunk when Kind == StreamEventText.
	Text string `json:"text,omitempty"`
	// ToolName / ToolID / ToolArgs carry the model's accumulated
	// tool_use record on Kind == StreamEventTool (the chassis adapter
	// fans these onto the existing tool-use chip render path).
	ToolName string `json:"tool_name,omitempty"`
	ToolID   string `json:"tool_id,omitempty"`
	ToolArgs string `json:"tool_args,omitempty"`
	// Reasoning carries a model reasoning frame on
	// Kind == StreamEventReasoning. The string is the rendered
	// content; the structured frame stays inside the chassis.
	Reasoning string `json:"reasoning,omitempty"`
	// Usage / Finish / ErrMsg are populated for the matching kinds.
	UsageInputTokens     int    `json:"usage_input_tokens,omitempty"`
	UsageOutputTokens    int    `json:"usage_output_tokens,omitempty"`
	UsageReasoningTokens int    `json:"usage_reasoning_tokens,omitempty"`
	Finish               string `json:"finish,omitempty"`
	ErrMsg               string `json:"err,omitempty"`

	// MoveIndex / MoveKind are populated only on
	// Kind == StreamEventMoveStart and carry the move the stream is
	// about to render (model-moves-transcript-01PMCH01 WP02).
	//
	// MoveIndex is the 0-based position of the move inside the human
	// turn — the same number the persisted transcript entry carries in
	// its move_index column, which is what lets the live view and a
	// reload be reconciled entry-for-entry.
	MoveIndex int    `json:"move_index,omitempty"`
	MoveKind  string `json:"move_kind,omitempty"`
	// MoveArgsSummary / MoveIsError complete a tool boundary so the chat
	// surface can render the chip without waiting for the persisted row
	// (model-moves-transcript-01PMCH01 WP04). MoveArgsSummary is the
	// DISPLAY-LAYER summary — argument names and value types, never
	// values, and never the raw ToolArgs above. MoveIsError says the
	// tool_result boundary closes its pair with a failure.
	MoveArgsSummary string `json:"move_args_summary,omitempty"`
	MoveIsError     bool   `json:"move_is_error,omitempty"`
}

// StreamSink is the kernel-side seam the LLMNode-bound provider feeds
// streaming chunks into. The chassis (core/rpc) wires a sink that
// fans the events onto the existing `llm:stream-chunk` broker topic
// so the chat surface continues to receive byte-equal deltas — no
// frontend changes required.
//
// Implementations MUST be safe for concurrent use within a single
// kernel run: even though one LLMNode dispatch produces events
// serially, multiple parallel LLMNodes (Parallel-fanout) can share
// the same sink.
//
// Nil-safe behavior: when env.StreamSink is nil the provider adapter
// simply drains the upstream stream without forwarding deltas — the
// final Response still lands on the LLMNode output ports as before,
// only the per-token surface goes silent. This preserves the kernel's
// "graphs without a chassis" path (tests, batch-runs, scripted
// activities) without forcing every caller to plumb a sink.
type StreamSink interface {
	// Emit forwards one streaming delta. The implementation is
	// expected to be non-blocking on the hot path; chassis wiring
	// pushes onto the broker's bounded channel and drops on overflow.
	Emit(StreamEvent)
	// Close signals the end of the stream for this LLMNode dispatch.
	// The chassis fans a stream-finish payload onto the broker. Safe
	// to call multiple times; subsequent calls are no-ops.
	Close()
}

// streamSinkContextKey is the unexported context key the LLMNode
// executor uses to thread the kernel-bound StreamSink into the
// chassis-bound LLMProvider call. The chassis adapter (core/rpc) calls
// StreamSinkFromContext to retrieve it; tests can use the same helper
// to assert the kernel propagates the sink.
type streamSinkContextKey struct{}

// withStreamSink binds the StreamSink onto ctx so a downstream
// LLMProvider.Generate implementation can pull it out without a new
// argument on the seam. Returns the original ctx unchanged when sink
// is nil so the chassis side sees the absence-of-sink state cleanly.
func withStreamSink(ctx context.Context, sink StreamSink) context.Context {
	if sink == nil {
		return ctx
	}
	return context.WithValue(ctx, streamSinkContextKey{}, sink)
}

// StreamSinkFromContext returns the StreamSink the kernel pinned to
// ctx for the active LLMNode dispatch. ok=false when the kernel
// chose not to wire one (test runs, batch executions).
func StreamSinkFromContext(ctx context.Context) (StreamSink, bool) {
	v := ctx.Value(streamSinkContextKey{})
	if v == nil {
		return nil, false
	}
	s, ok := v.(StreamSink)
	return s, ok
}

// streamSinkFunc adapts a function value to StreamSink. Used in tests
// that want to record events without a struct.
type streamSinkFunc struct {
	emit  func(StreamEvent)
	close func()
}

// NewStreamSinkFunc adapts emit + close callbacks to the StreamSink
// interface. close may be nil — Close becomes a no-op.
func NewStreamSinkFunc(emit func(StreamEvent), close func()) StreamSink {
	if emit == nil {
		emit = func(StreamEvent) {}
	}
	if close == nil {
		close = func() {}
	}
	return &streamSinkFunc{emit: emit, close: close}
}

// Emit satisfies StreamSink.
func (f *streamSinkFunc) Emit(ev StreamEvent) { f.emit(ev) }

// Close satisfies StreamSink.
func (f *streamSinkFunc) Close() { f.close() }

// Generate satisfies LLMProvider.
func (f LLMProviderFunc) Generate(ctx context.Context, req LLMRequest) (LLMResponse, error) {
	return f(ctx, req)
}

// ---- Tools ----

// ToolCall is the agentgraph-side request to dispatch a tool.
type ToolCall struct {
	// ID is the model-assigned tool_use id this dispatch answers. It is
	// what pairs a persisted tool_call transcript entry with its
	// tool_result (model-moves-transcript-01PMCH01 WP02) — and, one
	// layer up, what pairs a provider tool_use block with its
	// tool_result block, the mismatch that produces the classic 400.
	//
	// ToolDispatchNode populates it from the model's ToolCallRequest.
	// The builtin-tool node kinds leave it empty: a graph-authored tool
	// node is not answering a model request, so there is no id to pair
	// with and no transcript entry to write.
	ID   string
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
	Scopes []string // which scopes to query (default: all)
	Query  string   // free-text query (embedding-driven retrieval)
	TopK   int
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

// HistoryEntry is what crosses the history-write seam: one transcript
// entry, classic or move.
//
// Role/Content are the classic pair. The three move fields carry the
// model-moves metadata (model-moves-transcript-01PMCH01 WP01) down to
// core/session.Manager.AppendTranscriptEntry, which is the only writer
// that can stamp it onto a durable row.
//
// The seam takes a struct rather than four strings on purpose: with a
// second AppendMove method, or an optional-upgrade interface, an
// implementation could satisfy the seam while quietly dropping move
// metadata on the floor. One method, one shape — an implementer either
// carries the whole entry or does not compile.
//
// A zero MoveKind means a classic entry, byte-identical downstream to
// everything written before the mission.
type HistoryEntry struct {
	// Role is the speaker: "user" | "assistant" | "system" | "tool".
	Role string
	// Content is the entry's text.
	Content string

	// MoveKind is one of core/session.MoveKinds() — "assistant_move" |
	// "tool_call" | "tool_result" | "final" — or empty for a classic
	// entry. The writer validates the vocabulary; agentgraph carries it
	// as a string so this package keeps no dependency on core/session.
	MoveKind string
	// MoveIndex is the 0-based position of this entry inside its human
	// turn. Carries no meaning when MoveKind is empty.
	MoveIndex int
	// TurnSpanID is the id of the user message that opened the turn this
	// entry belongs to. Required when MoveKind is set.
	TurnSpanID string

	// ToolCalls is the structured tool payload a tool_call / tool_result
	// entry carries (model-moves-transcript-01PMCH01 WP02). It is what
	// makes the pairing machine-readable rather than a prose convention:
	// a tool_result entry names the id of the tool_call it answers.
	//
	// WP01 landed session.TranscriptEntry.ToolCalls with no counterpart
	// here, which is why the field had no production writer and sat in
	// the in-flight ledger. This is the counterpart.
	//
	// DISPLAY-LAYER REDACTION (spec §4): Arguments are deliberately NOT
	// read off this shape. A tool_call entry's Content carries an ARGS
	// SUMMARY — key names and value types, never values — and the store
	// binding drops ToolCallRequest.Arguments on the floor rather than
	// letting it reach session.ToolCall.Arguments, which the display
	// surface projects.
	ToolCalls []ToolCallRequest

	// ModelToolArgs is the MODEL LAYER's raw tool arguments for a
	// tool_call entry: tool-call id → the JSON argument object the model
	// emitted (model-moves-transcript-01PMCH01 WP03, spec §4).
	//
	// It is a SEPARATE FIELD from ToolCalls[].Arguments on purpose. The
	// two layers have opposite rules about the same bytes — display must
	// never see values, the provider protocol cannot work without them —
	// and giving each its own field is what stops a future edit from
	// "unifying" them and leaking raw arguments into an exportable
	// transcript row. It lands in session_messages.model_tool_args, which
	// no display or export projection reads.
	//
	// Only meaningful on MoveKind == "tool_call"; the writer rejects it
	// anywhere else rather than dropping it silently.
	ModelToolArgs map[string]string
}

// HistoryWriter is the optional persistence half of the history seam,
// consumed by the SessionWriteNode kind and by the chat runner.
// Production wiring binds it to
// core/session.Manager.AppendTranscriptEntry; tests can pass a fake.
//
// This is the ONE writer for chat transcript entries (spec §4 of
// model-moves-transcript-01PMCH01: "no second history-writing path").
// AppendEntry assigns the message ID and persistence side-effects; the
// returned id is surfaced on the SessionWriteNode `message_id` output
// port so downstream nodes (artifact backlinks, branch markers) can
// reference the freshly persisted row.
type HistoryWriter interface {
	AppendEntry(ctx context.Context, sessionID string, entry HistoryEntry) (messageID string, err error)
}

// HistoryWriterFunc adapts a function value to the HistoryWriter
// interface. Useful for tests that want to record append calls without
// declaring a struct.
type HistoryWriterFunc func(ctx context.Context, sessionID string, entry HistoryEntry) (string, error)

// AppendEntry satisfies HistoryWriter.
func (f HistoryWriterFunc) AppendEntry(ctx context.Context, sessionID string, entry HistoryEntry) (string, error) {
	return f(ctx, sessionID, entry)
}

// nilHistoryWriter is the default no-op writer installed when env
// applyEnvDefaults sees env.HistoryWriter == nil. It returns
// ErrNoHistoryWriter so SessionWriteNode surfaces a clear error rather
// than silently no-opping.
type nilHistoryWriter struct{}

// AppendEntry satisfies HistoryWriter; always errors.
func (nilHistoryWriter) AppendEntry(_ context.Context, _ string, _ HistoryEntry) (string, error) {
	return "", ErrNoHistoryWriter
}

// ErrNoHistoryWriter is returned by the default HistoryWriter stub
// when SessionWriteNode fires without a real writer wired.
var ErrNoHistoryWriter = errors.New("agentgraph: no history writer configured")

// ---- Attachments ----

// AttachmentResolver wraps `core/attachments` for the AttachmentNode.
type AttachmentResolver interface {
	Resolve(ctx context.Context, attachmentID string) (AttachmentBlock, error)
}

// AttachmentRegistrar is the optional registration half of the
// attachment seam, consumed by the read_file executor when its
// `as_attachment` attr is set. Implementations register a new
// attachment in `core/attachments` and return its ID. Implementations
// that don't support registration should return ErrNotImplemented.
//
// Found unimplemented by entry-points-and-crash-reporting-01PMZD13
// UNIT-4's checkseams fix: no production Env construction ever sets
// exec_state.go's env.AttachmentRegistry, so this branch has always been
// a no-op outside tests — the only prior "implementer" was
// core/agentgraph/internal/recorders' test double, which checkseams
// counted as real before this fix excluded test-double packages.
// wiring:deferred(no production Env ever sets AttachmentRegistry; core/attachments.Manager.AddMedia is the nearest candidate producer but needs scope-binding context — scopeKind/scopeID — the read_file/AttachmentNode call site does not carry, so wiring it is a design decision (what scope does content registered via as_attachment bind to?), not a mechanical wire; owner: unassigned, entry-points-and-crash-reporting-01PMZD13 UNIT-4)
type AttachmentRegistrar interface {
	Register(ctx context.Context, block AttachmentBlock) (attachmentID string, err error)
}

// AttachmentBlock is the resolved content the AttachmentNode flows on
// its output port. The kernel passes it through opaquely; the
// downstream LLMNode is responsible for converting it to a
// content block in its provider request.
type AttachmentBlock struct {
	MIME   string
	Data   []byte
	URI    string
	Title  string
	Inline bool
}

// ---- Bash output cache ----

// BashOutput is the cached transcript of a prior bash tool run, looked
// up by run-id by the read_bash_output executor (FR-057b).
type BashOutput struct {
	// RunID is the identifier the executor matches on.
	RunID string
	// Command is the parsed/argv command string (best-effort; may be
	// empty if the cache layer did not retain it).
	Command string
	// Stdout is the captured standard output.
	Stdout string
	// Stderr is the captured standard error.
	Stderr string
	// ExitCode is the process exit status (-1 when the run was killed
	// or the cache predates exit tracking).
	ExitCode int
}

// BashOutputStore is the kernel-side seam for reading cached bash tool
// transcripts. Production wiring binds this to the bash tool's run
// log; tests pass a fake. nil disables read_bash_output (the executor
// returns ErrNoBashOutput).
type BashOutputStore interface {
	Get(ctx context.Context, runID string) (BashOutput, error)
}

// BashOutputStoreFunc adapts a function to BashOutputStore.
type BashOutputStoreFunc func(ctx context.Context, runID string) (BashOutput, error)

// Get satisfies BashOutputStore.
func (f BashOutputStoreFunc) Get(ctx context.Context, runID string) (BashOutput, error) {
	return f(ctx, runID)
}

// ---- Policy gate ----

// PolicyGate is the kernel-side seam onto core/policy/cedar's filesystem
// gate hooks. It is intentionally narrow: only the operations the
// agentgraph executors call are exposed. Production wiring binds this
// to a small adapter that delegates to cedar.Check{File,State}{Read,Write};
// tests pass a stub.
//
// nil disables policy gating (the AllowAll fallback) — every executor
// proceeds as if the action were permitted. This matches the
// boot-stage default of cedar.AllowAll.
type PolicyGate interface {
	// CheckFileRead is consulted by read_file before opening a path.
	// Implementations return a non-nil error to deny.
	CheckFileRead(ctx context.Context, path string) error
	// CheckFileWrite is consulted by write_file before opening a path
	// for writing.
	CheckFileWrite(ctx context.Context, path string) error
	// CheckStateRead is the FR-058b finer-grained gate; source is the
	// `read` archetype's source class ("file", "bash_output", ...).
	CheckStateRead(ctx context.Context, source string) error
	// CheckStateWrite mirrors CheckStateRead for write targets.
	CheckStateWrite(ctx context.Context, target string) error
	// CheckTool is consulted by the tool-dispatch boundary (WP17,
	// trust-surfaces-that-fire-01PMZ202 UNIT-15) before a model-emitted
	// tool call executes. toolName is the fully-qualified
	// "<server>__<tool>" name (cedar.ToolUID's convention; empty server
	// segment is written as "builtin"). Implementations consult BOTH
	// the coarse Action::"use_tool" family every shipped policy names
	// and the finer-grained tool_exec action.
	CheckTool(ctx context.Context, toolName string) error
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
//
// The question and answer types are core/elicitation's — the same ones
// kenaz__ask_user_question and the Wails dialog speak
// (agentgraph-total-convergence-01PMGX01 WP06, spec §4.3). A graph node
// can therefore pose any question the model-facing tool can, and every
// implementation of this seam is an adapter over one
// elicitation.Registry rather than a private map.
type AskBus interface {
	// Pending records that an Ask is waiting. Production wiring pushes
	// to the chat surface and persists; tests record the question.
	Pending(ctx context.Context, runID, nodeID string, q elicitation.Question) error
	// LookupAnswer returns the answer the user provided, with ok=true
	// when one is available. The Ask executor calls this on its
	// second fire (after Run resumes). ok=false means the run is still
	// parked.
	LookupAnswer(ctx context.Context, runID, nodeID string) (elicitation.Answer, bool)
}

// memAskBus is the in-memory default: a thin adapter over the shared
// elicitation.Registry. It holds no ask state of its own, so the
// pause/resume semantics a test exercises are the production ones.
type memAskBus struct {
	registry *elicitation.Registry
}

// NewMemAskBus returns a process-local AskBus suitable for tests.
func NewMemAskBus() *memAskBus {
	return &memAskBus{registry: elicitation.NewRegistry(elicitation.Config{})}
}

// Registry exposes the backing store so a caller can share it.
func (b *memAskBus) Registry() *elicitation.Registry { return b.registry }

// Pending records a pending question.
func (b *memAskBus) Pending(_ context.Context, runID, nodeID string, q elicitation.Question) error {
	id := elicitation.NodeAskID(runID, nodeID)
	if _, ok := b.registry.Get(id); ok {
		// Already parked (the executor re-fired before an answer
		// arrived) — nothing to do.
		return nil
	}
	_, err := b.registry.Register(elicitation.Request{
		ID:       id,
		RunID:    runID,
		NodeID:   nodeID,
		Question: q,
	})
	return err
}

// LookupAnswer reads back any provided answer.
func (b *memAskBus) LookupAnswer(_ context.Context, runID, nodeID string) (elicitation.Answer, bool) {
	return b.registry.Answered(elicitation.NodeAskID(runID, nodeID))
}

// Answer is a test helper that injects a user answer. It registers the
// ask first when the executor has not parked one yet, which is how the
// chat chassis pre-seeds a turn's user message.
func (b *memAskBus) Answer(runID, nodeID, ans string) {
	id := elicitation.NodeAskID(runID, nodeID)
	if _, ok := b.registry.Get(id); !ok {
		_, _ = b.registry.Register(elicitation.Request{
			ID:     id,
			RunID:  runID,
			NodeID: nodeID,
		})
	}
	_ = b.registry.Resolve(id, elicitation.TextAnswer(ans))
}

// PendingQuestion is a test helper.
func (b *memAskBus) PendingQuestion(runID, nodeID string) (string, bool) {
	e, ok := b.registry.Get(elicitation.NodeAskID(runID, nodeID))
	if !ok || e.Status != elicitation.StatusPending {
		return "", false
	}
	return e.Question.Text, true
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
	// CompactionSitePostTool fires after a ToolNode returns, gated by
	// the pre_call site's token-watermark verdict
	// (Env.automaticCompactionCrossed) — NOT by result byte size.
	// CK-04/CK-05/CK-06 (chat-turn-integrity-01PMZ606 WP13): this used
	// to claim firing was gated by tool_result_max_bytes;
	// SiteConfig.ToolResultMaxBytes is round-tripped through Settings
	// but absent from CompactOpts, so no strategy dispatch can ever
	// read it — see that field's doc comment in
	// core/agentgraph/compaction/config.go for the justify() block.
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
	// ContextWindow is the active model's max context length in tokens
	// (0 = unknown). It is the denominator the compactor evaluates its
	// pre-call threshold against, so automatic compaction fires as the
	// conversation approaches the limit rather than on every turn.
	ContextWindow int
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

// ErrNoBashOutput is returned by the default BashOutputStore stub when
// no cache is wired or the run-id is unknown.
var ErrNoBashOutput = errors.New("agentgraph: no bash output store configured")

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

// nilBashOutput is the default BashOutputStore stub.
type nilBashOutput struct{}

func (nilBashOutput) Get(_ context.Context, _ string) (BashOutput, error) {
	return BashOutput{}, ErrNoBashOutput
}

// allowAllPolicy is the default PolicyGate when no gate is configured.
// Mirrors cedar.AllowAll's default-allow stance: every check returns
// nil so the executor proceeds.
type allowAllPolicy struct{}

func (allowAllPolicy) CheckFileRead(_ context.Context, _ string) error   { return nil }
func (allowAllPolicy) CheckFileWrite(_ context.Context, _ string) error  { return nil }
func (allowAllPolicy) CheckStateRead(_ context.Context, _ string) error  { return nil }
func (allowAllPolicy) CheckStateWrite(_ context.Context, _ string) error { return nil }
func (allowAllPolicy) CheckTool(_ context.Context, _ string) error       { return nil }

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
	if env.HistoryWriter == nil {
		env.HistoryWriter = nilHistoryWriter{}
	}
	if env.Attachments == nil {
		env.Attachments = nilAttachments{}
	}
	if env.BashOutput == nil {
		env.BashOutput = nilBashOutput{}
	}
	if env.Policy == nil {
		env.Policy = allowAllPolicy{}
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
	if env.TaskState == nil {
		env.TaskState = NewTaskState()
	}
	if env.Hooks == nil {
		env.Hooks = NewHookManager(env.Memory, env.SessionID, env.ProjectID)
		// Production-wired journal writer (memory_hook_journal table)
		// — installed after construction so a chassis that supplied
		// EnvDeps.JournalWriter gets persistence; tests / nil-seam
		// callers fall back to the in-memory ring buffer.
		if env.JournalWriter != nil {
			env.Hooks.SetJournalWriter(env.JournalWriter)
		}
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
