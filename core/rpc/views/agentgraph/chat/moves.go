package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"

	coreag "github.com/kameas-ai/kenaz-harness/core/agentgraph"
	"github.com/kameas-ai/kenaz-harness/core/logging"
)

// ---------------------------------------------------------------------------
// The turn journal — model-moves-transcript-01PMCH01 WP02.
//
// One human turn can drive up to the agent loop's iteration ceiling of
// model fires. Before this file the whole trajectory collapsed into ONE
// assistant row: the loop's flattened final text, written by the
// chat graph's `assistant_write` session_write node. Every intermediate
// segment, every tool call and every tool result was thrown away, and
// the live view glued the segments into a single run-on paragraph
// because nothing on the stream said where one ended and the next
// began (spec §1).
//
// turnJournal is the one object that fixes both halves, because both
// halves are the same fact — "a new move started" — observed once:
//
//	PERSISTENCE  every move becomes its own transcript entry, tagged
//	             with the turn's span id and its 0-based position.
//	STREAM       every move is announced on the chat stream with that
//	             same position, so WP04 can open a fresh bubble.
//
// Emitting both from one allocation site is what makes the 1:1
// count/order contract in llm.MoveBoundary true by construction rather
// than by two code paths agreeing to stay in step.
//
// ONE WRITER. The journal never touches the session store: it hands
// entries to the same coreag.HistoryWriter the runner was configured
// with, which resolves to llmHistoryWriter.AppendEntry →
// session.Manager.AppendTranscriptEntry, the single seam
// check-single-move-writer.sh counts. The journal itself also IMPLEMENTS
// coreag.HistoryWriter and is what the runner installs on env, so the
// kernel's session_write node reaches the store through it and its
// entry can be stamped as the turn's `final` (see AppendEntry).
//
// WHERE THE SIGNALS COME FROM. The journal is fed at the two places the
// kernel already distinguishes an agent-loop body fire:
//
//   - LLMProviderAdapter.Generate, for requests whose StreamToChat is
//     set — the model node's own attr, forwarded through the seam. Six
//     other executors call env.LLM.Generate (the fused router, the
//     review gate, both escalation-ladder rungs, the summarise
//     compaction strategy, the task-state gate) and none of them are
//     the user's assistant turn; recording them would persist the exit
//     gate's private verdict as chat.
//   - kernelToolAdapter.Call, once per dispatched tool, which is the
//     only place that sees a call and its result as a pair.
//
// INERT WITHOUT A SPAN. Every move must name the human turn it belongs
// to; that id is the user message that opened the turn. When the runner
// cannot resolve one (a session with no user message at all) the
// journal is inert and every entry is written classic, exactly as
// before this mission — a span-less move is unorderable and
// AppendTranscriptEntry would reject it, failing the run. That is a
// degenerate data condition, not a feature switch: there is no setting
// that reaches it.
// ---------------------------------------------------------------------------

// moveKind mirrors core/session.MoveKind. The chat package cannot
// import core/session (DIRECTIVE_001), and coreag.HistoryEntry.MoveKind
// is a plain string for the same reason, so the vocabulary is restated
// here. core/session.MoveKinds() is the canonical list; these four
// strings must equal it.
const (
	moveKindAssistantMove = "assistant_move"
	moveKindToolCall      = "tool_call"
	moveKindToolResult    = "tool_result"
	moveKindFinal         = "final"
)

// turnJournal records one human turn's moves. Safe for concurrent use:
// tool_dispatch runs up to max_concurrent tools in parallel, so two
// goroutines can be allocating move positions at once.
type turnJournal struct {
	// writer is the single transcript-writing seam. nil disables
	// persistence entirely (tests that construct a bare runner).
	writer coreag.HistoryWriter
	// emit fans a move boundary onto the chat stream. nil disables the
	// boundary half without disabling persistence.
	emit      func(coreag.StreamEvent)
	sessionID string
	// spanID is the id of the user message that opened this turn. Empty
	// makes the journal inert (see the file header).
	spanID string

	mu        sync.Mutex
	nextIndex int
	// openIdx is the position allocated for the assistant segment
	// currently streaming, or -1 when no segment is open. Allocated on
	// the segment's FIRST text delta so the boundary reaches the surface
	// before the tokens it separates.
	openIdx int
	// held is the last completed model fire's text, parked until we know
	// whether anything follows it. If nothing does, it is the turn's
	// final answer and is written once, with the `final` kind, instead
	// of being written twice — once as an assistant_move and again as
	// the session_write node's final row. Anything that persists next
	// flushes it first, so held text never lands out of order.
	held     string
	heldIdx  int
	heldLive bool
}

// newTurnJournal builds the journal for one turn. spanID is the id of
// the user message that opened it.
func newTurnJournal(writer coreag.HistoryWriter, emit func(coreag.StreamEvent),
	sessionID, spanID string) *turnJournal {
	return &turnJournal{
		writer:    writer,
		emit:      emit,
		sessionID: sessionID,
		spanID:    spanID,
		openIdx:   -1,
	}
}

// records reports whether this journal stamps moves. False means every
// entry it forwards is a classic entry.
func (j *turnJournal) records() bool {
	return j != nil && j.writer != nil && j.spanID != ""
}

// ---- allocation ---------------------------------------------------------

// allocate reserves the next move position and announces it on the
// stream. Caller holds j.mu, which is what keeps boundary order and
// persisted order identical.
func (j *turnJournal) allocate(kind, toolName, toolCallID string) int {
	idx := j.nextIndex
	j.nextIndex++
	if j.emit != nil {
		j.emit(coreag.StreamEvent{
			Kind:      coreag.StreamEventMoveStart,
			MoveIndex: idx,
			MoveKind:  kind,
			ToolName:  toolName,
			ToolID:    toolCallID,
		})
	}
	return idx
}

// persist writes one entry through the single seam. Caller holds j.mu.
// Failures are logged, never fatal: losing a transcript row must not
// abort the user's turn.
func (j *turnJournal) persist(ctx context.Context, e coreag.HistoryEntry) {
	if j.writer == nil {
		return
	}
	if _, err := j.writer.AppendEntry(ctx, j.sessionID, e); err != nil {
		logging.L().Warn("chat.move.persist_failed",
			"session_id", j.sessionID,
			"kind", e.MoveKind,
			"index", e.MoveIndex,
			"err", err.Error())
	}
}

// flushHeld writes the parked assistant text as an assistant_move.
// Caller holds j.mu. Called by every persisting path before its own
// write, which is what keeps the transcript in allocation order.
func (j *turnJournal) flushHeld(ctx context.Context) {
	if !j.heldLive {
		return
	}
	text, idx := j.held, j.heldIdx
	j.held, j.heldIdx, j.heldLive = "", 0, false
	j.persist(ctx, coreag.HistoryEntry{
		Role:       "assistant",
		Content:    text,
		MoveKind:   moveKindAssistantMove,
		MoveIndex:  idx,
		TurnSpanID: j.spanID,
	})
}

// ---- the three feed points ----------------------------------------------

// OpenAssistantSegment announces the move whose text is about to stream.
// Called by LLMProviderAdapter's drain loop on the first text delta of a
// chat-bound model fire; subsequent deltas of the same fire are no-ops.
//
// This is the boundary that kills the run-on paragraph: it reaches the
// surface strictly before the first token of the segment it opens.
func (j *turnJournal) OpenAssistantSegment() {
	if !j.records() {
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.openIdx >= 0 {
		return
	}
	j.openIdx = j.allocate(moveKindAssistantMove, "", "")
}

// RecordAssistantMove parks one completed model fire's text. Called by
// LLMProviderAdapter after stream.Final() for a chat-bound request.
//
// The text is parked rather than written because the LAST fire's text is
// also what the graph's session_write node persists as the turn's answer
// — writing it here as well would put the same paragraph in the
// transcript twice, and in WP03's model-visible history twice. Parking
// lets AppendEntry recognise its own text arriving and write ONE row
// with the `final` kind, at the position already announced on the
// stream. Any fire that is followed by anything at all has its parked
// text flushed as an assistant_move by whatever comes next.
//
// An empty text parks nothing: a fire that only emitted tool calls has
// no segment to render and no bubble was opened for it.
func (j *turnJournal) RecordAssistantMove(ctx context.Context, text string) {
	if !j.records() {
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	idx := j.openIdx
	j.openIdx = -1
	if text == "" {
		return
	}
	j.flushHeld(ctx)
	if idx < 0 {
		// No delta ever arrived (a non-streaming provider, or a provider
		// that returned the whole body in the final response). Announce
		// the boundary now so the 1:1 rule holds unconditionally.
		idx = j.allocate(moveKindAssistantMove, "", "")
	}
	j.held, j.heldIdx, j.heldLive = text, idx, true
}

// RecordToolCall persists the model's request to run a tool. Called by
// kernelToolAdapter.Call immediately before dispatch, so the entry
// exists even if the tool never returns.
//
// DISPLAY-LAYER REDACTION (spec §4): the entry's Content is
// displayArgsSummary's output — argument NAMES and value TYPES, never
// values. The raw arguments the provider protocol requires are WP03's
// problem and are composed there from the provider history; they must
// not be reintroduced here. The two layers' helpers are deliberately
// named apart so neither can be "fixed" with the other.
func (j *turnJournal) RecordToolCall(ctx context.Context, call coreag.ToolCall) {
	if !j.records() {
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	j.flushHeld(ctx)
	idx := j.allocate(moveKindToolCall, call.Name, call.ID)
	j.persist(ctx, coreag.HistoryEntry{
		Role:       "tool",
		Content:    displayArgsSummary(call.Name, call.Args),
		MoveKind:   moveKindToolCall,
		MoveIndex:  idx,
		TurnSpanID: j.spanID,
		ToolCalls:  []coreag.ToolCallRequest{{ID: call.ID, Name: call.Name}},
		// MODEL-LAYER ARGUMENTS (spec §4, WP03). The raw JSON, kept in a
		// field named for the layer that may see it, travelling beside —
		// never inside — the display payload above. The provider protocol
		// cannot reconstruct an assistant tool_use block without it, and
		// the model authored it in the first place: this is history, not
		// logs.
		//
		// Note the two lines together: Content carries
		// displayArgsSummary's names-and-types rendering, ModelToolArgs
		// carries the values. If you are ever tempted to make one of these
		// serve both, read the contract on session.Message's
		// ModelLayerToolArgs first — the layers have opposite rules and
		// neither implements the other.
		ModelToolArgs: modelLayerArgsJSON(call.ID, call.Args),
	})
}

// modelLayerArgsJSON renders one tool call's raw arguments for the MODEL
// LAYER, keyed by tool-call id.
//
// Deliberately named nothing like displayArgsSummary. That function is
// the DISPLAY layer's and emits argument names and value TYPES; this one
// emits the values verbatim. The names are unalike on purpose so a
// future reader searching for "the args helper" finds two and has to
// choose, rather than finding one and assuming.
//
// An empty argument map yields nil, not "{}": a tool invoked with no
// arguments has nothing for the model layer to carry, and nil keeps the
// persisted column NULL.
func modelLayerArgsJSON(callID string, args map[string]any) map[string]string {
	if callID == "" || len(args) == 0 {
		return nil
	}
	b, err := json.Marshal(args)
	if err != nil {
		return nil
	}
	return map[string]string{callID: string(b)}
}

// RecordToolResult persists what a tool returned. Called by
// kernelToolAdapter.Call once the dispatch resolves, success or failure
// — an error result is still a transcript entry, because "the tool
// failed" is exactly the reasoning step the next model fire pivots on.
func (j *turnJournal) RecordToolResult(ctx context.Context, call coreag.ToolCall,
	result coreag.ToolResult) {
	if !j.records() {
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	j.flushHeld(ctx)
	idx := j.allocate(moveKindToolResult, call.Name, call.ID)
	j.persist(ctx, coreag.HistoryEntry{
		Role:       "tool",
		Content:    result.Content,
		MoveKind:   moveKindToolResult,
		MoveIndex:  idx,
		TurnSpanID: j.spanID,
		ToolCalls:  []coreag.ToolCallRequest{{ID: call.ID, Name: call.Name}},
	})
}

// ---- the HistoryWriter face ---------------------------------------------

// AppendEntry satisfies coreag.HistoryWriter. This is the face the
// runner installs on env, so it is what the chat graph's
// `assistant_write` session_write node reaches.
//
// An assistant entry arriving here IS the turn's answer — the exit
// gate approved it and the run is finishing — so it is stamped
// `final`. When its text is the parked text of the last model fire
// (the overwhelmingly common case: the gate approves the draft
// unchanged) the parked copy is dropped and this row takes the
// position already announced on the stream, so the answer appears once,
// not twice. When the exit gate or the escalation ladder REVISED the
// draft, the texts differ, the parked segment is flushed as an
// assistant_move of its own, and the revised answer gets a fresh
// position — which is the honest record: the model said one thing and
// the turn returned another.
//
// Anything that is not an assistant entry (a system note) is forwarded
// unchanged as a classic entry, after flushing so ordering holds.
func (j *turnJournal) AppendEntry(ctx context.Context, sessionID string,
	entry coreag.HistoryEntry) (string, error) {
	if j == nil || j.writer == nil {
		// Not reachable from the runner, which installs the journal on
		// env only when it has a real writer to forward to — precisely
		// so a chassis with no history wired still gets the kernel's
		// ErrNoHistoryWriter from session_write instead of a silent
		// no-op. Kept honest for a direct caller.
		return "", coreag.ErrNoHistoryWriter
	}
	if !j.records() || entry.MoveKind != "" || entry.Role != "assistant" {
		if j.records() {
			j.mu.Lock()
			j.flushHeld(ctx)
			j.mu.Unlock()
		}
		return j.writer.AppendEntry(ctx, sessionID, entry)
	}

	j.mu.Lock()
	idx := -1
	// Compared after TrimSpace (adversarial review of WP02): a
	// whitespace-only difference between the parked draft and the
	// returned answer is not a revision, and treating it as one would
	// put the same paragraph in the transcript twice — the exact
	// user-visible duplicate this branch exists to prevent. Byte
	// equality alone makes that duplicate one stray "\n" away, from any
	// future graph that trims on its way to session_write.
	absorbed := j.heldLive && strings.TrimSpace(j.held) == strings.TrimSpace(entry.Content)
	if absorbed {
		idx = j.heldIdx
	} else {
		j.flushHeld(ctx)
		idx = j.allocate(moveKindFinal, "", "")
	}
	j.mu.Unlock()

	entry.MoveKind = moveKindFinal
	entry.MoveIndex = idx
	entry.TurnSpanID = j.spanID
	id, err := j.writer.AppendEntry(ctx, sessionID, entry)
	if absorbed {
		j.mu.Lock()
		if err == nil {
			// The parked copy became this row. Drop it.
			j.held, j.heldIdx, j.heldLive = "", 0, false
		}
		// On failure the park stands, so Finish's terminal flush still
		// gets the segment into the transcript. Clearing it first would
		// mean a failed final write silently deleted the text the user
		// watched stream.
		j.mu.Unlock()
	}
	return id, err
}

// ---- terminal paths ------------------------------------------------------

// RecordPartial persists an interrupted fire's accumulated text as its
// own move (agent-loop-robustness-parity FR-001 meets WP02). The
// segment was already announced on the stream when its first delta
// landed, so it reuses that position rather than opening a second one.
//
// Kind is assistant_move, not final: an interrupted turn produced no
// answer. WP05's collapsed view therefore has no `final` to render for
// it and falls back to the last move — which is the truth about what
// happened, and better than labelling a truncated segment an answer.
//
// Returns the position used so the caller can log it; the message id
// comes back through the writer as usual.
func (j *turnJournal) RecordPartial(ctx context.Context, text string) {
	if !j.records() || text == "" {
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	idx := j.openIdx
	j.openIdx = -1
	if j.heldLive && strings.HasPrefix(text, j.held) {
		// The stop landed AFTER the fire completed but before the turn's
		// session_write claimed its text: the parked segment is this
		// partial's body, only marked. Take its position and drop the
		// park — flushing it and then writing the marked copy would put
		// the same words in the transcript twice, which is the
		// duplicate this mission exists to remove, not create.
		// (adversarial review of WP02)
		idx = j.heldIdx
		j.held, j.heldIdx, j.heldLive = "", 0, false
	} else {
		j.flushHeld(ctx)
	}
	if idx < 0 {
		idx = j.allocate(moveKindAssistantMove, "", "")
	}
	j.persist(ctx, coreag.HistoryEntry{
		Role:       "assistant",
		Content:    text,
		MoveKind:   moveKindAssistantMove,
		MoveIndex:  idx,
		TurnSpanID: j.spanID,
	})
}

// RecordSyntheticToolResult persists the is_error tool_result that
// closes a tool_use the interrupt cancelled. The tool_call entry for it
// already exists — RecordToolCall writes before dispatch precisely so
// an interrupted call is not an orphan — so this only backfills the
// answering half.
func (j *turnJournal) RecordSyntheticToolResult(ctx context.Context,
	call coreag.ToolCallRequest, content string) {
	if !j.records() {
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	j.flushHeld(ctx)
	idx := j.allocate(moveKindToolResult, call.Name, call.ID)
	j.persist(ctx, coreag.HistoryEntry{
		Role:       "tool",
		Content:    content,
		MoveKind:   moveKindToolResult,
		MoveIndex:  idx,
		TurnSpanID: j.spanID,
		ToolCalls:  []coreag.ToolCallRequest{{ID: call.ID, Name: call.Name}},
	})
}

// Finish flushes anything still parked. The runner calls it on every
// terminal path: without it, a turn that ended before its
// session_write node fired (a kernel error, a budget cap, an ask-pause)
// would drop its last model segment on the floor — the exact loss this
// mission exists to stop.
func (j *turnJournal) Finish(ctx context.Context) {
	if !j.records() {
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	j.openIdx = -1
	j.flushHeld(ctx)
}

// MoveCount reports how many move positions this turn has allocated.
// Used by the runner's logging and by tests.
func (j *turnJournal) MoveCount() int {
	if j == nil {
		return 0
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.nextIndex
}

// ---- display-layer redaction --------------------------------------------

// displayArgsSummary renders a tool invocation for the DISPLAY layer:
// the tool name plus each argument's NAME and value TYPE, never a value.
//
//	kenaz__write_file(content=<string>, path=<string>)
//
// Spec §4 splits redaction in two and asks that the halves stay
// separately named. This is the display half and it is the only one
// WP02 has: a tool argument is frequently the most sensitive text in a
// session (a token being written to a config file, a password in a
// bash command), and the transcript row a user can copy, export and
// share must not carry it. The model-visible half, which legitimately
// carries raw arguments because the provider protocol requires them and
// the model wrote them in the first place, is WP03's and belongs in a
// helper named for that layer. Neither is a correct implementation of
// the other.
func displayArgsSummary(name string, args map[string]any) string {
	if len(args) == 0 {
		return name + "()"
	}
	keys := make([]string, 0, len(args))
	for k := range args {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=<%s>", k, argTypeName(args[k])))
	}
	return name + "(" + strings.Join(parts, ", ") + ")"
}

// argTypeName names an argument's JSON-ish type. Deliberately coarse:
// anything finer (a length, a prefix, a "looks like a path") starts
// leaking the value a byte at a time.
func argTypeName(v any) string {
	switch t := v.(type) {
	case nil:
		return "null"
	case bool:
		return "bool"
	case string:
		return "string"
	case float64, float32, int, int32, int64, uint, uint32, uint64:
		return "number"
	case []any:
		return "list"
	case map[string]any:
		return "object"
	default:
		_ = t
		return "value"
	}
}
