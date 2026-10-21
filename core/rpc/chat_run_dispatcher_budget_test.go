package rpc

// Per-schedule context budget (scheduled-chat-context-budget, migration
// sessions/0349).
//
// Since sessions/0348 every fire of a schedule appends to ONE
// conversation, so every fire replays the whole history. The schedule now
// carries a budget: before a fire whose conversation (plus its new
// prompt) is over context_budget_tokens, the dispatcher keeps the newest
// keep_recent_runs runs verbatim and folds every older one into one
// rolling summary, updated incrementally on the next overflow.
//
// Real sqlite throughout (CLAUDE.md blind spot #2): the conversation, its
// soft-archived originals and the summary row go through the production
// session store; the compaction engine is the production one over the
// production MessageStore adapter; the summariser is the production
// compactionwiring.LLMCaller over a recording registry, so the per-call
// cost reaches the fire through the same ctx tally production uses. The
// chat runner is the production one over chat_default.yaml; only the
// chat model (steppedModel) and the summariser's provider are scripted.

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/agentgraph/compaction"
	compactionwiring "github.com/kameas-ai/kenaz-harness/core/agentgraph/compaction/wiring"
	corellm "github.com/kameas-ai/kenaz-harness/core/llm"
	"github.com/kameas-ai/kenaz-harness/core/rpc/views/agentgraph/chat"
	llmview "github.com/kameas-ai/kenaz-harness/core/rpc/views/llm"
	sessionsview "github.com/kameas-ai/kenaz-harness/core/rpc/views/sessions"
	"github.com/kameas-ai/kenaz-harness/core/scheduler"
	"github.com/kameas-ai/kenaz-harness/core/session"
)

// summaryRegistry is the summariser's provider: it records every request
// (prompt and tool catalog) and answers "SUMMARY-N" at summaryCost. While
// it serves a call it records whether the conversation was contained at
// that moment (containedAt), so a test can prove the summariser ran
// outside the scheduled turn's containment.
type summaryRegistry struct {
	fakeOverheadRegistry
	mu          sync.Mutex
	prompts     []string
	toolCounts  []int
	containedAt []bool
	contained   func() bool
	// onCall, when set, runs (unlocked) at the start of each call with
	// its 1-based number — to make something happen in the conversation
	// while the summariser is "thinking".
	onCall func(n int)
	calls  int
}

const summaryCost = 0.01

func (r *summaryRegistry) Stream(_ context.Context, req corellm.GenerationRequest) (corellm.Stream, error) {
	r.mu.Lock()
	r.calls++
	n, hook := r.calls, r.onCall
	r.mu.Unlock()
	if hook != nil {
		hook(n)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	var b strings.Builder
	for _, m := range req.Messages {
		for _, c := range m.Content {
			b.WriteString(c.Text)
		}
	}
	r.prompts = append(r.prompts, b.String())
	r.toolCounts = append(r.toolCounts, len(req.Tools))
	if r.contained != nil {
		r.containedAt = append(r.containedAt, r.contained())
	}
	return &summaryStream{text: fmt.Sprintf("SUMMARY-%d", len(r.prompts))}, nil
}

func (r *summaryRegistry) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.prompts...)
}

type summaryStream struct{ text string }

func (s *summaryStream) Events() <-chan corellm.StreamEvent {
	ch := make(chan corellm.StreamEvent)
	close(ch)
	return ch
}
func (s *summaryStream) Cancel() error { return nil }
func (s *summaryStream) Final() (corellm.Response, error) {
	return corellm.Response{
		Content:      []corellm.ContentBlock{{Type: "text", Text: s.text}},
		FinishReason: "stop",
		Usage:        corellm.Usage{InputTokens: 100, OutputTokens: 10},
		Cost:         corellm.Cost{Currency: "USD", Total: summaryCost},
	}, nil
}

// fixedWindow is the capability lookup: every model has the same window.
type fixedWindow int

func (w fixedWindow) MaxContextTokens(compaction.ProviderProfileRef) (int, bool) { return int(w), true }

type budgetStack struct {
	*conversationStack
	msgs     session.Store
	summary  *summaryRegistry
	compact  *chat.ScheduledBudgetCompactor
	contains *ScheduledRunContainmentRegistry
	base     time.Time
	fires    int
}

func buildBudgetStack(t *testing.T) *budgetStack {
	t.Helper()
	cs := buildConversationStack(t)
	b := &budgetStack{
		conversationStack: cs,
		msgs:              cs.core.SessionManager().Store(),
		summary:           &summaryRegistry{},
		contains:          NewScheduledRunContainmentRegistry(nil),
		base:              time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC),
	}
	engine, err := compaction.NewSessionEngine(compaction.SessionEngineConfig{
		Store:        compactionwiring.NewMessageStore(b.msgs),
		LLM:          compactionwiring.NewLLMCaller(b.summary),
		Capabilities: fixedWindow(200_000),
	})
	if err != nil {
		t.Fatalf("NewSessionEngine: %v", err)
	}
	b.compact = chat.NewScheduledBudgetCompactor(&chat.CompactionDeps{
		Engine:           engine,
		MaxContextTokens: func(compaction.ProviderProfileRef) (int, bool) { return 200_000, true },
	})
	return b
}

func (b *budgetStack) dispatcher(llm llmview.LLMConnectorAPI) *LiveChatRunDispatcher {
	d := b.conversationStack.dispatcher(10 * time.Second)
	d.deps.Compactor = scheduledCompactor(b.compact)
	d.deps.Containment = b.contains
	if llm != nil {
		d.deps.LLM = llm
	}
	return d
}

// seedBudgetRow creates a schedule whose rendered prompt is about 600
// tokens and unique per fire ({{date}} {{time}}), with the given policy.
func seedBudgetRow(t *testing.T, store scheduler.ScheduledChatStore, id string, budget, keep int, focus string) {
	t.Helper()
	now := time.Now().UTC()
	k := keep
	if err := store.Create(context.Background(), scheduler.ChatRunRecord{
		ID: id, Name: "budget", Cron: "*/30 * * * *", OutputSink: "none", Enabled: true,
		PromptTemplate:      "RUN {{date}} {{time}} status check. " + strings.Repeat("lorem ipsum dolor ", 130),
		ContextBudgetTokens: budget, KeepRecentRuns: &k, MemoryFocus: focus,
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("seed row: %v", err)
	}
}

// fire dispatches one run at the next minute and persists its history
// row the way the cron engine does, returning the row.
func (b *budgetStack) fire(t *testing.T, d *LiveChatRunDispatcher, id string, job scheduler.Job) scheduler.ChatRunHistoryRecord {
	t.Helper()
	b.fires++
	b.queueCosts(0.1)
	r, err := d.DispatchChatRun(context.Background(), job, b.base.Add(time.Duration(b.fires)*time.Minute))
	if err != nil {
		t.Fatalf("fire %d: %v", b.fires, err)
	}
	r.ID = fmt.Sprintf("h-%03d", b.fires)
	r.ChatRunID = id
	if err := b.conversationStack.store.AppendHistory(context.Background(), r); err != nil {
		t.Fatalf("AppendHistory: %v", err)
	}
	if r.Status != "completed" {
		t.Fatalf("fire %d = %+v, want completed", b.fires, r)
	}
	b.waitIdle(t, r.SessionID)
	return r
}

// promptOf renders fire n's prompt marker ("RUN <date> <time>").
func (b *budgetStack) promptOf(n int) string {
	at := b.base.Add(time.Duration(n) * time.Minute)
	return "RUN " + at.Format("2006-01-02") + " " + at.Format("15:04") + " "
}

// active returns the conversation's live (non-archived) rows.
func (b *budgetStack) active(t *testing.T, sessionID string) []session.Message {
	t.Helper()
	msgs, err := b.msgs.ListMessagesActive(context.Background(), sessionID)
	if err != nil {
		t.Fatalf("ListMessagesActive: %v", err)
	}
	return msgs
}

// markerLen is the length of a prompt marker ("RUN 2026-10-20 09:01 ").
const markerLen = len("RUN 2026-10-20 09:00 ")

func marker(content string) string {
	if len(content) > markerLen {
		return content[:markerLen]
	}
	return content
}

func activeUserPrompts(msgs []session.Message) []string {
	var out []string
	for _, m := range msgs {
		if m.Role == session.RoleUser {
			out = append(out, marker(m.Content))
		}
	}
	return out
}

// Budget exceeded: compaction runs before the fire, the last N runs stay
// verbatim (with an interactive turn inside them), older runs are
// replaced by ONE summary row (originals soft-archived into it), and on
// the next overflow the summariser is given the previous summary plus
// ONLY the runs that aged out since. The memory focus reaches the
// summariser and the summarisation cost lands on the fire that paid it.
//
// Fail-on-revert:
//   - drop the dispatcher's budget pass -> no summary, every run active.
//   - pass RunStartIDs nil (count user rows) -> the interactive turn
//     counts as a run and run 6 is folded: the kept-prompts check fails.
//   - drop budgetAccount.apply -> CompactionCostUSD nil, cost 0.1.
//   - fold without the previous summary -> pile check #2 fails.
func TestScheduledBudget_CompactsOlderRunsKeepsRecentAndUpdatesSummaryIncrementally(t *testing.T) {
	b := buildBudgetStack(t)
	seedBudgetRow(t, b.conversationStack.store, "cr-budget", 4000, 2, "Track FOCUS-MARKER values across runs.")
	d := b.dispatcher(nil)
	job := chatRunJob("cr-budget")

	// Fires 1-6 stay under 4000 tokens (~610 each): no summariser call.
	var hist []scheduler.ChatRunHistoryRecord
	for i := 0; i < 6; i++ {
		hist = append(hist, b.fire(t, d, "cr-budget", job))
	}
	if n := len(b.summary.snapshot()); n != 0 {
		t.Fatalf("summariser called %d time(s) under budget, want 0", n)
	}
	for _, h := range hist {
		if h.CompactionCostUSD != nil || h.PromptMessageID == "" {
			t.Fatalf("under-budget fire = %+v, want no compaction cost and a prompt boundary", h)
		}
	}
	sid := hist[0].SessionID

	// The user chats in the conversation after run 6: an interactive turn
	// inside the newest kept run.
	if _, err := b.sessions.AppendMessage(context.Background(), sid, "user", "INTERACTIVE question"); err != nil {
		t.Fatalf("append interactive: %v", err)
	}
	if _, err := b.sessions.AppendMessage(context.Background(), sid, "assistant", "interactive answer"); err != nil {
		t.Fatalf("append interactive reply: %v", err)
	}
	// Price the interactive reply now (0), so the costing stand-in does
	// not hand run 7's turn cost to it.
	b.queueCosts(0)
	if _, err := (costingSessions{SessionsAPI: b.sessions, s: b.conversationStack}).ListMessages(context.Background(), sid); err != nil {
		t.Fatalf("price interactive reply: %v", err)
	}

	// Fire 7 is over budget: runs 1-4 are folded, runs 5-6 (+ the
	// interactive turn) stay, then run 7 answers.
	h7 := b.fire(t, d, "cr-budget", job)
	prompts := b.summary.snapshot()
	if len(prompts) != 1 {
		t.Fatalf("summariser called %d time(s) on the overflowing fire, want 1", len(prompts))
	}
	first := prompts[0]
	for n := 1; n <= 4; n++ {
		if !strings.Contains(first, b.promptOf(n)) {
			t.Errorf("first summary input is missing run %d", n)
		}
	}
	for _, n := range []int{5, 6, 7} {
		if strings.Contains(first, b.promptOf(n)) {
			t.Errorf("first summary input holds kept run %d", n)
		}
	}
	if strings.Contains(first, "INTERACTIVE") || strings.Contains(first, "[Rolling summary:") {
		t.Error("first summary input holds the kept interactive turn or a previous summary")
	}
	if !strings.Contains(first, "FOCUS-MARKER") {
		t.Error("the schedule's memory focus did not reach the summariser")
	}

	act := b.active(t, sid)
	if act[0].Role != session.RoleSystem || act[0].Content != "[Rolling summary: SUMMARY-1]" {
		t.Fatalf("head of the conversation = %s %q, want the rolling summary row", act[0].Role, act[0].Content)
	}
	got := strings.Join(activeUserPrompts(act[1:]), "|")
	want := strings.Join([]string{b.promptOf(5), b.promptOf(6), "INTERACTIVE question", b.promptOf(7)}, "|")
	if got != want {
		t.Fatalf("live user rows after compaction =\n%s\nwant\n%s", got, want)
	}
	// The originals are soft-archived into the summary, not deleted (the
	// CompactionArchiveDays sweep purges them later).
	all, err := b.msgs.ListMessages(context.Background(), sid)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	archived := 0
	for _, m := range all {
		if m.ArchivedAt != nil {
			archived++
			if m.CompactedIntoID == nil || *m.CompactedIntoID != act[0].ID {
				t.Fatalf("archived row %s compacted into %v, want the summary %q", m.ID, m.CompactedIntoID, act[0].ID)
			}
		}
	}
	if archived != 8 {
		t.Fatalf("%d rows archived, want 8 (runs 1-4: prompt + reply)", archived)
	}
	// Cost: the fire that compacted carries the summariser's cost, on its
	// own line and in its total.
	if h7.CompactionCostUSD == nil || *h7.CompactionCostUSD != summaryCost {
		t.Fatalf("compacting fire's compaction cost = %v, want %v", h7.CompactionCostUSD, summaryCost)
	}
	if want := 0.1 + summaryCost; h7.CostUSD < want-1e-9 || h7.CostUSD > want+1e-9 {
		t.Fatalf("compacting fire's cost = %v, want %v (turn + summary)", h7.CostUSD, want)
	}
	persisted, _ := b.conversationStack.store.History(context.Background(), "cr-budget", 1)
	if persisted[0].CompactionCostUSD == nil || *persisted[0].CompactionCostUSD != summaryCost {
		t.Fatalf("persisted compaction cost = %v, want %v", persisted[0].CompactionCostUSD, summaryCost)
	}

	// Fire until the conversation overflows again; that pass must be
	// incremental: previous summary + only the runs aged out since.
	for i := 0; i < 10 && len(b.summary.snapshot()) < 2; i++ {
		b.fire(t, d, "cr-budget", job)
	}
	prompts = b.summary.snapshot()
	if len(prompts) != 2 {
		t.Fatalf("no second compaction after %d fires", b.fires)
	}
	second := prompts[1]
	if !strings.Contains(second, "[Rolling summary: SUMMARY-1]") {
		t.Fatal("second summary input does not start from the previous summary")
	}
	for n := 1; n <= 4; n++ {
		if strings.Contains(second, b.promptOf(n)) {
			t.Errorf("second summary input re-summarises run %d, already in the previous summary", n)
		}
	}
	// What aged out since: everything from run 5 up to (not including)
	// the two runs now kept before the overflowing fire.
	last := b.fires
	for n := 5; n <= last-3; n++ {
		if !strings.Contains(second, b.promptOf(n)) {
			t.Errorf("second summary input is missing newly aged run %d", n)
		}
	}
	for n := last - 2; n <= last; n++ {
		if strings.Contains(second, b.promptOf(n)) {
			t.Errorf("second summary input holds kept run %d", n)
		}
	}
	act = b.active(t, sid)
	if act[0].Content != "[Rolling summary: SUMMARY-2]" {
		t.Fatalf("head after the second compaction = %q, want SUMMARY-2", act[0].Content)
	}
	for _, m := range act[1:] {
		if strings.HasPrefix(m.Content, "[Rolling summary:") {
			t.Fatal("more than one live rolling summary")
		}
	}
	for i, n := range b.summary.toolCounts {
		if n != 0 {
			t.Fatalf("summariser call %d carried %d tools, want none", i, n)
		}
	}
}

// Under budget: no summariser call, no compaction cost, every run live.
func TestScheduledBudget_UnderBudgetNeverCompacts(t *testing.T) {
	b := buildBudgetStack(t)
	seedBudgetRow(t, b.conversationStack.store, "cr-roomy", 100_000, 0, "")
	d := b.dispatcher(nil)
	var last scheduler.ChatRunHistoryRecord
	for i := 0; i < 8; i++ {
		last = b.fire(t, d, "cr-roomy", chatRunJob("cr-roomy"))
		if last.CompactionCostUSD != nil {
			t.Fatalf("fire %d recorded a compaction cost under budget", i+1)
		}
	}
	if n := len(b.summary.snapshot()); n != 0 {
		t.Fatalf("summariser called %d time(s) under budget, want 0", n)
	}
	if n := len(activeUserPrompts(b.active(t, last.SessionID))); n != 8 {
		t.Fatalf("%d live prompts, want all 8", n)
	}
}

// Containment still binds exactly the scheduled turn: the summariser
// runs before the fire's containment is armed (and with no tools), the
// turn runs contained, and the fire releases it.
func TestScheduledBudget_ContainmentBindsOnlyTheScheduledTurn(t *testing.T) {
	b := buildBudgetStack(t)
	now := time.Now().UTC()
	k := 1
	if err := b.conversationStack.store.Create(context.Background(), scheduler.ChatRunRecord{
		ID: "cr-cont", Name: "contained", Cron: "*/30 * * * *", OutputSink: "none", Enabled: true,
		PromptTemplate:      "RUN {{date}} {{time}} " + strings.Repeat("lorem ipsum dolor ", 130),
		CreatedBy:           scheduler.ScheduledRunCreatedByModel,
		ToolAllowlist:       []string{"kenaz__alpha"},
		ContextBudgetTokens: 4000, KeepRecentRuns: &k,
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	job := scheduler.Job{ID: "cr-cont", Kind: scheduler.JobKindChatRun, ChatRun: &scheduler.ChatRunSpec{
		ID: "cr-cont", CreatedBy: scheduler.ScheduledRunCreatedByModel, ToolAllowlist: []string{"kenaz__alpha"},
	}}
	d := b.dispatcher(nil)
	var sid string
	b.summary.contained = func() bool { return sid != "" && b.contains.Contained(sid) }
	for i := 0; i < 8 && len(b.summary.snapshot()) == 0; i++ {
		r := b.fire(t, d, "cr-cont", job)
		sid = r.SessionID
		if b.contains.Contained(sid) {
			t.Fatalf("fire %d left the conversation contained after its turn ended", i+1)
		}
	}
	if len(b.summary.snapshot()) == 0 {
		t.Fatal("no compaction happened")
	}
	for i, c := range b.summary.containedAt {
		if c {
			t.Fatalf("summariser call %d ran while the conversation was contained", i)
		}
	}
	if b.summary.toolCounts[0] != 0 {
		t.Fatal("summariser call carried tools")
	}
}

// sessionFullOnceLLM ends the first targeted stream "session full" (as
// the chat runner reports a conversation over the model's window) and
// delegates every later one to the real view.
type sessionFullOnceLLM struct {
	llmview.LLMConnectorAPI
	sessions sessionsview.SessionsAPI
	bus      *EventBus
	mu    sync.Mutex
	calls int
	full  int // how many leading calls end session-full
	// toolRow: before ending session-full, the turn writes a tool row
	// (it ran a tool). delivered: the close reports output delivered.
	toolRow   bool
	delivered bool
}

func (l *sessionFullOnceLLM) StartStreamForMessage(ctx context.Context, profileID, sessionID, model, messageID string) (string, error) {
	l.mu.Lock()
	l.calls++
	n := l.calls
	l.mu.Unlock()
	if n <= l.full {
		sub := fmt.Sprintf("full-sub-%d", n)
		if l.toolRow {
			if _, err := l.sessions.AppendMessage(ctx, sessionID, "tool", "TOOL RESULT: mail sent"); err != nil {
				return "", err
			}
		}
		go l.bus.Publish("llm:stream-closed", chat.StreamClosedPayload{
			SubID: sub, SessionID: sessionID, Reason: "backend-error", Delivered: l.delivered,
			Message: compaction.ErrSessionFull.Error(), ErrorKind: chat.StreamClosedErrorKindSessionFull, TurnSpanID: messageID,
		})
		return sub, nil
	}
	return l.LLMConnectorAPI.(llmview.MessageTargetedStreamer).StartStreamForMessage(ctx, profileID, sessionID, model, messageID)
}

func (l *sessionFullOnceLLM) count() int { l.mu.Lock(); defer l.mu.Unlock(); return l.calls }

// Session-full safety net: a fire whose turn still ends "session full"
// compacts (forced, keeping its own prompt) and retries ONCE; the retry
// answers, and the forced summary's cost is on the row.
func TestScheduledBudget_SessionFullCompactsAndRetriesOnce(t *testing.T) {
	b := buildBudgetStack(t)
	seedBudgetRow(t, b.conversationStack.store, "cr-full", 100_000, 1, "")
	plain := b.dispatcher(nil)
	for i := 0; i < 3; i++ {
		b.fire(t, plain, "cr-full", chatRunJob("cr-full"))
	}
	llm := &sessionFullOnceLLM{LLMConnectorAPI: b.llm, sessions: b.sessions, bus: b.bus, full: 1}
	d := b.dispatcher(llm)
	r := b.fire(t, d, "cr-full", chatRunJob("cr-full"))
	if llm.count() != 2 {
		t.Fatalf("stream starts = %d, want 2 (the session-full one and one retry)", llm.count())
	}
	if len(b.summary.snapshot()) != 1 || r.CompactionCostUSD == nil || *r.CompactionCostUSD != summaryCost {
		t.Fatalf("retry fire = %+v with %d summariser call(s), want one forced compaction on the row", r, len(b.summary.snapshot()))
	}
	got := activeUserPrompts(b.active(t, r.SessionID))
	if strings.Join(got, "|") != b.promptOf(3)+"|"+b.promptOf(4) {
		t.Fatalf("live prompts after the forced compaction = %v, want run 3 (kept) and this fire's run 4", got)
	}
}

// A second session-full is recorded, not looped on: the retry ends
// session-full too, and the row says why.
func TestScheduledBudget_SecondSessionFullIsRecordedNotLooped(t *testing.T) {
	b := buildBudgetStack(t)
	seedBudgetRow(t, b.conversationStack.store, "cr-full2", 100_000, 0, "")
	plain := b.dispatcher(nil)
	for i := 0; i < 2; i++ {
		b.fire(t, plain, "cr-full2", chatRunJob("cr-full2"))
	}
	llm := &sessionFullOnceLLM{LLMConnectorAPI: b.llm, sessions: b.sessions, bus: b.bus, full: 5}
	d := b.dispatcher(llm)
	b.fires++
	r, err := d.DispatchChatRun(context.Background(), chatRunJob("cr-full2"), b.base.Add(time.Duration(b.fires)*time.Minute))
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if r.Status != "failed" || r.Error != failReasonSessionFull {
		t.Fatalf("fire = %+v, want failed with the session-full reason", r)
	}
	if llm.count() != 2 {
		t.Fatalf("stream starts = %d, want 2 (no second retry)", llm.count())
	}
	if r.CompactionCostUSD == nil {
		t.Fatal("the forced compaction's cost was dropped from the failed row")
	}
}

// Session-full with nothing older to summarise: no retry is started, and
// the row does not claim one was.
func TestScheduledBudget_SessionFullWithNothingToSummariseSaysSo(t *testing.T) {
	b := buildBudgetStack(t)
	seedBudgetRow(t, b.conversationStack.store, "cr-full3", 100_000, 2, "")
	b.fire(t, b.dispatcher(nil), "cr-full3", chatRunJob("cr-full3"))
	llm := &sessionFullOnceLLM{LLMConnectorAPI: b.llm, sessions: b.sessions, bus: b.bus, full: 5}
	b.fires++
	r, err := b.dispatcher(llm).DispatchChatRun(context.Background(), chatRunJob("cr-full3"), b.base.Add(time.Duration(b.fires)*time.Minute))
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if r.Status != "failed" || r.Error != failReasonSessionFullNoRetry || llm.count() != 1 {
		t.Fatalf("fire = %+v after %d stream start(s), want failed with the no-retry reason after 1", r, llm.count())
	}
	if n := len(b.summary.snapshot()); n != 0 {
		t.Fatalf("summariser called %d time(s) with nothing older than the kept runs, want 0", n)
	}
}


// startUserTurn starts an interactive turn in sessionID through the real
// chat view and holds it running (the model waits on the returned gate)
// until the caller closes the gate.
func (b *budgetStack) startUserTurn(t *testing.T, sessionID string) chan struct{} {
	t.Helper()
	gate := make(chan struct{})
	b.model.setGate(gate)
	msg, err := b.sessions.AppendMessage(context.Background(), sessionID, "user", "INTERACTIVE while summarising")
	if err != nil {
		t.Fatalf("append user turn: %v", err)
	}
	if _, err := b.llm.(llmview.MessageTargetedStreamer).StartStreamForMessage(context.Background(), "test-profile", sessionID, "", msg.ID); err != nil {
		t.Fatalf("start user turn: %v", err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for !b.runner.HasActiveRun(sessionID) {
		if time.Now().After(deadline) {
			t.Fatal("user turn never became active")
		}
		time.Sleep(5 * time.Millisecond)
	}
	return gate
}

func (b *budgetStack) releaseUserTurn(t *testing.T, sessionID string, gate chan struct{}) {
	t.Helper()
	b.model.setGate(nil)
	close(gate)
	b.waitIdle(t, sessionID)
}

// A turn the user starts while the pre-fire pass is summarising: the fire
// is skipped (not run beside it) and the summary's cost stays on the row.
//
// Fail-on-revert: drop the post-pass busy re-check -> the fire appends its
// prompt and the record is not skipped.
func TestScheduledBudget_TurnStartedDuringCompactionSkipsFire(t *testing.T) {
	b := buildBudgetStack(t)
	seedBudgetRow(t, b.conversationStack.store, "cr-busy-c", 4000, 2, "")
	d := b.dispatcher(nil)
	var sid string
	for i := 0; i < 6; i++ {
		sid = b.fire(t, d, "cr-busy-c", chatRunJob("cr-busy-c")).SessionID
	}
	var gate chan struct{}
	b.summary.onCall = func(int) { gate = b.startUserTurn(t, sid) }
	b.fires++
	r, err := d.DispatchChatRun(context.Background(), chatRunJob("cr-busy-c"), b.base.Add(time.Duration(b.fires)*time.Minute))
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if gate == nil {
		t.Fatal("the fire never reached the summariser")
	}
	defer b.releaseUserTurn(t, sid, gate)
	if r.Status != scheduler.ChatRunStatusSkipped || r.Error != skipReasonBusyAfterCompaction {
		t.Fatalf("fire = %+v, want skipped (%q)", r, skipReasonBusyAfterCompaction)
	}
	if r.CompactionCostUSD == nil || *r.CompactionCostUSD != summaryCost {
		t.Fatalf("skipped fire's compaction cost = %v, want %v", r.CompactionCostUSD, summaryCost)
	}
	for _, p := range activeUserPrompts(b.active(t, sid)) {
		if p == b.promptOf(b.fires) {
			t.Fatal("the skipped fire appended its prompt")
		}
	}
}

// The user sends a message after seeing the session-full failure, while
// the forced retry pass is summarising: the retry must not start a second
// turn beside theirs, nor re-arm the schedule's containment over it.
//
// Fail-on-revert: drop the busy check in retryAfterSessionFull -> a second
// stream starts and the conversation is contained during the user's turn.
func TestScheduledBudget_SessionFullRetryYieldsToUserTurn(t *testing.T) {
	b := buildBudgetStack(t)
	now := time.Now().UTC()
	k := 1
	if err := b.conversationStack.store.Create(context.Background(), scheduler.ChatRunRecord{
		ID: "cr-yield", Name: "contained", Cron: "*/30 * * * *", OutputSink: "none", Enabled: true,
		PromptTemplate:      "RUN {{date}} {{time}} " + strings.Repeat("lorem ipsum dolor ", 130),
		CreatedBy:           scheduler.ScheduledRunCreatedByModel,
		ToolAllowlist:       []string{"kenaz__alpha"},
		ContextBudgetTokens: 100_000, KeepRecentRuns: &k,
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	job := scheduler.Job{ID: "cr-yield", Kind: scheduler.JobKindChatRun, ChatRun: &scheduler.ChatRunSpec{
		ID: "cr-yield", CreatedBy: scheduler.ScheduledRunCreatedByModel, ToolAllowlist: []string{"kenaz__alpha"},
	}}
	var sid string
	plain := b.dispatcher(nil)
	for i := 0; i < 3; i++ {
		sid = b.fire(t, plain, "cr-yield", job).SessionID
	}
	llm := &sessionFullOnceLLM{LLMConnectorAPI: b.llm, sessions: b.sessions, bus: b.bus, full: 1}
	var gate chan struct{}
	b.summary.onCall = func(int) { gate = b.startUserTurn(t, sid) }
	b.fires++
	r, err := b.dispatcher(llm).DispatchChatRun(context.Background(), job, b.base.Add(time.Duration(b.fires)*time.Minute))
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if gate == nil {
		t.Fatal("the forced pass never reached the summariser")
	}
	contained := b.contains.Contained(sid)
	b.releaseUserTurn(t, sid, gate)
	if r.Status != "failed" || r.Error != failReasonSessionFullBusy {
		t.Fatalf("fire = %+v, want failed with the busy reason", r)
	}
	if llm.count() != 1 {
		t.Fatalf("scheduled stream starts = %d, want 1 (no retry beside the user's turn)", llm.count())
	}
	if contained {
		t.Fatal("the schedule's containment was re-armed over the user's interactive turn")
	}
}

// A turn that already ran tools (or delivered output) before it ran out of
// room is not re-run: re-running could repeat what the tools did.
//
// Fail-on-revert: drop the Delivered / turnRanTools gate -> the turn is
// re-run (two stream starts) and completes.
func TestScheduledBudget_SessionFullAfterToolsIsNotRetried(t *testing.T) {
	for _, tc := range []struct {
		name      string
		toolRow   bool
		delivered bool
	}{
		{"tool ran", true, false},
		{"output delivered", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := buildBudgetStack(t)
			seedBudgetRow(t, b.conversationStack.store, "cr-tools", 100_000, 1, "")
			plain := b.dispatcher(nil)
			for i := 0; i < 3; i++ {
				b.fire(t, plain, "cr-tools", chatRunJob("cr-tools"))
			}
			llm := &sessionFullOnceLLM{LLMConnectorAPI: b.llm, sessions: b.sessions, bus: b.bus, full: 1, toolRow: tc.toolRow, delivered: tc.delivered}
			b.fires++
			r, err := b.dispatcher(llm).DispatchChatRun(context.Background(), chatRunJob("cr-tools"), b.base.Add(time.Duration(b.fires)*time.Minute))
			if err != nil {
				t.Fatalf("dispatch: %v", err)
			}
			if r.Status != "failed" || r.Error != failReasonSessionFullRanTools {
				t.Fatalf("fire = %+v, want failed, not retried", r)
			}
			if llm.count() != 1 || len(b.summary.snapshot()) != 0 {
				t.Fatalf("stream starts = %d, summariser calls = %d, want 1 and 0", llm.count(), len(b.summary.snapshot()))
			}
		})
	}
}
