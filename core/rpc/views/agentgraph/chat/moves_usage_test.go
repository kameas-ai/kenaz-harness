package chat

// fix/usage-persists-on-every-move.
//
// Prior investigation (SQL against a copy of the owner's real database,
// corroborated live: the chat footer showed 96,745 tok / $0.1341 for a
// conversation OpenRouter's own dashboard billed at ~$13 on the same
// model and key — a ~100x undercount) diagnosed the mechanism: only a
// turn's terminal `kind='final'` transcript row ever reached
// HookPostLLM, because that hook fires exactly once per turn, from
// exec_state.go's sessionWriteExecutor — the sole caller of
// FirePostHooks. Every other assistant_move a multi-move (tool-using)
// turn produces is written by turnJournal.flushHeld straight to the
// HistoryWriter, bypassing that node entirely, so every Generate() call
// except the last one silently dropped its usage.
//
// These tests drive the REAL LLMProviderAdapter + REAL turnJournal +
// REAL kernel run (loadProductionChatGraph, exactly like
// TestMoves_FiveIterationTurnPersistsEveryMove in moves_test.go)
// against REAL sqlite (CLAUDE.md blind spot #2: session.NewMemoryStore
// skips SQL encode/decode and has hidden four SQL-path mutations
// before). The acceptance bar is deliberately the SUM across every
// billed Generate() call, not "usage exists" or "the number went up" —
// a fix that only captured 2 of 6 calls would pass a weaker assertion
// and still be a ~3x undercount of the kind the owner reported. Every
// scripted turn below carries a DISTINCT, strictly increasing
// prompt/completion/cost triple for exactly that reason: a partial
// capture cannot coincidentally sum to the right total when no two
// turns share a value.

import (
	"context"
	"fmt"
	"testing"

	coreag "github.com/kameas-ai/kenaz-harness/core/agentgraph"
	corellm "github.com/kameas-ai/kenaz-harness/core/llm"
	"github.com/kameas-ai/kenaz-harness/core/session"
	"github.com/kameas-ai/kenaz-harness/core/storage"
	storagesqlite "github.com/kameas-ai/kenaz-harness/core/storage/sqlite"
	"github.com/kameas-ai/kenaz-harness/core/usage"
)

// realHistoryWriter adapts a real *session.Manager to coreag.HistoryWriter
// by calling AppendTranscriptEntry directly — the same seam
// llmHistoryWriter.AppendEntry (core/rpc/api.go) uses in production,
// reproduced here because core/rpc cannot be imported from this package
// (it imports chat, not the other way around) and because this is a
// test fixture, not a second production writer.
func realHistoryWriter(mgr *session.Manager) coreag.HistoryWriter {
	return coreag.HistoryWriterFunc(func(ctx context.Context, sessionID string,
		entry coreag.HistoryEntry) (string, error) {
		msg, err := mgr.AppendTranscriptEntry(ctx, sessionID, session.TranscriptEntry{
			Role:          session.Role(entry.Role),
			Content:       entry.Content,
			Kind:          session.MoveKind(entry.MoveKind),
			MoveIndex:     entry.MoveIndex,
			TurnSpanID:    entry.TurnSpanID,
			ToolCalls:     testMoveToolCalls(entry.ToolCalls),
			ModelToolArgs: entry.ModelToolArgs,
		})
		if err != nil {
			return "", err
		}
		return msg.ID, nil
	})
}

// testMoveToolCalls mirrors moveToolCalls (core/rpc/api.go) minimally —
// display-layer redaction (Arguments left nil) is not this test's
// concern, but the shape must satisfy TranscriptEntry.validate().
func testMoveToolCalls(calls []coreag.ToolCallRequest) []session.ToolCall {
	if len(calls) == 0 {
		return nil
	}
	out := make([]session.ToolCall, 0, len(calls))
	for _, c := range calls {
		out = append(out, session.ToolCall{ID: c.ID, Name: c.Name, IsError: c.IsError})
	}
	return out
}

// testUsageHook reproduces the cost/source classification and the two
// writes core/rpc/api.go's production usageHookFn performs (usage.Add +
// SetLastUsage), against REAL sqlite via a REAL usage.Manager and a REAL
// *session.Manager — minus the broker publish, which no assertion here
// needs. This is deliberately the SAME translation, not an
// independently-invented one: mirroring api.go's classification is what
// makes this a faithful test of the wiring rather than a test of a
// stand-in that could drift from production and hide a real defect.
func testUsageHook(usageMgr usage.Manager, sessionMgr *session.Manager) UsageHookFunc {
	return func(ctx context.Context, sessionID, messageID, providerKind, modelID string, resp corellm.Response) {
		var costUSD *float64
		source := "unknown"
		switch {
		case resp.Cost.Source == "provider" && resp.Cost.Total > 0:
			v := resp.Cost.Total
			costUSD = &v
			source = "provider"
		case !resp.Cost.Indeterminate && resp.Cost.Total > 0:
			v := resp.Cost.Total
			costUSD = &v
			source = "derived"
		}
		_ = usageMgr.Add(ctx, usage.UsageTurn{
			SessionID:        sessionID,
			MessageID:        messageID,
			ProviderKind:     providerKind,
			ModelID:          modelID,
			PromptTokens:     resp.Usage.InputTokens,
			CompletionTokens: resp.Usage.OutputTokens,
			CostUSD:          costUSD,
			CostSource:       source,
		})
		costVal := 0.0
		if costUSD != nil {
			costVal = *costUSD
		}
		_ = sessionMgr.SetLastUsage(ctx, sessionID, session.LastUsage{
			PromptTokens:     resp.Usage.InputTokens,
			CompletionTokens: resp.Usage.OutputTokens,
			TotalTokens:      resp.Usage.InputTokens + resp.Usage.OutputTokens,
			CostUSD:          costVal,
			CostSource:       source,
		})
	}
}

// toolTurnWithUsage scripts a fire that streams text, asks for one tool,
// and reports the given per-call usage/cost — the shape a provider
// actually returns on every request, tool-using or not.
func toolTurnWithUsage(text, callID, toolName, argsJSON string, promptTok, complTok int, cost float64) scriptedTurn {
	return scriptedTurn{
		deltas: []corellm.StreamEvent{{Kind: corellm.StreamText, Text: text}},
		resp: corellm.Response{
			Content:      []corellm.ContentBlock{{Type: "text", Text: text}},
			FinishReason: "tool_use",
			ToolCalls: []corellm.ToolUse{
				{ID: callID, Name: toolName, Input: []byte(argsJSON)},
			},
			Usage: corellm.Usage{InputTokens: promptTok, OutputTokens: complTok},
			Cost:  corellm.Cost{Currency: "USD", Total: cost, Source: "provider"},
		},
	}
}

// textTurnWithUsage scripts a fire that streams text and returns it,
// reporting the given per-call usage/cost.
func textTurnWithUsage(text string, promptTok, complTok int, cost float64) scriptedTurn {
	return scriptedTurn{
		deltas: []corellm.StreamEvent{{Kind: corellm.StreamText, Text: text}},
		resp: corellm.Response{
			Content:      []corellm.ContentBlock{{Type: "text", Text: text}},
			FinishReason: "stop",
			Usage:        corellm.Usage{InputTokens: promptTok, OutputTokens: complTok},
			Cost:         corellm.Cost{Currency: "USD", Total: cost, Source: "provider"},
		},
	}
}

// noopMemoryStore satisfies coreag.MemoryStore with an always-succeeds
// no-op. Wiring a UsageHook makes chat_runner.go construct a real
// HookManager (coreag.NewHookManager(env.Memory, ...)) so the usage
// hook has a boundary to register on; HookManager.Fire (used by the
// unrelated ask_user post-user-message memory hook, not by anything
// this test exercises) calls mem.Write unconditionally and panics on a
// nil MemoryStore interface. Production always wires a real memory
// store before this point; these tests need a harmless stand-in so
// that pre-existing, unrelated hook path doesn't crash a test that
// isn't testing it.
type noopMemoryStore struct{}

func (noopMemoryStore) Write(context.Context, coreag.MemoryWrite) (string, bool, error) {
	return "", false, nil
}
func (noopMemoryStore) Read(context.Context, coreag.MemoryReadFilter) ([]coreag.MemoryHit, error) {
	return nil, nil
}

// buildMoveRunnerRealSQLite is buildMoveRunner's real-persistence
// sibling: same production Kernel/GraphLoader/MaxTurns wiring, but the
// HistoryWriter and UsageHook are bound to REAL sqlite through a REAL
// *session.Manager and a REAL usage.Manager, exactly the objects
// production wiring uses (core/rpc/api.go), instead of
// recordingHistoryWriter. A fake writer cannot exercise the SQL-path
// mutations these tests exist to pin (CLAUDE.md blind spot #2).
//
// graph is caller-supplied (loadProductionChatGraph or
// loadRoutedChatGraph) so the SAME real-sqlite/real-usage.Manager
// harness can drive either topology — round 2 of this fix needs the
// routed graph specifically, since the misattribution it found only
// exists where exit_gate sits between the loop and session_write.
func buildMoveRunnerRealSQLite(t *testing.T, reg *scriptedRegistry, pool *scriptedPool, graph coreag.Graph) (
	runner *ChatRunner, broker *recordingBroker, sessionMgr *session.Manager, usageMgr usage.Manager, db storage.DB) {
	t.Helper()

	cfg := storage.Config{
		DataDir:          t.TempDir(),
		EncryptionStatus: storage.EncryptionStatusDisabledWithDiskEncryption,
	}
	var err error
	db, err = storagesqlite.Open(cfg)
	if err != nil {
		t.Fatalf("storagesqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close(context.Background()) })

	store := session.NewSQLStore(session.NewStorageDB(db))
	sessionMgr = session.NewManager(store)
	usageMgr = usage.New(db)

	broker = &recordingBroker{}
	runner, err = New(Config{
		Kernel:        coreag.NewKernel(),
		Registry:      reg,
		Pool:          pool,
		Broker:        broker,
		HistoryWriter: realHistoryWriter(sessionMgr),
		History:       staticHistoryReader{},
		GraphLoader:   func() (coreag.Graph, error) { return graph, nil },
		MaxTurns:      func() int { return 25 },
		UsageHook:     testUsageHook(usageMgr, sessionMgr),
		EnvDefaults:   func(env *coreag.Env) { env.Memory = noopMemoryStore{} },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return runner, broker, sessionMgr, usageMgr, db
}

// usageRow is one session_messages row's usage columns, read back
// straight from real sqlite in persisted (sequence) order.
type usageRow struct {
	prompt, completion int
	cost               float64
	costSource         string
}

// readAssistantUsageRows queries session_messages directly (not through
// any Manager/Aggregate helper) so these tests assert on what actually
// landed in the table, in the order it landed, rather than trusting a
// second layer of Go code to summarize it correctly.
func readAssistantUsageRows(t *testing.T, db storage.DB, sessionID string) []usageRow {
	t.Helper()
	ctx := context.Background()
	rows, err := db.Reader().Query(ctx,
		`SELECT prompt_tokens, completion_tokens, COALESCE(cost_usd, 0.0), COALESCE(cost_source, '')
		 FROM session_messages
		 WHERE session_id = ? AND role = 'assistant' AND prompt_tokens IS NOT NULL
		 ORDER BY sequence ASC`,
		sessionID,
	)
	if err != nil {
		t.Fatalf("query session_messages: %v", err)
	}
	defer rows.Close()
	var out []usageRow
	for rows.Next() {
		var r usageRow
		if err := rows.Scan(&r.prompt, &r.completion, &r.cost, &r.costSource); err != nil {
			t.Fatalf("scan session_messages row: %v", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate session_messages: %v", err)
	}
	return out
}

func approxEqualUSD(a, b, tol float64) bool {
	d := a - b
	if d < 0 {
		d = -d
	}
	return d <= tol
}

// usageWant is one Generate() call's expected usage — the ground truth
// this test compares real sqlite rows against.
type usageWant struct {
	prompt, completion int
	cost               float64
}

// buildUsageAcceptanceFixture returns 5 tool-using intermediate turns
// plus 1 final answer turn, each with a DISTINCT, strictly increasing
// prompt/completion/cost triple. The increase mirrors the owner's own
// diagnosis: in a real agentic loop the LATER calls are the expensive
// ones because context has grown — the exact calls a final-only hook
// drops. Distinctness is what makes a partial-capture bug (e.g. "2 of
// 6") structurally unable to pass: no subset of these six values sums
// to the total of all six.
func buildUsageAcceptanceFixture() (reg *scriptedRegistry, pool *scriptedPool, wants []usageWant) {
	wants = []usageWant{
		{prompt: 1000, completion: 40, cost: 0.0110},
		{prompt: 1200, completion: 55, cost: 0.0143},
		{prompt: 1500, completion: 60, cost: 0.0180},
		{prompt: 1900, completion: 70, cost: 0.0231},
		{prompt: 2400, completion: 85, cost: 0.0298},
		{prompt: 3000, completion: 500, cost: 0.0530}, // the final answer — the expensive tail
	}
	reg = &scriptedRegistry{}
	for i := 0; i < 5; i++ {
		w := wants[i]
		reg.push(toolTurnWithUsage(
			fmt.Sprintf("step %d: looking it up", i+1),
			fmt.Sprintf("tu-%d", i+1),
			"search__web",
			fmt.Sprintf(`{"q":"question %d"}`, i+1),
			w.prompt, w.completion, w.cost,
		))
	}
	final := wants[5]
	reg.push(textTurnWithUsage("the answer is 42", final.prompt, final.completion, final.cost))
	pool = &scriptedPool{entries: []ToolEntry{{Server: "search", Name: "web"}}}
	return reg, pool, wants
}

// TestUsage_MultiMoveTurnPersistsUsageForEveryGenerateCall is the
// primary acceptance test for fix/usage-persists-on-every-move.
//
// MUTATION EVIDENCE (run and confirmed to fail, then reverted):
//   - comment out the `j.fireUsage(ctx, id, err, resp, providerKind,
//     modelID)` call in turnJournal.flushHeld -> only the final row
//     carries usage; len(rows) drops from 6 to 1 and the aggregate-sum
//     assertions fail (aggregate = 3000/500/$0.0530 instead of the sum
//     of all six).
func TestUsage_MultiMoveTurnPersistsUsageForEveryGenerateCall(t *testing.T) {
	ctx := context.Background()
	reg, pool, wants := buildUsageAcceptanceFixture()
	runner, broker, sessionMgr, usageMgr, db := buildMoveRunnerRealSQLite(t, reg, pool, loadProductionChatGraph(t))

	rec, err := sessionMgr.Create(ctx, "usage repro session")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	sessionID := rec.ID

	if _, err := runner.StartStream(ctx, "profile-1", sessionID, "", "find it"); err != nil {
		t.Fatalf("StartStream: %v", err)
	}
	if closed := waitForClosed(t, broker); closed.Reason == "backend-error" {
		t.Fatalf("run failed: %s", closed.Message)
	}

	// ---- assert on REAL rows in REAL sqlite, not on hook-invocation
	// counts or an in-memory fake -------------------------------------
	rows := readAssistantUsageRows(t, db, sessionID)
	if len(rows) != len(wants) {
		t.Fatalf("session_messages has %d assistant rows carrying usage, want exactly %d "+
			"(one per billed Generate() call) — got %+v", len(rows), len(wants), rows)
	}
	for i, w := range wants {
		got := rows[i]
		if got.prompt != w.prompt || got.completion != w.completion {
			t.Errorf("move %d tokens = %d/%d, want %d/%d — this is the exact class of bug that "+
				"silently dropped every non-final Generate() call's usage",
				i, got.prompt, got.completion, w.prompt, w.completion)
		}
		if !approxEqualUSD(got.cost, w.cost, 1e-9) {
			t.Errorf("move %d cost = %v, want %v", i, got.cost, w.cost)
		}
		if got.costSource != "provider" {
			t.Errorf("move %d cost_source = %q, want %q", i, got.costSource, "provider")
		}
	}

	// ---- the acceptance criterion: aggregate == SUM of every real
	// Generate() call's own usage. A fix that captured only some moves
	// (e.g. only the final, or only 2 of 6) would fail here even though
	// a weaker ">0" or "usage exists" assertion would pass it. ----------
	var wantPrompt, wantCompletion int
	var wantCost float64
	for _, w := range wants {
		wantPrompt += w.prompt
		wantCompletion += w.completion
		wantCost += w.cost
	}
	agg, err := usageMgr.GetSession(ctx, sessionID)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if agg.PromptTokens != wantPrompt || agg.CompletionTokens != wantCompletion {
		t.Errorf("aggregate tokens = %d/%d, want %d/%d (the sum of all %d Generate calls in this turn)",
			agg.PromptTokens, agg.CompletionTokens, wantPrompt, wantCompletion, len(wants))
	}
	if !approxEqualUSD(agg.CostUSD, wantCost, 1e-9) {
		t.Errorf("aggregate cost = %v, want %v (the sum of all %d Generate calls)", agg.CostUSD, wantCost, len(wants))
	}
	// MessageCount pins BOTH failure directions at once: fewer than
	// len(wants) is under-capture (the original bug); more is
	// double-counting (the final row counted twice — the regression this
	// fix must not introduce).
	if agg.MessageCount != len(wants) {
		t.Errorf("aggregate MessageCount = %d, want exactly %d — anything else means either under-capture "+
			"(a Generate call's usage was dropped) or double-counting (the final row's usage was recorded twice)",
			agg.MessageCount, len(wants))
	}

	// ---- sessions.last_usage_json reflects the LATEST move — the
	// context-window meter must not be frozen on an earlier move's
	// numbers, which is what "stale on reopen" (per-symptom brief) means
	// in practice. ------------------------------------------------------
	final := wants[len(wants)-1]
	last, err := sessionMgr.GetLastUsage(ctx, sessionID)
	if err != nil {
		t.Fatalf("GetLastUsage: %v", err)
	}
	if last.PromptTokens != final.prompt || last.CompletionTokens != final.completion {
		t.Errorf("last_usage_json tokens = %d/%d, want the LATEST move's %d/%d",
			last.PromptTokens, last.CompletionTokens, final.prompt, final.completion)
	}
	if !approxEqualUSD(last.CostUSD, final.cost, 1e-9) {
		t.Errorf("last_usage_json cost = %v, want the latest move's %v", last.CostUSD, final.cost)
	}
}

// TestUsage_InterruptedTurnStillPersistsItsLastMoveUsage is a bonus
// fix surfaced by the same mechanism: a turn that ends WITHOUT ever
// reaching session_write (budget cap, ask-pause, an interrupt) never
// fired the old final-only hook at all, so EVERY Generate call's usage
// in that turn was lost — not just the intermediate ones. Journal.Finish
// flushes the last held segment through the same fireUsage path
// flushHeld uses, so this case is fixed as a side effect rather than
// requiring separate wiring.
//
// MUTATION EVIDENCE (run and confirmed to fail, then reverted):
//   - drop the `j.fireUsage` call from flushHeld (same mutation as the
//     primary test) -> zero usage rows land for the interrupted move and
//     last_usage_json stays at its zero value.
func TestUsage_InterruptedTurnStillPersistsItsLastMoveUsage(t *testing.T) {
	ctx := context.Background()
	sink := &recordingSink{}

	cfg := storage.Config{
		DataDir:          t.TempDir(),
		EncryptionStatus: storage.EncryptionStatusDisabledWithDiskEncryption,
	}
	db, err := storagesqlite.Open(cfg)
	if err != nil {
		t.Fatalf("storagesqlite.Open: %v", err)
	}
	defer db.Close(context.Background())
	store := session.NewSQLStore(session.NewStorageDB(db))
	sessionMgr := session.NewManager(store)
	usageMgr := usage.New(db)

	rec, err := sessionMgr.Create(ctx, "interrupted usage session")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	sessionID := rec.ID
	if _, err := sessionMgr.AppendMessage(ctx, sessionID, session.Message{
		Role: session.RoleUser, Content: "start",
	}); err != nil {
		t.Fatalf("append user message: %v", err)
	}

	// Drive the journal directly (as moves_test.go's journal-level tests
	// do) with a REAL writer + a REAL usage hook, but a fake stream sink
	// — this test is about Finish()'s flush path, not the kernel run.
	hook := testUsageHook(usageMgr, sessionMgr)
	j := newTurnJournal(realHistoryWriter(sessionMgr), sink.emit, sessionID, "span-x", hook)

	j.OpenAssistantSegment()
	resp := corellm.Response{
		Usage: corellm.Usage{InputTokens: 4200, OutputTokens: 900},
		Cost:  corellm.Cost{Currency: "USD", Total: 0.0777, Source: "provider"},
	}
	j.RecordAssistantMove(ctx, "partial progress before the budget cap fired", resp, "anthropic", "claude-x")
	// The turn ends here — no tool call, no final AppendEntry. This is
	// exactly the "kernel run terminates before session_write" case:
	// under the OLD mechanism NOTHING would ever have recorded this
	// call's usage, because HookPostLLM only fires from session_write.
	j.Finish(ctx)

	rows := readAssistantUsageRows(t, db, sessionID)
	if len(rows) != 1 {
		t.Fatalf("interrupted turn persisted %d usage rows, want 1: %+v", len(rows), rows)
	}
	if rows[0].prompt != 4200 || rows[0].completion != 900 {
		t.Errorf("interrupted move tokens = %d/%d, want 4200/900", rows[0].prompt, rows[0].completion)
	}
	if !approxEqualUSD(rows[0].cost, 0.0777, 1e-9) {
		t.Errorf("interrupted move cost = %v, want 0.0777", rows[0].cost)
	}

	last, err := sessionMgr.GetLastUsage(ctx, sessionID)
	if err != nil {
		t.Fatalf("GetLastUsage: %v", err)
	}
	if last.PromptTokens != 4200 || last.CompletionTokens != 900 {
		t.Errorf("last_usage_json = %d/%d after an interrupted turn, want 4200/900 (previously: stuck at zero forever)",
			last.PromptTokens, last.CompletionTokens)
	}
}

// TestUsage_PartialAfterCompletedFireStillPersistsUsage covers the
// OTHER interrupt shape (moves_test.go's "stopped after the fire
// completed" sub-test of TestMoves_InterruptPersistsOnlyTheInFlightSegment):
// the fire finished — RecordAssistantMove parked a real usage snapshot
// — and the stop landed in the window before session_write claimed the
// text, so RecordPartial's prefix-match branch takes over the parked
// position. That branch has its own usage-firing path (distinct from
// flushHeld's), and this test is what proves it independently.
//
// MUTATION EVIDENCE (run and confirmed to fail, then reverted):
//   - drop the `j.fireUsage(ctx, id, err, resp, providerKind, modelID)`
//     call from RecordPartial's hadUsage branch -> 0 usage rows land for
//     the partial and the row-count assertion fails.
func TestUsage_PartialAfterCompletedFireStillPersistsUsage(t *testing.T) {
	ctx := context.Background()
	sink := &recordingSink{}

	cfg := storage.Config{
		DataDir:          t.TempDir(),
		EncryptionStatus: storage.EncryptionStatusDisabledWithDiskEncryption,
	}
	db, err := storagesqlite.Open(cfg)
	if err != nil {
		t.Fatalf("storagesqlite.Open: %v", err)
	}
	defer db.Close(context.Background())
	store := session.NewSQLStore(session.NewStorageDB(db))
	sessionMgr := session.NewManager(store)
	usageMgr := usage.New(db)

	rec, err := sessionMgr.Create(ctx, "partial usage session")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	sessionID := rec.ID
	if _, err := sessionMgr.AppendMessage(ctx, sessionID, session.Message{
		Role: session.RoleUser, Content: "start",
	}); err != nil {
		t.Fatalf("append user message: %v", err)
	}

	hook := testUsageHook(usageMgr, sessionMgr)
	j := newTurnJournal(realHistoryWriter(sessionMgr), sink.emit, sessionID, "span-y", hook)

	const text = "half-finished draft that the stop landed just after"
	resp := corellm.Response{
		Usage: corellm.Usage{InputTokens: 777, OutputTokens: 88},
		Cost:  corellm.Cost{Currency: "USD", Total: 0.0099, Source: "provider"},
	}
	j.OpenAssistantSegment()
	// The fire completed: RecordAssistantMove parked text AND its usage.
	j.RecordAssistantMove(ctx, text, resp, "anthropic", "claude-x")
	// The stop lands in the window before session_write claims it —
	// RecordPartial's prefix branch takes the parked position and its
	// usage snapshot.
	j.RecordPartial(ctx, text)

	rows := readAssistantUsageRows(t, db, sessionID)
	if len(rows) != 1 {
		t.Fatalf("partial-after-completed-fire persisted %d usage rows, want 1: %+v", len(rows), rows)
	}
	if rows[0].prompt != 777 || rows[0].completion != 88 {
		t.Errorf("partial move tokens = %d/%d, want 777/88", rows[0].prompt, rows[0].completion)
	}
	if !approxEqualUSD(rows[0].cost, 0.0099, 1e-9) {
		t.Errorf("partial move cost = %v, want 0.0099", rows[0].cost)
	}
}

// TestUsage_RoutedGraphFinalRowGetsTheChatMovesUsageNotTheExitGates is
// round 2 of fix/usage-persists-on-every-move: an independent reviewer
// built this exact reproduction (loadRoutedChatGraph + distinct usage
// per scripted call) and proved that round 1 left a misattribution on
// the ROUTED graph (AgenticTurnRouting on — built, gated off, not yet
// the shipped default): exit_gate (kind: review) makes its own real,
// costed Generate() call between the loop and assistant_write
// (exec_compute.go's reviewExecutor; see
// TestMoves_OnlyTheChatBoundModelNodeBecomesAMove above for where that
// call is independently proven to happen), and the persisted `final`
// row got the GATE's small verdict usage — not the chat answer's —
// because the final row sourced usage from
// LLMProviderAdapter.LastResponse(), a single mutable slot the gate's
// call overwrites after the chat move's own call already ran.
//
// The chat move's usage (1000/40/$0.0110) and the exit gate's verdict
// usage (50/8/$0.0009) are deliberately far apart and both nonzero, so
// this test cannot pass by accident — a fix that fires SOME response
// for the final row but the WRONG one fails exactly the same way the
// live reproduction did.
//
// MUTATION EVIDENCE (run and confirmed to fail, then reverted): in
// AppendEntry's absorbed branch, source resp/providerKind/modelID from
// j.lastResponseFn() (mimicking round 1's design) instead of
// j.heldResp/heldProviderKind/heldModelID -> the final row's usage
// becomes 50/8/$0.0009 (the gate's) instead of 1000/40/$0.0110 (the
// chat move's), and both assertions below fail.
func TestUsage_RoutedGraphFinalRowGetsTheChatMovesUsageNotTheExitGates(t *testing.T) {
	ctx := context.Background()

	const chatPrompt, chatCompletion = 1000, 40
	const chatCost = 0.0110
	const gatePrompt, gateCompletion = 50, 8
	const gateCost = 0.0009

	reg := &scriptedRegistry{}
	// 1. The chat move: the model answers "the answer is 42" and the
	// draft is approved unchanged (absorbed) below.
	reg.push(textTurnWithUsage("the answer is 42", chatPrompt, chatCompletion, chatCost))
	// 2. exit_gate's own real Generate() call: a JSON verdict, small and
	// cheap relative to the chat move — the shape a review/classifier
	// call actually has. Not tracked as a move (StreamToChat is false
	// for this node), but it DOES run through the same adapter and DOES
	// report real usage.
	reg.push(textTurnWithUsage(`{"verdict":"pass","reason":"looks right"}`, gatePrompt, gateCompletion, gateCost))

	pool := &scriptedPool{}
	graph := loadRoutedChatGraph(t)
	runner, broker, sessionMgr, usageMgr, db := buildMoveRunnerRealSQLite(t, reg, pool, graph)

	rec, err := sessionMgr.Create(ctx, "routed usage session")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	sessionID := rec.ID

	if _, err := runner.StartStream(ctx, "profile-1", sessionID, "", "ask"); err != nil {
		t.Fatalf("StartStream: %v", err)
	}
	if closed := waitForClosed(t, broker); closed.Reason == "backend-error" {
		t.Fatalf("run failed: %s", closed.Message)
	}

	rows := readAssistantUsageRows(t, db, sessionID)
	if len(rows) != 1 {
		t.Fatalf("routed turn persisted %d usage rows, want exactly 1 (the final — the gate's "+
			"verdict is not a move and gets no row of its own): %+v", len(rows), rows)
	}
	if rows[0].prompt != chatPrompt || rows[0].completion != chatCompletion {
		t.Errorf("final row tokens = %d/%d, want the CHAT MOVE's %d/%d — got the exit gate's "+
			"%d/%d instead if this reads like the misattribution bug",
			rows[0].prompt, rows[0].completion, chatPrompt, chatCompletion, gatePrompt, gateCompletion)
	}
	if !approxEqualUSD(rows[0].cost, chatCost, 1e-9) {
		t.Errorf("final row cost = %v, want the chat move's %v (not the exit gate's %v)",
			rows[0].cost, chatCost, gateCost)
	}

	agg, err := usageMgr.GetSession(ctx, sessionID)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if agg.PromptTokens != chatPrompt || agg.CompletionTokens != chatCompletion {
		t.Errorf("aggregate tokens = %d/%d, want the chat move's %d/%d",
			agg.PromptTokens, agg.CompletionTokens, chatPrompt, chatCompletion)
	}
	if !approxEqualUSD(agg.CostUSD, chatCost, 1e-9) {
		t.Errorf("aggregate cost = %v, want the chat move's %v", agg.CostUSD, chatCost)
	}
}
