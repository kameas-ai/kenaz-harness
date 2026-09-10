package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"

	coreag "github.com/kameas-ai/kenaz-harness/core/agentgraph"
	corellm "github.com/kameas-ai/kenaz-harness/core/llm"
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
//
// USAGE CAPTURE (fix/usage-persists-on-every-move). Before this fix,
// token/cost usage was recorded ONLY for the turn's `final` row, via
// HookPostLLM firing exactly once from exec_state.go's
// sessionWriteExecutor — the sole caller of FirePostHooks. Every other
// assistant_move this journal persists bypasses that node entirely (it
// writes straight to j.writer), so on a multi-move tool-using turn every
// Generate() call except the last one silently dropped its usage
// (confirmed live: OpenRouter billed ~$13 on a conversation the app's
// own footer showed as $0.13 — a ~100x undercount on the CLASSIC graph,
// where AgenticTurnRouting is off, the shipped default).
//
// The journal now owns usage capture end to end, including the `final`
// row — round 1 of this fix left the final row on the pre-existing
// HookPostLLM mechanism, which a reviewer proved MISATTRIBUTES usage on
// the ROUTED graph (AgenticTurnRouting on, not yet the default): the
// exit_gate node makes its own real, costed Generate() call between the
// loop and session_write, so LastResponse() — a single mutable slot,
// last-write-wins — held the GATE's small verdict cost by the time the
// hook fired, not the chat answer's. See AppendEntry's doc comment for
// the absorbed/revised split that fixes this.
//
// The mechanism, current state:
//   - RecordAssistantMove parks the corellm.Response alongside the held
//     text (heldResp/heldProviderKind/heldModelID) the instant a
//     Generate() call for the user's turn completes — BEFORE anything
//     else (a tool loop, the exit gate) can run and clobber the
//     adapter's mutable LastResponse().
//   - flushHeld / RecordPartial fire j.usageHook directly from that
//     snapshot, once per persisted non-final assistant-role row, right
//     after the row's message id comes back from the writer.
//   - AppendEntry (the final row) fires j.usageHook too: from heldResp
//     when the draft was absorbed unchanged (the common case — immune
//     to whatever ran after the chat move), or from lastResponseFn when
//     the exit gate/escalation ladder genuinely revised the draft (the
//     one case with no per-move snapshot, where LastResponse() remains
//     correct — the revision IS the last Generate() call's own output).
//
// This is deliberately NOT routed through
// HookManager.FirePostHooks(HookPostLLM, ...) — that boundary also fans
// out to the artifacts code-block detector and the generated-image
// drain (see chat_runner.go / api.go), and firing those for every
// intermediate move is a separate, out-of-scope behaviour change.
// chat_runner.go now registers the OLD HookPostLLM-based usage callback
// only when !journal.records() (the degenerate no-span case, unchanged
// from before this fix) — so for every ordinary turn there is exactly
// ONE writer of a row's usage, never two, and the journal's own writer
// is immune to the misattribution the old mechanism had.
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
	// heldResp/heldProviderKind/heldModelID are the usage snapshot for
	// the Generate() call that produced `held`, captured by
	// RecordAssistantMove at the moment it parked the text — NOT read
	// later off LLMProviderAdapter.lastResp, which is a single mutable
	// slot overwritten by every subsequent Generate() call (including
	// ones this journal never records). Cleared together with
	// held/heldIdx/heldLive everywhere that trio is cleared, so a later
	// flush can never fire usage against a stale response.
	heldResp         corellm.Response
	heldProviderKind string
	heldModelID      string
	// usageHook fires once per persisted assistant-role row this journal
	// writes, INCLUDING the final row (see the file header's USAGE
	// CAPTURE note). nil disables usage capture entirely — tests that
	// don't exercise usage leave it nil.
	usageHook UsageHookFunc
	// candidates is a bounded, per-turn history of every non-empty text
	// a Generate() call produced during this turn — chat move or not —
	// paired with THAT call's own usage. See RecordCandidateUsage and
	// AppendEntry's doc comment for why this exists: on the ROUTED
	// graph, exit_gate ALWAYS makes its own real, costed Generate() call
	// between whatever revised the draft and assistant_write
	// (unconditional, exec_compute.go's reviewExecutor), so it is ALWAYS
	// the last call before AppendEntry — reading a single mutable
	// "last response" slot at AppendEntry time can therefore NEVER
	// recover a revision's own usage, not merely "if something else
	// intervenes". Content-matching against this history is what
	// recovers it without threading a new port through every executor
	// between the reviser and session_write.
	candidates []journalCandidate
}

// journalCandidate is one Generate() call's (text, usage) pair, recorded
// unconditionally by RecordCandidateUsage.
type journalCandidate struct {
	text         string
	resp         corellm.Response
	providerKind string
	modelID      string
}

// newTurnJournal builds the journal for one turn. spanID is the id of
// the user message that opened it. usageHook may be nil (usage capture
// disabled for this turn — e.g. no Manager wired, or a test that isn't
// exercising usage).
func newTurnJournal(writer coreag.HistoryWriter, emit func(coreag.StreamEvent),
	sessionID, spanID string, usageHook UsageHookFunc) *turnJournal {
	return &turnJournal{
		writer:    writer,
		emit:      emit,
		sessionID: sessionID,
		spanID:    spanID,
		openIdx:   -1,
		usageHook: usageHook,
	}
}

// RecordCandidateUsage records one Generate() call's (text, usage) pair
// for later content-matched lookup by AppendEntry's revised-final
// branch. Called unconditionally by LLMProviderAdapter.Generate for
// EVERY fire that produces non-empty text — the chat move, the exit
// gate's verdict, an escalation-ladder rung, a replan draft, all of
// them — the instant that call's own response is computed, before any
// LATER call on the same adapter can overwrite the shared mutable
// LastResponse() slot. This is what makes the revised-final case immune
// to "which node happened to run last": AppendEntry looks up the exact
// persisted text, not whatever the adapter's single slot holds at read
// time.
//
// Safe to call on a nil journal or with empty text (both no-op) — a
// bare adapter with no move journal (batch/activity runs) must not
// panic here.
func (j *turnJournal) RecordCandidateUsage(text string, resp corellm.Response, providerKind, modelID string) {
	if j == nil || text == "" {
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	j.candidates = append(j.candidates, journalCandidate{
		text: text, resp: resp, providerKind: providerKind, modelID: modelID,
	})
}

// lookupCandidateUsageLocked searches this turn's candidate history,
// most-recently-recorded first, for a text match against target
// (TrimSpace-compared — the same rule AppendEntry's absorbed check
// uses, for the same reason: a whitespace-only difference is not a
// different call). Caller holds j.mu.
func (j *turnJournal) lookupCandidateUsageLocked(target string) (resp corellm.Response, providerKind, modelID string, ok bool) {
	want := strings.TrimSpace(target)
	for i := len(j.candidates) - 1; i >= 0; i-- {
		if strings.TrimSpace(j.candidates[i].text) == want {
			c := j.candidates[i]
			return c.resp, c.providerKind, c.modelID, true
		}
	}
	return corellm.Response{}, "", "", false
}

// LookupCandidateUsage is lookupCandidateUsageLocked's locking wrapper
// for callers that don't already hold j.mu — chat_runner.go's
// degenerate (!records()) HookPostLLM registration is the one
// production caller.
//
// RecordCandidateUsage is NOT gated on records(): an inert journal (no
// user message to span a turn from) still accumulates every Generate()
// call's (text, usage) pair, so even that edge case can recover the
// right call's usage by content match instead of trusting
// LLMProviderAdapter.LastResponse() — which, on the ROUTED graph, would
// be exit_gate's own always-runs-last verdict call there too; the
// degenerate case is not exempt from that structural fact just because
// it has no span. Safe to call on a nil journal (returns ok=false).
func (j *turnJournal) LookupCandidateUsage(target string) (resp corellm.Response, providerKind, modelID string, ok bool) {
	if j == nil {
		return corellm.Response{}, "", "", false
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.lookupCandidateUsageLocked(target)
}

// records reports whether this journal stamps moves. False means every
// entry it forwards is a classic entry.
func (j *turnJournal) records() bool {
	return j != nil && j.writer != nil && j.spanID != ""
}

// ---- allocation ---------------------------------------------------------

// moveDetail carries the facts a boundary needs beyond its kind and
// position. Empty for an assistant segment; a tool boundary fills it so
// the chat surface can render the chip live, without waiting for the
// persisted row (model-moves-transcript-01PMCH01 WP04).
//
// argsSummary is the DISPLAY-LAYER summary — the same string the
// tool_call entry persists as its Content. Raw arguments never enter
// this struct; see RecordToolCall's redaction note.
type moveDetail struct {
	toolName    string
	toolCallID  string
	argsSummary string
	isError     bool
}

// allocate reserves the next move position and announces it on the
// stream. Caller holds j.mu, which is what keeps boundary order and
// persisted order identical.
func (j *turnJournal) allocate(kind string, d moveDetail) int {
	idx := j.nextIndex
	j.nextIndex++
	if j.emit != nil {
		j.emit(coreag.StreamEvent{
			Kind:            coreag.StreamEventMoveStart,
			MoveIndex:       idx,
			MoveKind:        kind,
			ToolName:        d.toolName,
			ToolID:          d.toolCallID,
			MoveArgsSummary: d.argsSummary,
			MoveIsError:     d.isError,
		})
	}
	return idx
}

// persist writes one entry through the single seam. Caller holds j.mu.
// Failures are logged, never fatal: losing a transcript row must not
// abort the user's turn. Returns the assigned message id (empty on
// failure or when no writer is wired) so callers that need to correlate
// a follow-up write — usage capture, in particular — can do so without
// a second read.
func (j *turnJournal) persist(ctx context.Context, e coreag.HistoryEntry) (string, error) {
	if j.writer == nil {
		return "", nil
	}
	id, err := j.writer.AppendEntry(ctx, j.sessionID, e)
	if err != nil {
		logging.L().Warn("chat.move.persist_failed",
			"session_id", j.sessionID,
			"kind", e.MoveKind,
			"index", e.MoveIndex,
			"err", err.Error())
		return "", err
	}
	return id, nil
}

// fireUsage invokes j.usageHook for one persisted assistant-role row,
// when a hook is wired and the row actually landed (non-empty id, no
// write error). Caller holds j.mu — usageHook is expected to be fast or
// to accept the latency (see UsageHookFunc's doc comment); this mirrors
// how the pre-existing final-row path already invokes it synchronously
// from within the kernel's single-threaded run.
func (j *turnJournal) fireUsage(ctx context.Context, id string, err error,
	resp corellm.Response, providerKind, modelID string) {
	if err != nil || id == "" || j.usageHook == nil {
		return
	}
	j.usageHook(ctx, j.sessionID, id, providerKind, modelID, resp)
}

// flushHeld writes the parked assistant text as an assistant_move.
// Caller holds j.mu. Called by every persisting path before its own
// write, which is what keeps the transcript in allocation order.
//
// This is also THE fix for the dropped-usage bug (see the file header's
// USAGE CAPTURE note): every assistant_move flushHeld persists is a
// Generate() call session_write's HookPostLLM fire will never see, so
// this is the only place that call's usage can still be recorded.
func (j *turnJournal) flushHeld(ctx context.Context) {
	if !j.heldLive {
		return
	}
	text, idx := j.held, j.heldIdx
	resp, providerKind, modelID := j.heldResp, j.heldProviderKind, j.heldModelID
	j.held, j.heldIdx, j.heldLive = "", 0, false
	j.heldResp, j.heldProviderKind, j.heldModelID = corellm.Response{}, "", ""
	id, err := j.persist(ctx, coreag.HistoryEntry{
		Role:       "assistant",
		Content:    text,
		MoveKind:   moveKindAssistantMove,
		MoveIndex:  idx,
		TurnSpanID: j.spanID,
	})
	j.fireUsage(ctx, id, err, resp, providerKind, modelID)
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
	j.openIdx = j.allocate(moveKindAssistantMove, moveDetail{})
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
// no segment to render and no bubble was opened for it — and, as a
// consequence, that fire's usage has nowhere to attach either (usage.Add
// UPDATEs an existing session_messages row by id; a fire with no row has
// none). That gap is pre-existing and out of scope here: this fix
// targets fires that DO produce a persisted row, matching the reported
// defect (a 58-row, 3-move turn contributing $0), not the deeper "no row
// exists to bill against" case, which would need a usage ledger
// decoupled from session_messages.
//
// resp/providerKind/modelID are the just-computed Generate() call's
// usage snapshot, parked alongside the text so flushHeld can fire usage
// for this exact call — never LLMProviderAdapter.LastResponse(), which
// by the time flushHeld runs may already reflect a LATER Generate()
// call (the whole reason the final-only hook undercounts a multi-move
// turn).
func (j *turnJournal) RecordAssistantMove(ctx context.Context, text string,
	resp corellm.Response, providerKind, modelID string) {
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
		idx = j.allocate(moveKindAssistantMove, moveDetail{})
	}
	j.held, j.heldIdx, j.heldLive = text, idx, true
	j.heldResp, j.heldProviderKind, j.heldModelID = resp, providerKind, modelID
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
	summary := displayArgsSummary(call.Name, call.Args)
	idx := j.allocate(moveKindToolCall, moveDetail{
		toolName:    call.Name,
		toolCallID:  call.ID,
		argsSummary: summary,
	})
	j.persist(ctx, coreag.HistoryEntry{
		Role:       "tool",
		Content:    summary,
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
	idx := j.allocate(moveKindToolResult, moveDetail{
		toolName:   call.Name,
		toolCallID: call.ID,
		isError:    result.IsError,
	})
	j.persist(ctx, coreag.HistoryEntry{
		Role:       "tool",
		Content:    result.Content,
		MoveKind:   moveKindToolResult,
		MoveIndex:  idx,
		TurnSpanID: j.spanID,
		ToolCalls: []coreag.ToolCallRequest{
			{ID: call.ID, Name: call.Name, IsError: result.IsError},
		},
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
// USAGE, absorbed vs. revised (fix/usage-persists-on-every-move, rounds
// 2 and 3 — two misattributions an independent reviewer caught by
// running loadRoutedChatGraph with distinct per-call usage): on the
// ROUTED graph, exit_gate (kind: review) sits on EVERY path into this
// AppendEntry call — chat_default.yaml wires both replan_check's false
// edge and recover's result edge into exit_gate:draft, and only
// exit_gate's approved output reaches assistant_write — and
// reviewExecutor.Execute calls env.LLM.Generate UNCONDITIONALLY on
// every fire, no bypass (exec_compute.go). So the gate's own verdict
// call is not "usually" the last Generate() before AppendEntry — it is
// ALWAYS the last one, in the revised branch exactly as much as the
// absorbed one. A single mutable "last response" slot read at
// AppendEntry time can therefore NEVER recover a revision's own usage;
// it will always return the gate's small verdict cost instead (proven
// live: chat move 1000/40/$0.0110 → ladder revision 500/20/$0.0200 →
// gate verdict 50/8/$0.0009 → the final row got 50/8/$0.0009, not the
// ladder's $0.0200). Round 2 fixed the absorbed case with heldResp
// (RecordAssistantMove captures it BEFORE the gate's call can run, so
// it is provably the chat move's own response regardless of what runs
// after) but left the revised case reading the mutable slot one hop
// later, reproducing the same defect shape on the revised path. Round 3
// closes that: the revised case now does a CONTENT-MATCHED lookup
// against j.candidates (see RecordCandidateUsage), which every
// Generate() call — chat move, ladder rung, replan draft, gate verdict,
// all of them — populates unconditionally and immediately, before any
// later call can overwrite anything. Whichever call actually produced
// entry.Content is the one whose usage this finds, regardless of what
// ran after it. No reader of LLMProviderAdapter.LastResponse() remains
// anywhere on this path.
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
	var resp corellm.Response
	var providerKind, modelID string
	haveUsage := false
	if absorbed {
		idx = j.heldIdx
		// Captured NOW, before this branch's own unlock/write, so a
		// Generate() call racing in on another goroutine cannot land
		// between this read and the clear below. See the doc comment
		// above for why this must be heldResp and not LastResponse().
		resp, providerKind, modelID = j.heldResp, j.heldProviderKind, j.heldModelID
		haveUsage = true
	} else {
		j.flushHeld(ctx)
		idx = j.allocate(moveKindFinal, moveDetail{})
		// Content-matched, not "last one wins": exit_gate's own verdict
		// call is ALWAYS the last Generate() before this point (see the
		// doc comment above), so a positional read is structurally
		// wrong here. Looking up entry.Content against every call's own
		// recorded text finds whichever call actually authored the
		// revision, no matter what ran after it.
		if r, pk, mid, ok := j.lookupCandidateUsageLocked(entry.Content); ok {
			resp, providerKind, modelID = r, pk, mid
			haveUsage = true
		}
	}
	j.mu.Unlock()

	entry.MoveKind = moveKindFinal
	entry.MoveIndex = idx
	entry.TurnSpanID = j.spanID
	id, err := j.writer.AppendEntry(ctx, sessionID, entry)
	if absorbed {
		j.mu.Lock()
		if err == nil {
			// The parked copy became this row. Drop it — including the
			// usage snapshot, so a hypothetical later flush in this same
			// journal can never fire usage using a stale response.
			j.held, j.heldIdx, j.heldLive = "", 0, false
			j.heldResp, j.heldProviderKind, j.heldModelID = corellm.Response{}, "", ""
		}
		// On failure the park stands, so Finish's terminal flush still
		// gets the segment into the transcript. Clearing it first would
		// mean a failed final write silently deleted the text the user
		// watched stream.
		j.mu.Unlock()
	}
	// This is the journal's OWN write for the final row — it replaces
	// the old external mechanism entirely (chat_runner.go only registers
	// that mechanism when !journal.records(), i.e. never for a turn that
	// reaches this branch), so there is exactly one writer per row: no
	// double-count, and (as of round 2) no misattribution either.
	if haveUsage {
		j.fireUsage(ctx, id, err, resp, providerKind, modelID)
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
	// resp/providerKind/modelID carry the completed fire's usage
	// snapshot ONLY in the prefix branch below — that is the one case
	// where a real corellm.Response was actually computed for the text
	// being persisted (see the branch comment). The else branch persists
	// text from a fire that never reached stream.Final(), so there is no
	// response to attach; fireUsage's err/id guard covers the zero-value
	// resp harmlessly, but we gate on hadUsage explicitly for clarity.
	var resp corellm.Response
	var providerKind, modelID string
	hadUsage := false
	if j.heldLive && strings.HasPrefix(text, j.held) {
		// The stop landed AFTER the fire completed but before the turn's
		// session_write claimed its text: the parked segment is this
		// partial's body, only marked. Take its position and drop the
		// park — flushing it and then writing the marked copy would put
		// the same words in the transcript twice, which is the
		// duplicate this mission exists to remove, not create.
		// (adversarial review of WP02)
		//
		// The fire DID complete (RecordAssistantMove parked it, which
		// only happens after stream.Final() returns), so its usage
		// snapshot is real and must not be dropped along with `held`.
		idx = j.heldIdx
		resp, providerKind, modelID = j.heldResp, j.heldProviderKind, j.heldModelID
		hadUsage = true
		j.held, j.heldIdx, j.heldLive = "", 0, false
		j.heldResp, j.heldProviderKind, j.heldModelID = corellm.Response{}, "", ""
	} else {
		j.flushHeld(ctx)
	}
	if idx < 0 {
		idx = j.allocate(moveKindAssistantMove, moveDetail{})
	}
	id, err := j.persist(ctx, coreag.HistoryEntry{
		Role:       "assistant",
		Content:    text,
		MoveKind:   moveKindAssistantMove,
		MoveIndex:  idx,
		TurnSpanID: j.spanID,
	})
	if hadUsage {
		j.fireUsage(ctx, id, err, resp, providerKind, modelID)
	}
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
	// isError is unconditionally true here: this path exists only to
	// close a tool_use the interrupt cancelled, and a cancelled call did
	// not succeed. The chip must say so on reload exactly as it did live.
	idx := j.allocate(moveKindToolResult, moveDetail{
		toolName:   call.Name,
		toolCallID: call.ID,
		isError:    true,
	})
	j.persist(ctx, coreag.HistoryEntry{
		Role:       "tool",
		Content:    content,
		MoveKind:   moveKindToolResult,
		MoveIndex:  idx,
		TurnSpanID: j.spanID,
		ToolCalls: []coreag.ToolCallRequest{
			{ID: call.ID, Name: call.Name, IsError: true},
		},
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
