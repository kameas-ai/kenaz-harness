package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	coreag "github.com/kameas-ai/kenaz-harness/core/agentgraph"
	corellm "github.com/kameas-ai/kenaz-harness/core/llm"
)

// ---------------------------------------------------------------------------
// model-moves-transcript-01PMCH01 WP02.
//
// These tests drive the REAL LLMProviderAdapter and the REAL
// kernelToolAdapter against the production chat_default.yaml, because
// those two adapters are where the move journal is fed and a fixture
// that swapped them for stubs (as the older integration harness does via
// EnvDefaults) would assert nothing about the shipped path.
// ---------------------------------------------------------------------------

// ---- a programmable corellm.Registry -------------------------------------

// scriptedTurn is one model fire: the deltas it streams and the response
// it finally returns.
type scriptedTurn struct {
	deltas []corellm.StreamEvent
	resp   corellm.Response
}

type scriptedStream struct {
	events chan corellm.StreamEvent
	resp   corellm.Response
}

func (s *scriptedStream) Events() <-chan corellm.StreamEvent { return s.events }
func (s *scriptedStream) Cancel() error                      { return nil }
func (s *scriptedStream) Final() (corellm.Response, error)   { return s.resp, nil }

// scriptedRegistry hands out one scriptedTurn per Stream call.
type scriptedRegistry struct {
	stubRegistry
	mu    sync.Mutex
	turns []scriptedTurn
}

func (r *scriptedRegistry) push(t scriptedTurn) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.turns = append(r.turns, t)
}

func (r *scriptedRegistry) Stream(_ context.Context, _ corellm.GenerationRequest) (corellm.Stream, error) {
	r.mu.Lock()
	if len(r.turns) == 0 {
		r.mu.Unlock()
		return nil, fmt.Errorf("scriptedRegistry: out of turns")
	}
	t := r.turns[0]
	r.turns = r.turns[1:]
	r.mu.Unlock()

	ch := make(chan corellm.StreamEvent, len(t.deltas))
	for _, d := range t.deltas {
		ch <- d
	}
	close(ch)
	return &scriptedStream{events: ch, resp: t.resp}, nil
}

// ---- a programmable ToolPool ---------------------------------------------

type scriptedPool struct {
	mu      sync.Mutex
	entries []ToolEntry
	results [][]byte
	calls   []string
	// failOn makes a named "server__tool" dispatch fail, so the
	// tool_result move it produces is an error result
	// (model-moves-transcript-01PMCH01 WP04 — the chip's running→error
	// transition needs a failure to transition on).
	failOn map[string]error
}

func (p *scriptedPool) Tools(context.Context) ([]ToolEntry, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]ToolEntry, len(p.entries))
	copy(out, p.entries)
	return out, nil
}

func (p *scriptedPool) Call(_ context.Context, server, tool string, _ []byte) ([]byte, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls = append(p.calls, server+"__"+tool)
	if err, bad := p.failOn[server+"__"+tool]; bad {
		return nil, err
	}
	if len(p.results) == 0 {
		return []byte(`{"ok":true}`), nil
	}
	r := p.results[0]
	p.results = p.results[1:]
	return r, nil
}

// buildMoveRunner wires a runner whose LLM and tool seams are the
// PRODUCTION adapters — no EnvDefaults override — so the move journal is
// exercised exactly where it runs in a shipped build.
func buildMoveRunner(t *testing.T, reg *scriptedRegistry, pool *scriptedPool) (
	*ChatRunner, *recordingBroker, *recordingHistoryWriter) {
	t.Helper()
	broker := &recordingBroker{}
	writer := &recordingHistoryWriter{}
	graph := loadProductionChatGraph(t)
	runner, err := New(Config{
		Kernel:        coreag.NewKernel(),
		Registry:      reg,
		Pool:          pool,
		Broker:        broker,
		HistoryWriter: writer,
		History:       staticHistoryReader{},
		GraphLoader:   func() (coreag.Graph, error) { return graph, nil },
		MaxTurns:      func() int { return 25 },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return runner, broker, writer
}

// textTurn scripts a fire that streams text and returns it.
func textTurn(text string) scriptedTurn {
	return scriptedTurn{
		deltas: []corellm.StreamEvent{{Kind: corellm.StreamText, Text: text}},
		resp: corellm.Response{
			Content:      []corellm.ContentBlock{{Type: "text", Text: text}},
			FinishReason: "stop",
		},
	}
}

// toolTurn scripts a fire that streams text and then asks for one tool.
func toolTurn(text, callID, toolName, argsJSON string) scriptedTurn {
	return scriptedTurn{
		deltas: []corellm.StreamEvent{{Kind: corellm.StreamText, Text: text}},
		resp: corellm.Response{
			Content:      []corellm.ContentBlock{{Type: "text", Text: text}},
			FinishReason: "tool_use",
			ToolCalls: []corellm.ToolUse{
				{ID: callID, Name: toolName, Input: []byte(argsJSON)},
			},
		},
	}
}

// moveEntries filters the writer's calls down to the move-tagged ones.
func moveEntries(w *recordingHistoryWriter) []coreag.HistoryEntry {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]coreag.HistoryEntry, 0, len(w.calls))
	for _, c := range w.calls {
		if c.entry.MoveKind != "" {
			out = append(out, c.entry)
		}
	}
	return out
}

// boundaries pulls the move-boundary markers off the broker in emission
// order.
func boundaries(b *recordingBroker) []corellm.MoveBoundary {
	var out []corellm.MoveBoundary
	for _, e := range b.snapshot() {
		if e.topic != "llm:stream-chunk" {
			continue
		}
		chunk, ok := e.payload.(StreamChunkPayload)
		if !ok || chunk.Chunk.Kind != corellm.StreamMoveStart || chunk.Chunk.Move == nil {
			continue
		}
		out = append(out, *chunk.Chunk.Move)
	}
	return out
}

// runFiveIterationTurn drives a turn of five model fires: four that call
// a tool and one that answers. Returns the recorders.
func runFiveIterationTurn(t *testing.T) (*recordingBroker, *recordingHistoryWriter) {
	t.Helper()
	reg := &scriptedRegistry{}
	for i := 1; i <= 4; i++ {
		reg.push(toolTurn(
			fmt.Sprintf("step %d: looking it up", i),
			fmt.Sprintf("tu-%d", i),
			"search__web",
			fmt.Sprintf(`{"q":"question %d"}`, i),
		))
	}
	reg.push(textTurn("the answer is 42"))

	pool := &scriptedPool{entries: []ToolEntry{{Server: "search", Name: "web"}}}
	runner, broker, writer := buildMoveRunner(t, reg, pool)

	if _, err := runner.StartStream(context.Background(), "profile-1", "session-1", "", "find it"); err != nil {
		t.Fatalf("StartStream: %v", err)
	}
	closed := waitForClosed(t, broker)
	if closed.Reason == "backend-error" {
		t.Fatalf("run failed: %s", closed.Message)
	}
	return broker, writer
}

// TestMoves_FiveIterationTurnPersistsEveryMove is the FR-001 acceptance
// case: "a 5-iteration tool-using turn persists >=5 assistant move
// entries + tool entries".
//
// Before WP02 this same run persisted TWO rows — the user turn and one
// flattened assistant message — and the four intermediate segments, four
// tool calls and four tool results were gone by the time the page
// reloaded.
//
// MUTATION EVIDENCE (run and confirmed to fail):
//   - delete the `a.moves.RecordAssistantMove(ctx, out.Content)` call in
//     LLMProviderAdapter.Generate -> 4 assistant moves missing, count
//     assertion fails.
//   - delete `a.moves.RecordToolCall(ctx, call)` in
//     kernelToolAdapter.Call -> the tool_call entries vanish and the
//     kind sequence assertion fails.
//   - change turnJournal.allocate to reuse `j.nextIndex` without
//     incrementing -> the contiguity assertion fails.
func TestMoves_FiveIterationTurnPersistsEveryMove(t *testing.T) {
	t.Parallel()
	_, writer := runFiveIterationTurn(t)

	got := moveEntries(writer)
	wantKinds := []string{
		moveKindAssistantMove, moveKindToolCall, moveKindToolResult,
		moveKindAssistantMove, moveKindToolCall, moveKindToolResult,
		moveKindAssistantMove, moveKindToolCall, moveKindToolResult,
		moveKindAssistantMove, moveKindToolCall, moveKindToolResult,
		moveKindFinal,
	}
	if len(got) != len(wantKinds) {
		t.Fatalf("persisted %d moves, want %d; got=%s", len(got), len(wantKinds), describeMoves(got))
	}
	for i, want := range wantKinds {
		if got[i].MoveKind != want {
			t.Errorf("move %d kind = %q, want %q (all: %s)", i, got[i].MoveKind, want, describeMoves(got))
		}
	}

	// Assistant moves: at least five (four intermediate + the final).
	assistantish := 0
	for _, e := range got {
		if e.MoveKind == moveKindAssistantMove || e.MoveKind == moveKindFinal {
			assistantish++
		}
	}
	if assistantish < 5 {
		t.Errorf("assistant-bearing moves = %d, want >=5 (spec acceptance)", assistantish)
	}

	// Contiguous, 0-based, in persisted order.
	for i, e := range got {
		if e.MoveIndex != i {
			t.Errorf("move %d has MoveIndex %d, want %d (indices must be dense and in order)", i, e.MoveIndex, i)
		}
	}

	// Every move shares the turn's span — the id of the user row the
	// runner wrote at StartStream (recordingHistoryWriter returns
	// "msg-1").
	for i, e := range got {
		if e.TurnSpanID != "msg-1" {
			t.Errorf("move %d TurnSpanID = %q, want %q", i, e.TurnSpanID, "msg-1")
		}
	}

	// The final carries the answer, and the intermediate segments carry
	// what the user watched stream.
	if got[len(got)-1].Content != "the answer is 42" {
		t.Errorf("final content = %q, want the answer", got[len(got)-1].Content)
	}
	if !strings.Contains(got[0].Content, "step 1") {
		t.Errorf("first move content = %q, want the first streamed segment", got[0].Content)
	}

	// The answer appears ONCE. The last model fire's text and the
	// session_write node's row are the same paragraph; writing both
	// would double it in the transcript and again in WP03's
	// model-visible history.
	answers := 0
	for _, e := range got {
		if e.Content == "the answer is 42" {
			answers++
		}
	}
	if answers != 1 {
		t.Errorf("the answer is persisted %d times, want exactly 1: %s", answers, describeMoves(got))
	}
}

// TestMoves_StreamBoundariesMatchPersistedMoves is the WP02/WP04
// contract (llm.MoveBoundary): one boundary per persisted move, same
// count, same order, same index.
//
// MUTATION EVIDENCE (run and confirmed to fail):
//   - drop the emit from turnJournal.allocate -> 0 boundaries, count
//     assertion fails.
//   - move the OpenAssistantSegment call in the drain loop to AFTER
//     sink.Emit -> the ordering assertion below (boundary precedes its
//     first text delta) fails.
func TestMoves_StreamBoundariesMatchPersistedMoves(t *testing.T) {
	t.Parallel()
	broker, writer := runFiveIterationTurn(t)

	moves := moveEntries(writer)
	marks := boundaries(broker)
	if len(marks) != len(moves) {
		t.Fatalf("boundaries=%d persisted moves=%d — the 1:1 contract is broken\nmoves=%s\nmarks=%+v",
			len(marks), len(moves), describeMoves(moves), marks)
	}
	for i := range marks {
		if marks[i].Index != moves[i].MoveIndex {
			t.Errorf("boundary %d index = %d, persisted move index = %d", i, marks[i].Index, moves[i].MoveIndex)
		}
	}

	// A tool boundary names its tool so WP04 can bind a chip's status
	// transition to the right chip.
	for i, m := range marks {
		if m.Kind == moveKindToolCall || m.Kind == moveKindToolResult {
			if m.ToolName == "" || m.ToolCallID == "" {
				t.Errorf("boundary %d (%s) has no tool identity: %+v", i, m.Kind, m)
			}
		}
	}

	// The whole point: the boundary for a streamed segment reaches the
	// surface BEFORE that segment's first token. Without this the
	// frontend cannot split the segments and the run-on paragraph
	// survives.
	events := broker.snapshot()
	firstText := -1
	firstBoundary := -1
	for i, e := range events {
		if e.topic != "llm:stream-chunk" {
			continue
		}
		chunk := e.payload.(StreamChunkPayload)
		if firstBoundary < 0 && chunk.Chunk.Kind == corellm.StreamMoveStart {
			firstBoundary = i
		}
		if firstText < 0 && chunk.Chunk.Kind == corellm.StreamText {
			firstText = i
		}
	}
	if firstBoundary < 0 || firstText < 0 {
		t.Fatalf("expected both a boundary and a text delta; boundary=%d text=%d", firstBoundary, firstText)
	}
	if firstBoundary > firstText {
		t.Errorf("first boundary at event %d arrives AFTER the first text delta at %d", firstBoundary, firstText)
	}
}

// TestMoves_ToolCallEntryRedactsArgumentValues pins the display-layer
// redaction contract (spec §4): a tool_call entry carries an ARGS
// SUMMARY, never the values.
//
// MUTATION EVIDENCE (run and confirmed to fail): change
// displayArgsSummary's per-key format from "%s=<%s>" with argTypeName to
// "%s=%v" with the value -> the secret lands in the entry and this test
// fails on the first assertion.
func TestMoves_ToolCallEntryRedactsArgumentValues(t *testing.T) {
	t.Parallel()
	const secret = "sk-live-51H8xQOhSUPERSECRETvalue"

	reg := &scriptedRegistry{}
	args, err := json.Marshal(map[string]any{
		"path":  "/etc/kenaz/config.toml",
		"token": secret,
		"retry": 3,
		"force": true,
	})
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	reg.push(toolTurn("writing the config", "tu-1", "fs__write", string(args)))
	reg.push(textTurn("done"))

	pool := &scriptedPool{entries: []ToolEntry{{Server: "fs", Name: "write"}}}
	runner, broker, writer := buildMoveRunner(t, reg, pool)
	if _, err := runner.StartStream(context.Background(), "p", "s", "", "write the config"); err != nil {
		t.Fatalf("StartStream: %v", err)
	}
	if closed := waitForClosed(t, broker); closed.Reason == "backend-error" {
		t.Fatalf("run failed: %s", closed.Message)
	}

	writer.mu.Lock()
	all := append([]writerCall(nil), writer.calls...)
	writer.mu.Unlock()

	for i, c := range all {
		if strings.Contains(c.entry.Content, secret) {
			t.Fatalf("entry %d (%s) leaked a raw argument value into the display layer: %q",
				i, c.entry.MoveKind, c.entry.Content)
		}
		for _, tc := range c.entry.ToolCalls {
			if strings.Contains(tc.Arguments, secret) {
				t.Fatalf("entry %d ToolCalls carried raw arguments: %q", i, tc.Arguments)
			}
		}
	}

	var toolCall coreag.HistoryEntry
	for _, e := range moveEntries(writer) {
		if e.MoveKind == moveKindToolCall {
			toolCall = e
			break
		}
	}
	if toolCall.MoveKind == "" {
		t.Fatal("no tool_call move persisted")
	}
	want := "fs__write(force=<bool>, path=<string>, retry=<number>, token=<string>)"
	if toolCall.Content != want {
		t.Errorf("tool_call summary = %q, want %q", toolCall.Content, want)
	}
	// The pairing id still has to be there — redaction removes values,
	// not identity.
	if len(toolCall.ToolCalls) != 1 || toolCall.ToolCalls[0].ID != "tu-1" {
		t.Errorf("tool_call entry lost its pairing id: %+v", toolCall.ToolCalls)
	}
}

// TestMoves_OnlyTheChatBoundModelNodeBecomesAMove pins the
// StreamToChat discriminator. env.LLM.Generate is reached by several
// executors; on the ROUTED graph the exit gate makes a real, costed
// model call whose reply is a private PASS/FAIL verdict. Recording it
// would publish the gate's internal judgement into the user's
// transcript as an assistant message.
//
// MUTATION EVIDENCE (run and confirmed to fail): drop `req.StreamToChat
// &&` from the recordMoves expression in LLMProviderAdapter.Generate ->
// the gate's verdict is persisted as a move and both assertions fail.
func TestMoves_OnlyTheChatBoundModelNodeBecomesAMove(t *testing.T) {
	t.Parallel()
	reg := &scriptedRegistry{}
	reg.push(textTurn("the answer is 42")) // assistant_turn
	reg.push(textTurn(`{"verdict":"pass","reason":"looks right"}`))

	pool := &scriptedPool{}
	broker := &recordingBroker{}
	writer := &recordingHistoryWriter{}
	graph := loadRoutedChatGraph(t)
	runner, err := New(Config{
		Kernel:        coreag.NewKernel(),
		Registry:      reg,
		Pool:          pool,
		Broker:        broker,
		HistoryWriter: writer,
		History:       staticHistoryReader{},
		GraphLoader:   func() (coreag.Graph, error) { return graph, nil },
		MaxTurns:      func() int { return 25 },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := runner.StartStream(context.Background(), "p", "s", "", "ask"); err != nil {
		t.Fatalf("StartStream: %v", err)
	}
	if closed := waitForClosed(t, broker); closed.Reason == "backend-error" {
		t.Fatalf("run failed: %s", closed.Message)
	}

	got := moveEntries(writer)
	for i, e := range got {
		if strings.Contains(e.Content, "verdict") {
			t.Errorf("move %d persisted the exit gate's private verdict: %q", i, e.Content)
		}
	}
	// Exactly one move: the answer, as the turn's final.
	if len(got) != 1 || got[0].MoveKind != moveKindFinal {
		t.Fatalf("persisted %s, want exactly one final move", describeMoves(got))
	}
}

// TestMoves_RunThatDiesKeepsItsLastSegment pins the terminal flush. The
// journal parks a completed fire's text until it knows whether the
// session_write node is going to claim it as the turn's answer; a run
// that dies before reaching that node — a provider drop, a budget cap,
// an ask-pause — never reaches it. Without the flush on the terminal
// path, the segment the user just watched stream is the one thing the
// reload loses, which is the exact failure this mission exists to stop.
//
// MUTATION EVIDENCE (run and confirmed to fail): delete the
// `sub.journal.Finish(flushCtx)` deferred flush in driveRun -> the first
// fire's text is never written and the count assertion fails.
func TestMoves_RunThatDiesKeepsItsLastSegment(t *testing.T) {
	t.Parallel()
	// The routed graph puts the exit gate's own model call between the
	// loop and assistant_write. The assistant fire succeeds and streams;
	// the gate's call then finds the provider gone, the run dies, and
	// session_write never fires — so the ONLY thing that can save the
	// segment the user just watched is the terminal flush.
	reg := &scriptedRegistry{}
	reg.push(textTurn("here is what I found"))

	pool := &scriptedPool{}
	broker := &recordingBroker{}
	writer := &recordingHistoryWriter{}
	graph := loadRoutedChatGraph(t)
	runner, err := New(Config{
		Kernel:        coreag.NewKernel(),
		Registry:      reg,
		Pool:          pool,
		Broker:        broker,
		HistoryWriter: writer,
		History:       staticHistoryReader{},
		GraphLoader:   func() (coreag.Graph, error) { return graph, nil },
		MaxTurns:      func() int { return 25 },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := runner.StartStream(context.Background(), "p", "s", "", "find it"); err != nil {
		t.Fatalf("StartStream: %v", err)
	}
	closed := waitForClosed(t, broker)
	if closed.Reason != "backend-error" {
		t.Fatalf("expected the run to die; reason=%q msg=%q", closed.Reason, closed.Message)
	}

	got := moveEntries(writer)
	if len(got) != 1 {
		t.Fatalf("persisted %s, want exactly the one streamed segment — the dying run dropped it",
			describeMoves(got))
	}
	if got[0].MoveKind != moveKindAssistantMove {
		t.Errorf("move 0 kind = %q, want assistant_move", got[0].MoveKind)
	}
	if got[0].Content != "here is what I found" {
		t.Errorf("move 0 content = %q, want the streamed segment", got[0].Content)
	}
	// No `final`: the turn never produced an answer.
	for _, e := range got {
		if e.MoveKind == moveKindFinal {
			t.Errorf("a dead run produced a `final` move: %+v", e)
		}
	}
}

// stubTurnSpan answers the empty-userMessage span lookup.
type stubTurnSpan struct {
	id  string
	err error
	// calls counts lookups so the test can assert the runner only
	// reaches for it when it has no id of its own.
	calls int
	mu    sync.Mutex
}

func (s *stubTurnSpan) LatestUserMessageID(context.Context, string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	return s.id, s.err
}

func (s *stubTurnSpan) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

// TestMoves_EmptyUserMessageTurnResolvesItsSpan pins the third
// turn-span source. StartStream is called with an empty userMessage on
// two live paths — the keychain-rotation redrive and the multimodal
// send, where the frontend already landed the user row — so the runner
// has no id of its own to span from. Without the lookup those turns
// would write no moves at all and silently regress to the pre-mission
// single-row shape.
//
// MUTATION EVIDENCE (run and confirmed to fail): delete the
// `if turnSpanID == "" && r.cfg.TurnSpan != nil` block from StartStream
// -> zero moves are persisted and the first assertion fails.
func TestMoves_EmptyUserMessageTurnResolvesItsSpan(t *testing.T) {
	t.Parallel()
	reg := &scriptedRegistry{}
	reg.push(textTurn("resumed answer"))
	pool := &scriptedPool{}
	broker := &recordingBroker{}
	writer := &recordingHistoryWriter{}
	span := &stubTurnSpan{id: "user-row-77"}
	graph := loadProductionChatGraph(t)
	runner, err := New(Config{
		Kernel:        coreag.NewKernel(),
		Registry:      reg,
		Pool:          pool,
		Broker:        broker,
		HistoryWriter: writer,
		History:       staticHistoryReader{},
		TurnSpan:      span,
		GraphLoader:   func() (coreag.Graph, error) { return graph, nil },
		MaxTurns:      func() int { return 25 },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// Empty userMessage: the redrive shape.
	if _, err := runner.StartStream(context.Background(), "p", "s", "", ""); err != nil {
		t.Fatalf("StartStream: %v", err)
	}
	if closed := waitForClosed(t, broker); closed.Reason == "backend-error" {
		t.Fatalf("run failed: %s", closed.Message)
	}

	got := moveEntries(writer)
	if len(got) == 0 {
		t.Fatalf("empty-userMessage turn persisted no moves — its span was never resolved")
	}
	for i, e := range got {
		if e.TurnSpanID != "user-row-77" {
			t.Errorf("move %d TurnSpanID = %q, want the looked-up user row", i, e.TurnSpanID)
		}
	}
	if span.callCount() != 1 {
		t.Errorf("span lookups = %d, want exactly 1", span.callCount())
	}
}

// TestMoves_UserMessageTurnDoesNotLookUpItsSpan is the other half: when
// StartStream wrote the user row itself it already HAS the id, and
// reaching for the reader would be a wasted store read on the hot path
// of every ordinary turn.
func TestMoves_UserMessageTurnDoesNotLookUpItsSpan(t *testing.T) {
	t.Parallel()
	reg := &scriptedRegistry{}
	reg.push(textTurn("answer"))
	pool := &scriptedPool{}
	broker := &recordingBroker{}
	writer := &recordingHistoryWriter{}
	span := &stubTurnSpan{id: "should-not-be-used"}
	graph := loadProductionChatGraph(t)
	runner, err := New(Config{
		Kernel:        coreag.NewKernel(),
		Registry:      reg,
		Pool:          pool,
		Broker:        broker,
		HistoryWriter: writer,
		History:       staticHistoryReader{},
		TurnSpan:      span,
		GraphLoader:   func() (coreag.Graph, error) { return graph, nil },
		MaxTurns:      func() int { return 25 },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := runner.StartStream(context.Background(), "p", "s", "", "a real question"); err != nil {
		t.Fatalf("StartStream: %v", err)
	}
	if closed := waitForClosed(t, broker); closed.Reason == "backend-error" {
		t.Fatalf("run failed: %s", closed.Message)
	}
	if span.callCount() != 0 {
		t.Errorf("span lookups = %d, want 0 — the runner already had the id", span.callCount())
	}
	for i, e := range moveEntries(writer) {
		if e.TurnSpanID != "msg-1" {
			t.Errorf("move %d TurnSpanID = %q, want the id the runner's own write returned", i, e.TurnSpanID)
		}
	}
}

// ---- journal-level tests --------------------------------------------------

// recordingSink collects stream events emitted by a journal under test.
type recordingSink struct {
	mu     sync.Mutex
	events []coreag.StreamEvent
}

func (r *recordingSink) emit(ev coreag.StreamEvent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, ev)
}

func (r *recordingSink) snapshot() []coreag.StreamEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]coreag.StreamEvent, len(r.events))
	copy(out, r.events)
	return out
}

// TestMoves_InterruptKeepsPartialAndClosesDanglingPairs pins the
// interrupt path (agent-loop-robustness-parity FR-001 under WP02): the
// truncated segment keeps the position its first delta announced, and
// every dangling tool_use gets its answering tool_result so the pair is
// not orphaned.
//
// MUTATION EVIDENCE (run and confirmed to fail):
//   - make RecordPartial allocate a fresh index instead of reusing
//     j.openIdx -> the partial lands at index 3 and both the index
//     assertion and the 1:1 boundary count fail.
//   - drop the RecordSyntheticToolResult loop from PersistInterrupt ->
//     the tool_call at index 1 has no answering result and the kind
//     sequence assertion fails.
func TestMoves_InterruptKeepsPartialAndClosesDanglingPairs(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w := &recordingHistoryWriter{}
	sink := &recordingSink{}
	j := newTurnJournal(w, sink.emit, "sess-1", "span-1")

	// Fire 1 completes with text and asks for a tool.
	j.OpenAssistantSegment()
	j.RecordAssistantMove(ctx, "I'll read the file")
	call := coreag.ToolCall{ID: "tu-1", Name: "fs__read", Args: map[string]any{"path": "/tmp/x"}}
	j.RecordToolCall(ctx, call)
	j.RecordToolResult(ctx, call, coreag.ToolResult{Content: "contents"})

	// Fire 2 starts streaming and the user hits Stop.
	j.OpenAssistantSegment()
	dangling := coreag.ToolCallRequest{ID: "tu-2", Name: "fs__write"}
	j.RecordToolCall(ctx, coreag.ToolCall{ID: dangling.ID, Name: dangling.Name})
	// SegmentText is what the move path persists — the in-flight move's
	// own text, not the turn's whole accumulation. Here they coincide
	// because the fixture is hand-built; the production divergence is
	// pinned by TestMoves_InterruptPersistsOnlyTheInFlightSegment.
	is := &InterruptState{
		PartialText:       "Now I'll write",
		SegmentText:       "Now I'll write",
		DanglingToolCalls: []coreag.ToolCallRequest{dangling},
	}
	is.PersistInterrupt(ctx, "sess-1", w, j)
	j.Finish(ctx)

	got := moveEntries(w)
	wantKinds := []string{
		moveKindAssistantMove, // fire 1 text
		moveKindToolCall,      // tu-1
		moveKindToolResult,    // tu-1 result
		moveKindToolCall,      // tu-2, written before dispatch
		moveKindAssistantMove, // the interrupted partial
		moveKindToolResult,    // tu-2 synthetic close
	}
	if len(got) != len(wantKinds) {
		t.Fatalf("persisted %d moves, want %d: %s", len(got), len(wantKinds), describeMoves(got))
	}
	for i, want := range wantKinds {
		if got[i].MoveKind != want {
			t.Errorf("move %d kind = %q, want %q (%s)", i, got[i].MoveKind, want, describeMoves(got))
		}
	}
	// The partial reuses the position announced when its first delta
	// landed (index 3, allocated by the second OpenAssistantSegment),
	// which is why it is persisted out of index order relative to the
	// tu-2 tool_call at index 4.
	partial := got[4]
	if partial.MoveIndex != 3 {
		t.Errorf("partial MoveIndex = %d, want 3 (the position its first delta announced)", partial.MoveIndex)
	}
	if !strings.Contains(partial.Content, "Now I'll write") {
		t.Errorf("partial content = %q, want the truncated segment", partial.Content)
	}
	// No `final`: an interrupted turn produced no answer.
	for _, e := range got {
		if e.MoveKind == moveKindFinal {
			t.Errorf("interrupted turn produced a `final` move: %+v", e)
		}
	}
	// The dangling call's pair closes.
	if got[5].ToolCalls[0].ID != "tu-2" {
		t.Errorf("synthetic result pairs with %q, want tu-2", got[5].ToolCalls[0].ID)
	}

	// Boundaries still match persisted moves 1:1.
	var marks int
	for _, ev := range sink.snapshot() {
		if ev.Kind == coreag.StreamEventMoveStart {
			marks++
		}
	}
	if marks != len(got) {
		t.Errorf("boundaries=%d persisted=%d on the interrupt path", marks, len(got))
	}
}

// TestMoves_InterruptPersistsOnlyTheInFlightSegment is the regression
// for the duplication the adversarial review of WP02 found.
//
// StreamBridge.PartialState() accumulates the WHOLE turn's deltas —
// correct before moves existed, when the interrupt path wrote exactly
// one assistant row. With moves, every completed segment is already its
// own persisted row, so handing that accumulation to RecordPartial
// wrote the earlier segments' words a SECOND time: the run-on paragraph
// re-persisted, and (once WP03 lands) the model re-reading its own text
// twice.
//
// This drives the REAL StreamBridge rather than a hand-built
// InterruptState, because the hand-built fixture is exactly what hid
// the bug.
//
// MUTATION EVIDENCE (run and confirmed to fail):
//   - PersistInterrupt using is.markedText() instead of
//     is.markedSegment() -> the preamble is persisted twice and the
//     occurrence assertion fails.
//   - dropping the StreamEventMoveStart case from StreamBridge.Emit ->
//     segmentStart never advances, PartialSegment returns the whole
//     accumulation, same failure.
//   - dropping the held-prefix absorb from RecordPartial -> the
//     stopped-after-the-fire sub-case persists the segment twice.
func TestMoves_InterruptPersistsOnlyTheInFlightSegment(t *testing.T) {
	t.Parallel()

	// countOccurrences totals how many persisted move entries contain s.
	countOccurrences := func(entries []coreag.HistoryEntry, s string) int {
		n := 0
		for _, e := range entries {
			n += strings.Count(e.Content, s)
		}
		return n
	}

	const preamble = "I'll read the file"
	const inflight = "Now I'll write"

	t.Run("stopped mid-stream", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		w := &recordingHistoryWriter{}
		bridge := NewStreamBridge(&recordingBroker{}, "sub-1", "sess-1")
		j := newTurnJournal(w, bridge.Emit, "sess-1", "span-1")

		// Fire 1: a preamble, then a tool.
		j.OpenAssistantSegment()
		bridge.Emit(coreag.StreamEvent{Kind: coreag.StreamEventText, Text: preamble})
		j.RecordAssistantMove(ctx, preamble)
		call := coreag.ToolCall{ID: "tu-1", Name: "fs__read"}
		j.RecordToolCall(ctx, call)
		j.RecordToolResult(ctx, call, coreag.ToolResult{Content: "contents"})

		// Fire 2 starts streaming; the user hits Stop.
		j.OpenAssistantSegment()
		bridge.Emit(coreag.StreamEvent{Kind: coreag.StreamEventText, Text: inflight})

		is := NewInterruptState(bridge, nil)
		if !strings.Contains(is.PartialText, preamble) {
			t.Fatalf("precondition broken: the bridge is supposed to accumulate the whole turn, got %q", is.PartialText)
		}
		is.PersistInterrupt(ctx, "sess-1", w, j)
		j.Finish(ctx)

		got := moveEntries(w)
		if n := countOccurrences(got, preamble); n != 1 {
			t.Errorf("fire 1's text appears %d times across the turn's moves, want exactly 1: %s",
				n, describeMoves(got))
		}
		if n := countOccurrences(got, inflight); n != 1 {
			t.Errorf("the interrupted segment appears %d times, want exactly 1: %s",
				n, describeMoves(got))
		}
	})

	t.Run("stopped after the fire completed", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		w := &recordingHistoryWriter{}
		bridge := NewStreamBridge(&recordingBroker{}, "sub-1", "sess-1")
		j := newTurnJournal(w, bridge.Emit, "sess-1", "span-1")

		// A single fire that finished streaming — its text is parked,
		// waiting to learn whether session_write claims it — and the
		// stop lands in that window.
		j.OpenAssistantSegment()
		bridge.Emit(coreag.StreamEvent{Kind: coreag.StreamEventText, Text: inflight})
		j.RecordAssistantMove(ctx, inflight)

		is := NewInterruptState(bridge, nil)
		is.PersistInterrupt(ctx, "sess-1", w, j)
		j.Finish(ctx)

		got := moveEntries(w)
		if len(got) != 1 {
			t.Fatalf("persisted %d moves for one interrupted fire, want 1: %s", len(got), describeMoves(got))
		}
		if n := countOccurrences(got, inflight); n != 1 {
			t.Errorf("the parked segment appears %d times, want exactly 1: %s", n, describeMoves(got))
		}
		if got[0].MoveIndex != 0 {
			t.Errorf("partial MoveIndex = %d, want 0 (the position already announced)", got[0].MoveIndex)
		}
		if got[0].MoveKind != moveKindAssistantMove {
			t.Errorf("partial kind = %q, want %q", got[0].MoveKind, moveKindAssistantMove)
		}
	})
}

// TestMoves_InertWithoutTurnSpan pins the degenerate case: with no user
// message to span from, the journal writes classic entries and stamps
// nothing. A span-less move is unorderable and AppendTranscriptEntry
// rejects it, so the alternative to this is a failed run.
//
// MUTATION EVIDENCE (run and confirmed to fail): drop the
// `j.spanID != ""` clause from records() -> the entries come back
// stamped with an empty TurnSpanID and this test fails.
func TestMoves_InertWithoutTurnSpan(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w := &recordingHistoryWriter{}
	j := newTurnJournal(w, nil, "sess-1", "")

	j.OpenAssistantSegment()
	j.RecordAssistantMove(ctx, "text")
	j.RecordToolCall(ctx, coreag.ToolCall{ID: "t", Name: "a__b"})
	if _, err := j.AppendEntry(ctx, "sess-1", coreag.HistoryEntry{Role: "assistant", Content: "answer"}); err != nil {
		t.Fatalf("AppendEntry: %v", err)
	}
	j.Finish(ctx)

	if moves := moveEntries(w); len(moves) != 0 {
		t.Fatalf("inert journal stamped %d moves: %s", len(moves), describeMoves(moves))
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.calls) != 1 || w.calls[0].content != "answer" {
		t.Errorf("expected exactly the classic assistant row, got %+v", w.calls)
	}
}

// TestMoves_RevisedAnswerKeepsBothTheDraftAndTheFinal pins the other
// half of the final-promotion rule: when the exit gate or the
// escalation ladder REVISES the draft, the model's own last segment and
// the returned answer are different facts and both are recorded.
//
// MUTATION EVIDENCE (run and confirmed to fail): make AppendEntry take
// the held index unconditionally (drop the `j.held == entry.Content`
// comparison) -> the draft is dropped, only one move is persisted, and
// the count assertion fails.
func TestMoves_RevisedAnswerKeepsBothTheDraftAndTheFinal(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w := &recordingHistoryWriter{}
	j := newTurnJournal(w, nil, "sess-1", "span-1")

	j.OpenAssistantSegment()
	j.RecordAssistantMove(ctx, "half-finished draft")
	if _, err := j.AppendEntry(ctx, "sess-1", coreag.HistoryEntry{
		Role: "assistant", Content: "the revised answer",
	}); err != nil {
		t.Fatalf("AppendEntry: %v", err)
	}

	got := moveEntries(w)
	if len(got) != 2 {
		t.Fatalf("persisted %d moves, want 2 (draft + revised final): %s", len(got), describeMoves(got))
	}
	if got[0].MoveKind != moveKindAssistantMove || got[0].Content != "half-finished draft" {
		t.Errorf("move 0 = %+v, want the draft as an assistant_move", got[0])
	}
	if got[1].MoveKind != moveKindFinal || got[1].Content != "the revised answer" {
		t.Errorf("move 1 = %+v, want the revised answer as the final", got[1])
	}
	if got[0].MoveIndex != 0 || got[1].MoveIndex != 1 {
		t.Errorf("indices = %d,%d, want 0,1", got[0].MoveIndex, got[1].MoveIndex)
	}
}

// TestMoves_WhitespaceOnlyDifferenceIsNotARevision is the other side of
// that boundary (adversarial review of WP02). The absorb test is byte
// equality of the parked draft and the answer session_write returns; a
// graph that trims on its way to the write node — or a provider whose
// final Response drops a trailing newline the deltas carried — would
// make EVERY turn persist its answer twice, as a near-identical
// assistant_move followed by a final. A user cannot tell that pair from
// a bug, and WP03 would feed the model its own answer twice.
//
// MUTATION EVIDENCE (run and confirmed to fail): drop the TrimSpace
// from the absorb comparison in AppendEntry -> two moves are persisted
// and the count assertion fails.
func TestMoves_WhitespaceOnlyDifferenceIsNotARevision(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w := &recordingHistoryWriter{}
	j := newTurnJournal(w, nil, "sess-1", "span-1")

	j.OpenAssistantSegment()
	j.RecordAssistantMove(ctx, "the answer is 42\n")
	if _, err := j.AppendEntry(ctx, "sess-1", coreag.HistoryEntry{
		Role: "assistant", Content: "the answer is 42",
	}); err != nil {
		t.Fatalf("AppendEntry: %v", err)
	}
	j.Finish(ctx)

	got := moveEntries(w)
	if len(got) != 1 {
		t.Fatalf("persisted %d moves for one answer, want 1: %s", len(got), describeMoves(got))
	}
	if got[0].MoveKind != moveKindFinal {
		t.Errorf("kind = %q, want %q", got[0].MoveKind, moveKindFinal)
	}
	// The row carries what the turn RETURNED, at the position the
	// stream already announced for it.
	if got[0].Content != "the answer is 42" {
		t.Errorf("content = %q, want the returned answer", got[0].Content)
	}
	if got[0].MoveIndex != 0 {
		t.Errorf("MoveIndex = %d, want 0 (the position the parked draft announced)", got[0].MoveIndex)
	}
}

// TestMoves_NonStreamingFireStillAnnouncesItsBoundary pins the
// unconditional half of the 1:1 rule: a provider that returns the whole
// body in the final response, streaming nothing, must still produce one
// boundary for the move it produced.
//
// MUTATION EVIDENCE (run and confirmed to fail): delete the `if idx < 0`
// allocate branch in RecordAssistantMove -> the move persists with
// index -1 and no boundary, and both assertions fail.
func TestMoves_NonStreamingFireStillAnnouncesItsBoundary(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w := &recordingHistoryWriter{}
	sink := &recordingSink{}
	j := newTurnJournal(w, sink.emit, "sess-1", "span-1")

	// No OpenAssistantSegment: no delta ever arrived.
	j.RecordAssistantMove(ctx, "whole body at once")
	j.RecordToolCall(ctx, coreag.ToolCall{ID: "t1", Name: "a__b"})
	j.Finish(ctx)

	got := moveEntries(w)
	if len(got) != 2 {
		t.Fatalf("persisted %d moves, want 2: %s", len(got), describeMoves(got))
	}
	if got[0].MoveIndex != 0 || got[1].MoveIndex != 1 {
		t.Errorf("indices = %d,%d, want 0,1", got[0].MoveIndex, got[1].MoveIndex)
	}
	marks := 0
	for _, ev := range sink.snapshot() {
		if ev.Kind == coreag.StreamEventMoveStart {
			marks++
		}
	}
	if marks != 2 {
		t.Errorf("boundaries = %d, want 2", marks)
	}
}

// TestMoves_ArgsSummaryNamesTypesOnly is the direct unit pin on the
// display-layer redaction helper.
//
// MUTATION EVIDENCE (run and confirmed to fail): return
// fmt.Sprintf("%s=%v", k, args[k]) from displayArgsSummary -> every
// case below fails.
func TestMoves_ArgsSummaryNamesTypesOnly(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		args map[string]any
		want string
	}{
		{"t", nil, "t()"},
		{"t", map[string]any{"s": "secret-value"}, "t(s=<string>)"},
		{"t", map[string]any{"n": float64(3), "b": true}, "t(b=<bool>, n=<number>)"},
		{"t", map[string]any{"l": []any{1, 2}, "o": map[string]any{"k": "v"}}, "t(l=<list>, o=<object>)"},
		{"t", map[string]any{"z": nil}, "t(z=<null>)"},
	}
	for _, tc := range cases {
		if got := displayArgsSummary(tc.name, tc.args); got != tc.want {
			t.Errorf("displayArgsSummary(%q, %v) = %q, want %q", tc.name, tc.args, got, tc.want)
		}
	}
}

// TestMoves_ArgsSummaryHidesValuesInEveryShape is the adversarial
// redaction fixture (spec §4, added by the adversarial review of WP02).
//
// The existing pins use flat, obviously-secret-looking arguments. A
// redaction contract is only worth anything if it holds for the shapes
// a real leak takes: the value nested two objects deep, the value
// inside an array, a value long enough that a "safe prefix" would be
// tempting, invalid UTF-8, and — the one that defeats key-name
// denylists — a secret under a completely innocuous key.
//
// The assertion is deliberately universal rather than per-case: NO
// substring of any value may appear in the summary, whatever its
// shape. Only key names and coarse type words are allowed out.
//
// MUTATION EVIDENCE (run and confirmed to fail):
//   - `%s=%v` instead of `%s=<%s>` in displayArgsSummary -> every
//     nested / array / long / innocuous case leaks and this fails;
//   - making argTypeName return a length or a prefix for strings
//     (`fmt.Sprintf("string:%d", len(s))` is the tempting version) ->
//     the long-value case fails on the leaked length digits.
func TestMoves_ArgsSummaryHidesValuesInEveryShape(t *testing.T) {
	t.Parallel()

	const (
		nested    = "AKIAIOSFODNN7NESTED"
		inArray   = "ghp_arrayElementSecret000"
		longValue = "BEGIN-PRIVATE-KEY-" +
			"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" +
			"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb-END"
		innocuous = "hunter2-under-a-boring-key"
	)
	badBytes := string([]byte{0xff, 0xfe, 0x00, 0x41, 0x42})

	args := map[string]any{
		// Two objects deep.
		"config": map[string]any{
			"auth": map[string]any{"aws_access_key_id": nested},
		},
		// Inside an array, inside an object.
		"headers": []any{
			map[string]any{"Authorization": "Bearer " + inArray},
		},
		// Long enough that a truncating "summary" would still leak.
		"body": longValue,
		// Invalid UTF-8 — must not be echoed, mangled or not.
		"raw": badBytes,
		// The one a key-name denylist misses entirely.
		"label": innocuous,
		// A non-JSON Go value, to pin the default arm of argTypeName.
		"opaque": struct{ Secret string }{Secret: nested},
	}

	got := displayArgsSummary("kenaz__write_file", args)

	for _, secret := range []string{nested, inArray, longValue, innocuous, badBytes} {
		if strings.Contains(got, secret) {
			t.Errorf("summary leaked a value: %q contains %q", got, secret)
		}
	}
	// Sub-strings too: a truncating implementation leaks a prefix.
	for _, frag := range []string{"AKIA", "ghp_", "BEGIN-PRIVATE-KEY", "hunter2", "Bearer"} {
		if strings.Contains(got, frag) {
			t.Errorf("summary leaked a value fragment %q: %q", frag, got)
		}
	}
	// No length hint either — the long value's size must not be inferable.
	if strings.ContainsAny(got, "0123456789") {
		t.Errorf("summary carries digits, which is where a length hint hides: %q", got)
	}
	want := "kenaz__write_file(body=<string>, config=<object>, headers=<list>, " +
		"label=<string>, opaque=<value>, raw=<string>)"
	if got != want {
		t.Errorf("summary = %q, want %q", got, want)
	}
}

// TestMoves_KindVocabularyMatchesTheStore pins the restated vocabulary
// (added by the adversarial review of WP02).
//
// moves.go restates session.MoveKind's four strings because the chat
// package cannot import core/session, and its comment says "these four
// strings must equal it" — which, until this test, nothing enforced.
// The failure mode of drift is nastier than a compile error: the store
// validates the kind against session.MoveKinds() and
// turnJournal.persist logs-and-continues on error, so a rename in
// core/session would silently stop persisting EVERY move while the run
// and the stream both looked healthy.
//
// The literal list here is the same one
// core/session.TestMoveKinds_MatchesWireVocabulary asserts from the
// other side, so a rename cannot land green on either side alone.
func TestMoves_KindVocabularyMatchesTheStore(t *testing.T) {
	t.Parallel()
	got := []string{
		moveKindAssistantMove, moveKindToolCall, moveKindToolResult, moveKindFinal,
	}
	want := []string{"assistant_move", "tool_call", "tool_result", "final"}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("move kind %d = %q, want %q — core/session.MoveKinds() is the "+
				"canonical list and the store rejects anything else", i, got[i], want[i])
		}
	}
}

// describeMoves renders a move slice compactly for failure messages.
func describeMoves(es []coreag.HistoryEntry) string {
	parts := make([]string, 0, len(es))
	for _, e := range es {
		parts = append(parts, fmt.Sprintf("%d:%s", e.MoveIndex, e.MoveKind))
	}
	return "[" + strings.Join(parts, " ") + "]"
}

// ---------------------------------------------------------------------------
// model-moves-transcript-01PMCH01 WP04 — the chip's two facts.
//
// WP02 shipped the boundary with a tool's identity but nothing a chip
// could render: no args summary (so a live chip and a reloaded chip
// would disagree the moment the row landed) and no error flag (so
// "error" was a chip state nothing could ever reach and every failed
// tool would read as "ok"). These pin both, on the stream AND on the
// persisted row, because reload parity is the requirement — not just
// live rendering.
// ---------------------------------------------------------------------------

// TestMoves_ToolCallBoundaryCarriesTheArgsSummary asserts the live chip
// shows exactly what the reloaded chip will: the boundary's ArgsSummary
// equals the tool_call entry's Content, and neither is the raw value.
//
// MUTATION EVIDENCE (run and confirmed to fail): drop `argsSummary` from
// the moveDetail in RecordToolCall -> the boundary carries "" and the
// equality assertion fails.
func TestMoves_ToolCallBoundaryCarriesTheArgsSummary(t *testing.T) {
	t.Parallel()
	const secret = "sk-live-DO-NOT-STREAM-THIS"

	reg := &scriptedRegistry{}
	args, err := json.Marshal(map[string]any{"path": "/etc/kenaz.toml", "token": secret})
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	reg.push(toolTurn("writing", "tu-1", "fs__write", string(args)))
	reg.push(textTurn("done"))

	pool := &scriptedPool{entries: []ToolEntry{{Server: "fs", Name: "write"}}}
	runner, broker, writer := buildMoveRunner(t, reg, pool)
	if _, err := runner.StartStream(context.Background(), "p", "s", "", "write it"); err != nil {
		t.Fatalf("StartStream: %v", err)
	}
	if closed := waitForClosed(t, broker); closed.Reason == "backend-error" {
		t.Fatalf("run failed: %s", closed.Message)
	}

	var callMark *corellm.MoveBoundary
	for i, m := range boundaries(broker) {
		if m.Kind == moveKindToolCall {
			callMark = &boundaries(broker)[i]
			break
		}
	}
	if callMark == nil {
		t.Fatal("no tool_call boundary on the stream")
	}
	if callMark.ArgsSummary == "" {
		t.Fatal("tool_call boundary carries no args summary — a live chip would show only a name")
	}
	if strings.Contains(callMark.ArgsSummary, secret) {
		t.Fatalf("the boundary streamed a raw argument value: %q", callMark.ArgsSummary)
	}

	var entry *coreag.HistoryEntry
	for i, e := range moveEntries(writer) {
		if e.MoveKind == moveKindToolCall {
			entry = &moveEntries(writer)[i]
			break
		}
	}
	if entry == nil {
		t.Fatal("no tool_call entry persisted")
	}
	// The live chip and the reloaded chip read the same string. This is
	// the whole reason the field exists.
	if callMark.ArgsSummary != entry.Content {
		t.Errorf("boundary summary %q != persisted summary %q — live and reload would disagree",
			callMark.ArgsSummary, entry.Content)
	}
}

// TestMoves_FailedToolIsErrorOnBoundaryAndRow asserts a failed tool
// reaches the chip as an error BOTH live and on reload, and that a
// successful one does not.
//
// MUTATION EVIDENCE (run and confirmed to fail):
//   - drop `isError` from the moveDetail in RecordToolResult -> the
//     boundary assertion fails (live chip reads "ok" for a failed tool).
//   - drop IsError from the ToolCallRequest RecordToolResult persists ->
//     the persisted assertion fails (a reload downgrades the failure).
func TestMoves_FailedToolIsErrorOnBoundaryAndRow(t *testing.T) {
	t.Parallel()

	reg := &scriptedRegistry{}
	reg.push(toolTurn("reading", "tu-ok", "fs__read", `{"path":"a"}`))
	reg.push(toolTurn("running", "tu-bad", "sh__exec", `{"cmd":"b"}`))
	reg.push(textTurn("done"))

	pool := &scriptedPool{
		entries: []ToolEntry{{Server: "fs", Name: "read"}, {Server: "sh", Name: "exec"}},
		failOn:  map[string]error{"sh__exec": fmt.Errorf("permission denied")},
	}
	runner, broker, writer := buildMoveRunner(t, reg, pool)
	if _, err := runner.StartStream(context.Background(), "p", "s", "", "go"); err != nil {
		t.Fatalf("StartStream: %v", err)
	}
	waitForClosed(t, broker)

	gotMarks := map[string]bool{}
	for _, m := range boundaries(broker) {
		if m.Kind == moveKindToolResult {
			gotMarks[m.ToolCallID] = m.IsError
		}
	}
	if len(gotMarks) != 2 {
		t.Fatalf("want 2 tool_result boundaries, got %d: %+v", len(gotMarks), gotMarks)
	}
	if gotMarks["tu-ok"] {
		t.Error("the successful tool's boundary says is_error — the chip would show a false failure")
	}
	if !gotMarks["tu-bad"] {
		t.Error("the failed tool's boundary does not say is_error — the chip would read ok")
	}

	gotRows := map[string]bool{}
	for _, e := range moveEntries(writer) {
		if e.MoveKind != moveKindToolResult || len(e.ToolCalls) == 0 {
			continue
		}
		gotRows[e.ToolCalls[0].ID] = e.ToolCalls[0].IsError
	}
	if len(gotRows) != 2 {
		t.Fatalf("want 2 persisted tool_result rows, got %d: %+v", len(gotRows), gotRows)
	}
	if gotRows["tu-ok"] {
		t.Error("the successful tool persisted is_error")
	}
	if !gotRows["tu-bad"] {
		t.Error("the failed tool did not persist is_error — a reload would downgrade it to ok")
	}
}
