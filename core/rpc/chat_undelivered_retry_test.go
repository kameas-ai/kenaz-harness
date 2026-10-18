package rpc

// undelivered-message-retry (owner dogfood, 2026-10-07; OpenRouter,
// moonshotai/kimi-k3): the account ran out of credits, the provider
// answered 402 before a single token, and the only way back was to RETYPE
// the message — a second user row — because nothing said the first one
// never reached the model and nothing offered to re-run it.
//
// This drives the production chain end to end on REAL sqlite, exactly as
// the chat surface does:
//
//	Sessions_AppendMessage                 (the user row, once)
//	  -> LLM_StartStream                   (run 1: provider answers 402)
//	  -> LLM_StartStream again, no append  (the Retry: run 2 succeeds)
//
// and asserts:
//
//	(a) run 1's llm:stream-closed says delivered=false, class
//	    user_actionable, code payment_required, names the user row, and
//	    reads "Out of credits with OpenRouter";
//	(b) run 1's outcome is persisted on its session_turn_runs row
//	    (failed / delivered=0 / classified) — what survives a reload;
//	(c) the Retry appends NO user row: exactly one user row before and
//	    after, and both runs carry it as their turn span;
//	(d) run 2's outcome is completed/delivered, and its answer is spanned
//	    on the same user row;
//	(e) fleet context-sync saw the user turn exactly once — the Retry is
//	    not a second announcement.

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	coreag "github.com/kameas-ai/kenaz-harness/core/agentgraph"
	corellm "github.com/kameas-ai/kenaz-harness/core/llm"
	graphview "github.com/kameas-ai/kenaz-harness/core/rpc/views/agentgraph"
	"github.com/kameas-ai/kenaz-harness/core/rpc/views/agentgraph/chat"
	llmview "github.com/kameas-ai/kenaz-harness/core/rpc/views/llm"
	"github.com/kameas-ai/kenaz-harness/core/rpc/views/sessions"
	"github.com/kameas-ai/kenaz-harness/core/session"
)

// scriptedFailRegistry fails the first `failures` Stream calls with err,
// then serves the same one-shot "ok" stream recordingRegistry serves.
type scriptedFailRegistry struct {
	recordingRegistry
	mu       sync.Mutex
	failures int
	err      error
}

func (r *scriptedFailRegistry) Profile(string) (corellm.ProviderProfile, error) {
	return corellm.ProviderProfile{ID: "or-profile", Kind: "openrouter"}, nil
}

func (r *scriptedFailRegistry) Stream(ctx context.Context, req corellm.GenerationRequest) (corellm.Stream, error) {
	r.mu.Lock()
	if r.failures > 0 {
		r.failures--
		r.mu.Unlock()
		return nil, r.err
	}
	r.mu.Unlock()
	return r.recordingRegistry.Stream(ctx, req)
}

// payloadBroker records every closed payload, race-safely.
type payloadBroker struct {
	mu     sync.Mutex
	closed []chat.StreamClosedPayload
}

func (b *payloadBroker) Emit(topic string, payload any) {
	if topic != "llm:stream-closed" {
		return
	}
	p, ok := payload.(chat.StreamClosedPayload)
	if !ok {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.closed = append(b.closed, p)
}

func (b *payloadBroker) waitClosed(t *testing.T, n int) chat.StreamClosedPayload {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		b.mu.Lock()
		if len(b.closed) >= n {
			p := b.closed[n-1]
			b.mu.Unlock()
			return p
		}
		b.mu.Unlock()
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("did not observe %d llm:stream-closed within 10s", n)
	return chat.StreamClosedPayload{}
}

func TestChatTurn_UndeliveredThenRetry_SameUserRow(t *testing.T) {
	ctx := context.Background()
	sessMgr, attMgr := newSQLTestStores(t)

	graphMgr, err := graphview.NewManager()
	if err != nil {
		t.Fatalf("graphview.NewManager: %v", err)
	}
	syncRec := &syncEventRecorder{}
	historyAdapter := &sessionHistoryReader{mgr: sessMgr, syncHook: syncRec.hook}
	pay := &corellm.ErrPaymentRequired{Status: 402, Message: "This request requires more credits, or fewer max_tokens."}
	reg := &scriptedFailRegistry{
		failures: 1,
		err: &corellm.ErrProviderPaymentRequired{
			Provider: "openrouter", ProfileID: "or-profile", ModelID: "moonshotai/kimi-k3",
			Reason: pay.Message, Cause: pay,
		},
	}
	broker := &payloadBroker{}
	runner, err := chat.New(chat.Config{
		Kernel:   graphMgr.Kernel(),
		Registry: reg,
		Broker:   broker,
		History: chatSessionMessageReader{
			inner:            historyAdapter,
			moveFidelityDial: func() bool { return false },
		},
		HistoryWriter: &llmHistoryWriter{inner: historyAdapter},
		TurnSpan:      chatTurnSpanReader{mgr: sessMgr},
		TurnRuns:      sessMgr,
		GraphLoader: func() (coreag.Graph, error) {
			g, gerr := graphMgr.LoadGraphSpec("chat_default")
			if gerr != nil {
				return g, gerr
			}
			return coreag.GateAgenticTurnRouting(g, false), nil
		},
		MaxTurns: func() int { return 5 },
	})
	if err != nil {
		t.Fatalf("chat.New: %v", err)
	}
	llmAPI := llmview.New(llmview.Config{History: historyAdapter, ChatRunner: runner})
	sessAPI := sessions.NewManagerAPIWithAttachments(sessMgr, attMgr)

	rec, err := sessMgr.Create(ctx, "undelivered probe")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	sid := rec.ID
	userRow, err := sessAPI.AppendMessage(ctx, sid, "user", "summarise the release notes")
	if err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}

	// Run 1: the provider refuses before any token.
	run1, err := llmAPI.StartStream(ctx, "or-profile", sid, "")
	if err != nil {
		t.Fatalf("StartStream (run 1): %v", err)
	}
	closed1 := broker.waitClosed(t, 1)

	// (a) the live close carries the classified, undelivered failure.
	if closed1.Reason != "backend-error" || closed1.Delivered {
		t.Fatalf("(a) run 1 close: reason=%q delivered=%v, want backend-error/false", closed1.Reason, closed1.Delivered)
	}
	if closed1.FailureClass != string(corellm.FailureUserActionable) || closed1.FailureCode != corellm.FailureCodePaymentRequired {
		t.Errorf("(a) class/code = %q/%q", closed1.FailureClass, closed1.FailureCode)
	}
	if closed1.FailureStatus != 402 || closed1.FailureProvider != "openrouter" {
		t.Errorf("(a) status/provider = %d/%q", closed1.FailureStatus, closed1.FailureProvider)
	}
	if closed1.FailureSummary != "Out of credits with OpenRouter" {
		t.Errorf("(a) summary = %q", closed1.FailureSummary)
	}
	if closed1.TurnSpanID != userRow.ID {
		t.Errorf("(a) turn_span_id = %q, want the user row %q", closed1.TurnSpanID, userRow.ID)
	}

	// (b) the outcome is durable.
	runs, err := sessMgr.ListTurnRuns(ctx, sid)
	if err != nil || len(runs) != 1 {
		t.Fatalf("(b) ListTurnRuns after run 1 = %+v (err %v), want 1", runs, err)
	}
	o := runs[0].Outcome
	if runs[0].RunID != run1 || o.Outcome != session.TurnOutcomeFailed || o.Delivered ||
		o.FailureClass != string(corellm.FailureUserActionable) || o.FailureCode != corellm.FailureCodePaymentRequired ||
		o.FailureStatus != 402 || o.FailureSummary != "Out of credits with OpenRouter" || o.FinishedAt.IsZero() {
		t.Errorf("(b) persisted outcome = %+v", runs[0])
	}

	usersBefore := countUserRows(t, sessMgr, sid)

	// The Retry: the same entry point a fresh send uses, with no append.
	run2, err := llmAPI.StartStream(ctx, "or-profile", sid, "")
	if err != nil {
		t.Fatalf("StartStream (retry): %v", err)
	}
	closed2 := broker.waitClosed(t, 2)
	if closed2.Reason != "completed" || !closed2.Delivered || closed2.FailureClass != "" {
		t.Fatalf("(d) retry close = %+v", closed2)
	}

	// (c) no second user row; both runs span the one row.
	if after := countUserRows(t, sessMgr, sid); after != usersBefore || after != 1 {
		t.Errorf("(c) user rows before/after retry = %d/%d, want 1/1", usersBefore, after)
	}
	runs, err = sessMgr.ListTurnRuns(ctx, sid)
	if err != nil || len(runs) != 2 {
		t.Fatalf("(c) ListTurnRuns after retry = %+v (err %v), want 2", runs, err)
	}
	for _, r := range runs {
		if r.TurnSpanID != userRow.ID {
			t.Errorf("(c) run %s spans %q, want %q", r.RunID, r.TurnSpanID, userRow.ID)
		}
	}
	// (d) run 2 is the delivered one, and its answer hangs off the row.
	var r2 session.TurnRun
	for _, r := range runs {
		if r.RunID == run2 {
			r2 = r
		}
	}
	if r2.Outcome.Outcome != session.TurnOutcomeCompleted || !r2.Outcome.Delivered {
		t.Errorf("(d) run 2 outcome = %+v", r2.Outcome)
	}
	stored, _ := sessMgr.ListMessages(ctx, sid)
	answered := false
	for _, m := range stored {
		if m.Role == session.RoleAssistant && m.TurnSpanID() == userRow.ID {
			answered = true
		}
	}
	if !answered {
		t.Error("(d) no assistant row spans the retried user message")
	}

	// (f) a stale Retry — the same no-append dispatch, from a second
	// window still showing the badge — is refused: the message already
	// reached the model. No run is started and no row is written.
	if _, err := llmAPI.StartStream(ctx, "or-profile", sid, ""); !errors.Is(err, llmview.ErrTurnAlreadyDelivered) {
		t.Errorf("(f) stale retry err = %v, want ErrTurnAlreadyDelivered", err)
	}
	if runs, _ := sessMgr.ListTurnRuns(ctx, sid); len(runs) != 2 {
		t.Errorf("(f) a refused retry recorded a run: %d runs", len(runs))
	}
	if n := countUserRows(t, sessMgr, sid); n != 1 {
		t.Errorf("(f) user rows after refused retry = %d", n)
	}
	// The normal path is unaffected: append, then StartStream.
	if _, err := sessAPI.AppendMessage(ctx, sid, "user", "next question"); err != nil {
		t.Fatalf("AppendMessage (next): %v", err)
	}
	if _, err := llmAPI.StartStream(ctx, "or-profile", sid, ""); err != nil {
		t.Fatalf("(f) fresh send after a delivered turn refused: %v", err)
	}
	broker.waitClosed(t, 3)

	// (e) one announcement per user turn — the Retry added none.
	userTurns := 0
	for _, ev := range syncRec.snapshot() {
		if ev["role"] == "user" {
			userTurns++
		}
	}
	if userTurns != 2 {
		t.Errorf("(e) fleet context-sync saw %d user-turn events, want 2 (two user turns; the Retry must not re-announce): %+v",
			userTurns, syncRec.snapshot())
	}
}

func countUserRows(t *testing.T, mgr *session.Manager, sid string) int {
	t.Helper()
	stored, err := mgr.ListMessages(context.Background(), sid)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	n := 0
	for _, m := range stored {
		if m.Role == session.RoleUser {
			n++
		}
	}
	return n
}

// blockingRegistry parks every Stream call until release is closed, so a
// run stays in flight for as long as the test needs.
type blockingRegistry struct {
	recordingRegistry
	release chan struct{}
}

func (r *blockingRegistry) Profile(string) (corellm.ProviderProfile, error) {
	return corellm.ProviderProfile{ID: "or-profile", Kind: "openrouter"}, nil
}

func (r *blockingRegistry) Stream(ctx context.Context, req corellm.GenerationRequest) (corellm.Stream, error) {
	select {
	case <-r.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return r.recordingRegistry.Stream(ctx, req)
}

// TestChatTurn_RetryWhileInFlight_Refused: two clients retrying the same
// message at once must not start two runs of one turn. The second
// no-append StartStream, while the first run is still executing, is
// refused with ErrTurnRetryInFlight; real sqlite.
func TestChatTurn_RetryWhileInFlight_Refused(t *testing.T) {
	ctx := context.Background()
	sessMgr, attMgr := newSQLTestStores(t)
	graphMgr, err := graphview.NewManager()
	if err != nil {
		t.Fatalf("graphview.NewManager: %v", err)
	}
	historyAdapter := &sessionHistoryReader{mgr: sessMgr}
	reg := &blockingRegistry{release: make(chan struct{})}
	broker := &payloadBroker{}
	runner, err := chat.New(chat.Config{
		Kernel:   graphMgr.Kernel(),
		Registry: reg,
		Broker:   broker,
		History: chatSessionMessageReader{
			inner:            historyAdapter,
			moveFidelityDial: func() bool { return false },
		},
		HistoryWriter: &llmHistoryWriter{inner: historyAdapter},
		TurnSpan:      chatTurnSpanReader{mgr: sessMgr},
		TurnRuns:      sessMgr,
		GraphLoader: func() (coreag.Graph, error) {
			g, gerr := graphMgr.LoadGraphSpec("chat_default")
			if gerr != nil {
				return g, gerr
			}
			return coreag.GateAgenticTurnRouting(g, false), nil
		},
		MaxTurns: func() int { return 5 },
	})
	if err != nil {
		t.Fatalf("chat.New: %v", err)
	}
	llmAPI := llmview.New(llmview.Config{History: historyAdapter, ChatRunner: runner})
	sessAPI := sessions.NewManagerAPIWithAttachments(sessMgr, attMgr)
	rec, err := sessMgr.Create(ctx, "in-flight probe")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := sessAPI.AppendMessage(ctx, rec.ID, "user", "hello"); err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}
	if _, err := llmAPI.StartStream(ctx, "or-profile", rec.ID, ""); err != nil {
		t.Fatalf("StartStream: %v", err)
	}
	if _, err := llmAPI.StartStream(ctx, "or-profile", rec.ID, ""); !errors.Is(err, llmview.ErrTurnRetryInFlight) {
		t.Errorf("concurrent retry err = %v, want ErrTurnRetryInFlight", err)
	}
	close(reg.release)
	broker.waitClosed(t, 1)
	if runs, _ := sessMgr.ListTurnRuns(ctx, rec.ID); len(runs) != 1 {
		t.Errorf("runs = %d, want 1 — the refused retry must not start a run", len(runs))
	}
}
