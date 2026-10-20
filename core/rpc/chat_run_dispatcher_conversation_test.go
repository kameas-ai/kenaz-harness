package rpc

// One conversation per schedule (migration sessions/0348).
//
// Owner report: a */30 schedule produced ~350 "Scheduled: …" chats,
// because every fire created a new session. A schedule now owns ONE
// conversation; every fire after the first appends its prompt to it as a
// new user turn.
//
// Real sqlite throughout (core.New over a temp DataDir): the schedule
// row's stored conversation, the session and its messages, the per-row
// cost the usage pipeline writes (usage.New over the same DB — see
// costingSessions) and the history rows the
// scheduledchat API persists all go through the production SQL paths
// (CLAUDE.md blind spot #2). The chat runner is the production one over
// chat_default.yaml; only the model is scripted.

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core"
	coreag "github.com/kameas-ai/kenaz-harness/core/agentgraph"
	corellm "github.com/kameas-ai/kenaz-harness/core/llm"
	"github.com/kameas-ai/kenaz-harness/core/rpc/views/agentgraph/chat"
	llmview "github.com/kameas-ai/kenaz-harness/core/rpc/views/llm"
	scheduledchatview "github.com/kameas-ai/kenaz-harness/core/rpc/views/scheduledchat"
	sessionsview "github.com/kameas-ai/kenaz-harness/core/rpc/views/sessions"
	"github.com/kameas-ai/kenaz-harness/core/scheduler"
	"github.com/kameas-ai/kenaz-harness/core/usage"
)

// steppedModel answers "reply N" on its Nth call. With a gate set, every
// call waits for one token on it (or ctx) first; fail makes calls error.
// Race-safe: driven from the kernel goroutine, read by the test.
type steppedModel struct {
	mu    sync.Mutex
	calls int
	gate  chan struct{}
	fail  error
}

func (m *steppedModel) Generate(ctx context.Context, _ coreag.LLMRequest) (coreag.LLMResponse, error) {
	m.mu.Lock()
	m.calls++
	n, gate, fail := m.calls, m.gate, m.fail
	m.mu.Unlock()
	if gate != nil {
		select {
		case <-gate:
		case <-ctx.Done():
			return coreag.LLMResponse{}, ctx.Err()
		}
	}
	if fail != nil {
		return coreag.LLMResponse{}, fail
	}
	text := fmt.Sprintf("reply %d", n)
	if sink, ok := coreag.StreamSinkFromContext(ctx); ok && sink != nil {
		sink.Emit(coreag.StreamEvent{Kind: coreag.StreamEventText, Text: text})
	}
	return coreag.LLMResponse{Content: text, FinishReason: "stop"}, nil
}

func (m *steppedModel) setGate(g chan struct{}) { m.mu.Lock(); m.gate = g; m.mu.Unlock() }
func (m *steppedModel) setFail(err error)       { m.mu.Lock(); m.fail = err; m.mu.Unlock() }
func (m *steppedModel) callCount() int          { m.mu.Lock(); defer m.mu.Unlock(); return m.calls }

func (m *steppedModel) waitCalls(t *testing.T, n int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for m.callCount() < n {
		if time.Now().After(deadline) {
			t.Fatalf("model reached %d call(s), want %d", m.callCount(), n)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

type conversationStack struct {
	sessions sessionsview.SessionsAPI
	llm      llmview.LLMConnectorAPI
	runner   *chat.ChatRunner
	bus      *EventBus
	store    scheduler.ScheduledChatStore
	model    *steppedModel
	origins  *ScheduledRunOriginRegistry

	usage  usage.Manager
	costMu sync.Mutex
	costs  []float64 // cost recorded on each successive uncosted assistant row
}

// costingSessions is the dispatcher's SessionsAPI in this file: before a
// ListMessages read it records the next queued cost on every assistant
// row that has none yet, through the production usage writer
// (usage.Manager.Add -> session_messages.cost_usd) — standing in for the
// chat runner's UsageHook, which the bare test runner cannot drive (its
// post-LLM hook needs the production memory seam). The dispatcher then
// reads cost back from real sqlite rows exactly as in production.
type costingSessions struct {
	sessionsview.SessionsAPI
	s *conversationStack
}

func (c costingSessions) ListMessages(ctx context.Context, id string) ([]sessionsview.Message, error) {
	msgs, err := c.SessionsAPI.ListMessages(ctx, id)
	if err != nil {
		return nil, err
	}
	wrote := false
	for _, m := range msgs {
		if m.Role != "assistant" || m.CostUSD != nil {
			continue
		}
		c.s.costMu.Lock()
		if len(c.s.costs) == 0 {
			c.s.costMu.Unlock()
			continue
		}
		cost := c.s.costs[0]
		c.s.costs = c.s.costs[1:]
		c.s.costMu.Unlock()
		if err := c.s.usage.Add(ctx, usage.UsageTurn{SessionID: id, MessageID: m.ID, CostUSD: &cost, CostSource: "provider"}); err != nil {
			return nil, err
		}
		wrote = true
	}
	if wrote {
		return c.SessionsAPI.ListMessages(ctx, id)
	}
	return msgs, nil
}

// buildConversationStack is buildDispatcherTestStack plus the pieces this
// file needs: the runner (for ActiveRuns), an origins registry, and the
// production usage writer (usage.New, see costingSessions), so assistant
// rows carry a real session_messages.cost_usd.
func buildConversationStack(t *testing.T) *conversationStack {
	t.Helper()
	c, err := core.New(core.Options{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("core.New: %v", err)
	}
	historyAdapter := newSessionHistoryReader(c)
	historyWriter := &llmHistoryWriter{inner: historyAdapter}
	bus := NewEventBus()
	broker := chatBrokerAdapter{broker: NewStreamBroker(NewMultiEmitter(&busEmitter{bus: bus}))}
	s := &conversationStack{
		sessions: sessionsview.NewManagerAPI(c.SessionManager()),
		bus:      bus,
		store:    scheduler.NewSQLiteChatStore(c.Storage()),
		model:    &steppedModel{},
		origins:  NewScheduledRunOriginRegistry(),
	}
	s.usage = usage.New(c.Storage())
	graph := loadChatDefaultGraph(t)
	runner, err := chat.New(chat.Config{
		Kernel:        coreag.NewKernel(),
		Registry:      dispatcherTestRegistry{},
		Broker:        broker,
		History:       chatSessionMessageReader{inner: historyAdapter},
		HistoryWriter: historyWriter,
		GraphLoader:   func() (coreag.Graph, error) { return graph, nil },
		MaxTurns:      func() int { return 25 },
		EnvDefaults:   func(env *coreag.Env) { env.LLM = s.model },
	})
	if err != nil {
		t.Fatalf("chat.New: %v", err)
	}
	s.runner = runner
	s.llm = llmview.New(llmview.Config{Registry: dispatcherTestRegistry{}, History: historyWriter, ChatRunner: runner})
	return s
}

func (s *conversationStack) queueCosts(c ...float64) {
	s.costMu.Lock()
	defer s.costMu.Unlock()
	s.costs = append(s.costs, c...)
}

func (s *conversationStack) dispatcher(timeout time.Duration) *LiveChatRunDispatcher {
	return NewChatRunDispatcher(ChatRunDispatcherDeps{
		Store:          s.store,
		Sessions:       costingSessions{SessionsAPI: s.sessions, s: s},
		ActiveRuns:     s.runner,
		PausedTurns:    s.runner,
		LLM:            s.llm,
		Bus:            s.bus,
		Origins:        s.origins,
		DefaultProfile: func() string { return "test-profile" },
		Timeout:        timeout,
	})
}

func (s *conversationStack) api(d scheduler.ChatRunDispatcher) scheduledchatview.ScheduledChatAPI {
	return scheduledchatview.New(scheduledchatview.Config{Store: s.store, Dispatcher: d})
}

func chatRunJob(id string) scheduler.Job {
	return scheduler.Job{ID: id, Kind: scheduler.JobKindChatRun, ChatRun: &scheduler.ChatRunSpec{ID: id}}
}

func (s *conversationStack) storedConversation(t *testing.T, id string) string {
	t.Helper()
	r, err := s.store.Get(context.Background(), id)
	if err != nil {
		t.Fatalf("store.Get(%s): %v", id, err)
	}
	return r.SessionID
}

func (s *conversationStack) roleCount(t *testing.T, sessionID, role string) int {
	t.Helper()
	msgs, err := s.sessions.ListMessages(context.Background(), sessionID)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	n := 0
	for _, m := range msgs {
		if m.Role == role {
			n++
		}
	}
	return n
}

func (s *conversationStack) scheduledSessions(t *testing.T) int {
	t.Helper()
	list, err := s.sessions.List(context.Background())
	if err != nil {
		t.Fatalf("List sessions: %v", err)
	}
	n := 0
	for _, x := range list {
		if strings.HasPrefix(x.Name, "Scheduled: ") {
			n++
		}
	}
	return n
}

// waitIdle waits until no turn is executing in sessionID.
func (s *conversationStack) waitIdle(t *testing.T, sessionID string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for s.runner.HasActiveRun(sessionID) {
		if time.Now().After(deadline) {
			t.Fatalf("session %s still has an active run", sessionID)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// Two fires -> ONE session, two user turns, two history rows that both
// link to it, each with THAT run's cost and reply — not the session's
// cumulative cost, not the previous run's reply.
//
// Mutation evidence: restore `Sessions.Create` on every fire -> two
// sessions and the SessionID equality fails; cost the whole session
// instead of the run's turn (turnCost(msgs)) -> the second row reads
// 0.75, not 0.5.
func TestScheduledConversation_TwoFiresOneSessionPerRunCost(t *testing.T) {
	s := buildConversationStack(t)
	seedChatRunRow(t, s.store, "cr-conv", "status check at {{time}}")
	api := s.api(s.dispatcher(5 * time.Second))
	ctx := context.Background()

	s.queueCosts(0.25)
	first, err := api.RunNow(ctx, "cr-conv")
	if err != nil {
		t.Fatalf("RunNow #1: %v", err)
	}
	s.queueCosts(0.5)
	second, err := api.RunNow(ctx, "cr-conv")
	if err != nil {
		t.Fatalf("RunNow #2: %v", err)
	}
	if first.Status != "completed" || second.Status != "completed" {
		t.Fatalf("statuses = %q / %q (errors %q / %q), want completed twice", first.Status, second.Status, first.Error, second.Error)
	}
	if first.SessionID == "" || first.SessionID != second.SessionID {
		t.Fatalf("session ids = %q / %q, want one shared conversation", first.SessionID, second.SessionID)
	}
	if got := s.storedConversation(t, "cr-conv"); got != first.SessionID {
		t.Fatalf("schedule's stored conversation = %q, want %q", got, first.SessionID)
	}
	if n := s.scheduledSessions(t); n != 1 {
		t.Fatalf("%d \"Scheduled:\" sessions after two fires, want 1", n)
	}
	if u, a := s.roleCount(t, first.SessionID, "user"), s.roleCount(t, first.SessionID, "assistant"); u != 2 || a != 2 {
		t.Fatalf("conversation has %d user / %d assistant rows, want 2 / 2", u, a)
	}

	// Ground truth: the persisted history rows, newest first.
	hist, err := s.store.History(ctx, "cr-conv", 10)
	if err != nil || len(hist) != 2 {
		t.Fatalf("History = %+v (err %v), want 2 rows", hist, err)
	}
	byID := map[string]scheduler.ChatRunHistoryRecord{hist[0].ID: hist[0], hist[1].ID: hist[1]}
	h1, h2 := byID[first.ID], byID[second.ID]
	if h1.SessionID != first.SessionID || h2.SessionID != first.SessionID {
		t.Fatalf("history rows link to %q / %q, want both %q", h1.SessionID, h2.SessionID, first.SessionID)
	}
	if h1.CostUSD != 0.25 || h2.CostUSD != 0.5 {
		t.Fatalf("per-run cost = %v / %v, want 0.25 / 0.5 (not cumulative)", h1.CostUSD, h2.CostUSD)
	}
	if h1.OutputSnippet != "reply 1" || h2.OutputSnippet != "reply 2" {
		t.Fatalf("snippets = %q / %q, want each run's own reply", h1.OutputSnippet, h2.OutputSnippet)
	}
	// The session's own aggregate is the sum — what the record must NOT be.
	// (NewManagerAPI has no usage manager, so sum the persisted rows.)
	msgs, err := s.sessions.ListMessages(ctx, first.SessionID)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	var total float64
	for _, m := range msgs {
		if m.CostUSD != nil {
			total += *m.CostUSD
		}
	}
	if total != 0.75 {
		t.Fatalf("conversation's persisted cost = %v, want 0.75 (both runs)", total)
	}
}

// The user deleted the conversation: the next fire creates a new one and
// stores it, instead of failing or writing into a dangling id.
func TestScheduledConversation_DeletedSessionIsRecreated(t *testing.T) {
	s := buildConversationStack(t)
	seedChatRunRow(t, s.store, "cr-del", "ping")
	d := s.dispatcher(5 * time.Second)
	ctx := context.Background()

	first, err := d.DispatchChatRun(ctx, chatRunJob("cr-del"), time.Now().UTC())
	if err != nil || first.Status != "completed" {
		t.Fatalf("fire #1 = %+v (err %v)", first, err)
	}
	if err := s.sessions.Delete(ctx, first.SessionID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	second, err := d.DispatchChatRun(ctx, chatRunJob("cr-del"), time.Now().UTC())
	if err != nil || second.Status != "completed" {
		t.Fatalf("fire #2 = %+v (err %v)", second, err)
	}
	if second.SessionID == "" || second.SessionID == first.SessionID {
		t.Fatalf("fire #2 session = %q, want a new one (deleted: %q)", second.SessionID, first.SessionID)
	}
	if got := s.storedConversation(t, "cr-del"); got != second.SessionID {
		t.Fatalf("stored conversation = %q, want the recreated %q", got, second.SessionID)
	}
	if n := s.scheduledSessions(t); n != 1 {
		t.Fatalf("%d \"Scheduled:\" sessions, want 1", n)
	}
}

// A fire while the same schedule's previous dispatch is still running is
// skipped and recorded — never a second concurrent turn in the session.
// The running fire's origin is attributed to the schedule for its
// duration and cleared after.
func TestScheduledConversation_OverlappingFireIsSkipped(t *testing.T) {
	s := buildConversationStack(t)
	seedChatRunRow(t, s.store, "cr-overlap", "ping")
	d := s.dispatcher(10 * time.Second)
	api := s.api(d)
	ctx := context.Background()

	gate := make(chan struct{})
	s.model.setGate(gate)
	done := make(chan scheduler.ChatRunHistoryRecord, 1)
	go func() {
		r, _ := d.DispatchChatRun(ctx, chatRunJob("cr-overlap"), time.Now().UTC())
		done <- r
	}()
	s.model.waitCalls(t, 1)
	sid := s.storedConversation(t, "cr-overlap")
	if got, ok := s.origins.Get(sid); !ok || got != "cr-overlap" {
		t.Fatalf("origin of the running conversation = %q/%v, want cr-overlap", got, ok)
	}

	skipped, err := api.RunNow(ctx, "cr-overlap")
	if err != nil {
		t.Fatalf("RunNow during the run: %v", err)
	}
	if skipped.Status != scheduler.ChatRunStatusSkipped || skipped.Error != skipReasonInFlight || skipped.SessionID != sid {
		t.Fatalf("overlapping fire = %+v, want skipped (%q) linked to %s", skipped, skipReasonInFlight, sid)
	}
	if n := s.roleCount(t, sid, "user"); n != 1 {
		t.Fatalf("conversation has %d user rows after the skipped fire, want 1 (no prompt appended)", n)
	}

	close(gate)
	if r := <-done; r.Status != "completed" || r.SessionID != sid {
		t.Fatalf("running fire = %+v, want completed in %s", r, sid)
	}
	if _, ok := s.origins.Get(sid); ok {
		t.Fatal("origin still set after the run returned — a later interactive turn would be attributed to the schedule")
	}
	hist, _ := s.store.History(ctx, "cr-overlap", 10)
	if len(hist) != 1 || hist[0].Status != scheduler.ChatRunStatusSkipped {
		t.Fatalf("persisted history = %+v, want the one skipped row (RunNow persists; direct dispatch does not)", hist)
	}
}

// A turn the user started in the conversation (or a previous run that
// outlived its dispatch timeout) is still executing: the fire is skipped.
func TestScheduledConversation_ActiveTurnInConversationSkipsFire(t *testing.T) {
	s := buildConversationStack(t)
	seedChatRunRow(t, s.store, "cr-busy", "ping")
	d := s.dispatcher(300 * time.Millisecond)
	ctx := context.Background()

	first, err := d.DispatchChatRun(ctx, chatRunJob("cr-busy"), time.Now().UTC())
	if err != nil || first.Status != "completed" {
		t.Fatalf("fire #1 = %+v (err %v)", first, err)
	}
	sid := first.SessionID

	// The user, mid-turn in the schedule's conversation.
	gate := make(chan struct{})
	s.model.setGate(gate)
	if _, err := s.sessions.AppendMessage(ctx, sid, "user", "and what about tomorrow?"); err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}
	if _, err := s.llm.StartStream(ctx, "test-profile", sid, ""); err != nil {
		t.Fatalf("interactive StartStream: %v", err)
	}
	s.model.waitCalls(t, 2)
	skipped, err := d.DispatchChatRun(ctx, chatRunJob("cr-busy"), time.Now().UTC())
	if err != nil {
		t.Fatalf("fire #2: %v", err)
	}
	if skipped.Status != scheduler.ChatRunStatusSkipped || skipped.Error != skipReasonActiveTurn || skipped.SessionID != sid {
		t.Fatalf("fire during the user's turn = %+v, want skipped (%q) in %s", skipped, skipReasonActiveTurn, sid)
	}
	if n := s.roleCount(t, sid, "user"); n != 2 {
		t.Fatalf("%d user rows, want 2 (the skipped fire appended nothing)", n)
	}
	gate <- struct{}{}
	s.waitIdle(t, sid)

	// A previous scheduled run that timed out but is still executing.
	timedOut, err := d.DispatchChatRun(ctx, chatRunJob("cr-busy"), time.Now().UTC())
	if err != nil || timedOut.Status != "failed" || !strings.Contains(timedOut.Error, "timed out") {
		t.Fatalf("fire #3 = %+v (err %v), want a timeout", timedOut, err)
	}
	again, err := d.DispatchChatRun(ctx, chatRunJob("cr-busy"), time.Now().UTC())
	if err != nil || again.Status != scheduler.ChatRunStatusSkipped || again.Error != skipReasonActiveTurn {
		t.Fatalf("fire #4 while #3's turn still runs = %+v (err %v), want skipped", again, err)
	}
	s.model.setGate(nil)
	close(gate)
	s.waitIdle(t, sid)

	last, err := d.DispatchChatRun(ctx, chatRunJob("cr-busy"), time.Now().UTC())
	if err != nil || last.Status != "completed" || last.SessionID != sid {
		t.Fatalf("fire #5 = %+v (err %v), want completed in the same conversation", last, err)
	}
}

// A failed FIRST fire leaves an empty session nobody will open: it is
// discarded and the schedule's link cleared. A failed LATER fire must
// leave the conversation — and every earlier run in it — intact.
func TestScheduledConversation_FailedFireDiscardsOnlyANewEmptySession(t *testing.T) {
	s := buildConversationStack(t)
	seedChatRunRow(t, s.store, "cr-fail2", "ping")
	d := s.dispatcher(5 * time.Second)
	ctx := context.Background()
	reject := &corellm.ErrInvalidRequest{Status: 400, Message: "maximum context length is 131072 tokens"}

	s.model.setFail(reject)
	r1, err := d.DispatchChatRun(ctx, chatRunJob("cr-fail2"), time.Now().UTC())
	if err != nil || r1.Status != "failed" {
		t.Fatalf("fire #1 = %+v (err %v), want failed", r1, err)
	}
	if r1.SessionID != "" || s.storedConversation(t, "cr-fail2") != "" || s.scheduledSessions(t) != 0 {
		t.Fatalf("after a failed first fire: record session %q, stored %q, %d sessions — want the empty session discarded and unlinked",
			r1.SessionID, s.storedConversation(t, "cr-fail2"), s.scheduledSessions(t))
	}

	s.model.setFail(nil)
	r2, err := d.DispatchChatRun(ctx, chatRunJob("cr-fail2"), time.Now().UTC())
	if err != nil || r2.Status != "completed" {
		t.Fatalf("fire #2 = %+v (err %v), want completed", r2, err)
	}

	s.model.setFail(reject)
	r3, err := d.DispatchChatRun(ctx, chatRunJob("cr-fail2"), time.Now().UTC())
	if err != nil || r3.Status != "failed" {
		t.Fatalf("fire #3 = %+v (err %v), want failed", r3, err)
	}
	if r3.SessionID != r2.SessionID {
		t.Fatalf("failed later fire SessionID = %q, want the conversation %q kept and linked", r3.SessionID, r2.SessionID)
	}
	if _, err := s.sessions.Get(ctx, r2.SessionID); err != nil {
		t.Fatalf("conversation deleted by a failed later fire: %v", err)
	}
	if got := s.storedConversation(t, "cr-fail2"); got != r2.SessionID {
		t.Fatalf("stored conversation = %q, want %q", got, r2.SessionID)
	}
	if a := s.roleCount(t, r2.SessionID, "assistant"); a != 1 {
		t.Fatalf("conversation has %d assistant rows, want fire #2's reply intact", a)
	}
	if r3.OutputSnippet != "" {
		t.Fatalf("failed fire #3 snippet = %q, want empty (not fire #2's reply)", r3.OutputSnippet)
	}
}

// runTurnRows keeps exactly the rows of the turn its prompt opened.
func TestRunTurnRows_SelectsOnlyThisRunsTurn(t *testing.T) {
	c := func(v float64) *float64 { return &v }
	msgs := []sessionsview.Message{
		{ID: "u1", Role: "user"},
		{ID: "a1", Role: "assistant", Content: "earlier run", TurnSpanID: "u1", CostUSD: c(1)},
		{ID: "u2", Role: "user"}, // this run's prompt
		{ID: "t2", Role: "tool", TurnSpanID: "u2"},
		{ID: "a2", Role: "assistant", Content: "this run", TurnSpanID: "u2", CostUSD: c(0.2)},
		{ID: "x2", Role: "assistant", Content: "unspanned, same turn", CostUSD: c(0.05)},
		{ID: "u3", Role: "user"}, // a turn the user ran afterwards
		{ID: "a3", Role: "assistant", Content: "user's turn", TurnSpanID: "u3", CostUSD: c(4)},
		{ID: "x3", Role: "assistant", Content: "unspanned, user's turn", CostUSD: c(8)},
	}
	rows := runTurnRows(msgs, "u2")
	var ids []string
	for _, m := range rows {
		ids = append(ids, m.ID)
	}
	if got := strings.Join(ids, ","); got != "t2,a2,x2" {
		t.Fatalf("turn rows = %s, want t2,a2,x2", got)
	}
	if got := turnCost(rows); got != 0.25 {
		t.Fatalf("turnCost = %v, want 0.25", got)
	}
	if got := lastAssistantSnippet(rows); got != "unspanned, same turn" {
		t.Fatalf("snippet = %q", got)
	}
	if runTurnRows(msgs, "missing") != nil || runTurnRows(msgs, "") != nil {
		t.Fatal("an unknown prompt id must select nothing")
	}
}

// Each run's generation is retired only by its own release, and the
// session stays contained while any generation is outstanding — so a
// later fire's release cannot end an earlier turn that can still run
// (an auth-paused one), nor an earlier run's late release a newer one.
// While two are outstanding a tool must be on both allowlists.
func TestScheduledRunContainment_GenerationsAreIndependent(t *testing.T) {
	ctx := context.Background()
	reg := NewScheduledRunContainmentRegistry(nil)
	g1 := reg.Contain("s", "cr", []string{"kenaz__alpha", "kenaz__beta"})
	g2 := reg.Contain("s", "cr", []string{"kenaz__alpha"})
	if g1 == g2 {
		t.Fatal("two Contain calls returned the same generation")
	}
	if _, denied := reg.Check(ctx, "s", "kenaz", "beta"); !denied {
		t.Fatal("beta allowed while a generation that does not list it is outstanding")
	}
	if _, denied := reg.Check(ctx, "s", "kenaz", "alpha"); denied {
		t.Fatal("alpha denied though every outstanding generation lists it")
	}
	if !reg.Release("s", g2) || !reg.Contained("s") {
		t.Fatal("the later run's release ended the earlier run's containment")
	}
	if reg.Release("s", g2) {
		t.Fatal("a retired generation released twice")
	}
	if !reg.Release("s", g1) || reg.Contained("s") {
		t.Fatal("retiring the last generation did not release the session")
	}
}

// interleavingSessions appends a user message to the conversation right
// after the dispatcher appends its prompt — the user typing into the
// shared conversation between the scheduled append and its stream start.
type interleavingSessions struct {
	sessionsview.SessionsAPI
	prompt, inject string
	injected       *sessionsview.Message
}

func (s *interleavingSessions) AppendMessage(ctx context.Context, id, role, content string) (sessionsview.Message, error) {
	m, err := s.SessionsAPI.AppendMessage(ctx, id, role, content)
	if err == nil && role == "user" && content == s.prompt && s.injected == nil {
		u, uerr := s.SessionsAPI.AppendMessage(ctx, id, "user", s.inject)
		if uerr != nil {
			return m, uerr
		}
		s.injected = &u
	}
	return m, err
}

// The scheduled stream answers exactly its own prompt even when a user
// message lands between the prompt's append and the stream start.
// Fail-on-revert: start the stream with plain StartStream (newest user
// row) -> the run answers the user's message, its own prompt has no
// reply and the snippet is empty.
func TestScheduledConversation_StreamAnswersItsOwnPromptDespiteInterleavedUserMessage(t *testing.T) {
	s := buildConversationStack(t)
	seedChatRunRow(t, s.store, "cr-race", "scheduled prompt")
	ctx := context.Background()
	is := &interleavingSessions{SessionsAPI: s.sessions, prompt: "scheduled prompt", inject: "user typed this meanwhile"}
	d := NewChatRunDispatcher(ChatRunDispatcherDeps{
		Store: s.store, Sessions: is, ActiveRuns: s.runner, PausedTurns: s.runner, LLM: s.llm, Bus: s.bus,
		DefaultProfile: func() string { return "test-profile" },
		Timeout:        5 * time.Second,
	})
	rec, err := d.DispatchChatRun(ctx, chatRunJob("cr-race"), time.Now().UTC())
	if err != nil || rec.Status != "completed" {
		t.Fatalf("fire = %+v (err %v), want completed", rec, err)
	}
	if is.injected == nil {
		t.Fatal("the interleaved user message was never appended")
	}
	msgs, err := s.sessions.ListMessages(ctx, rec.SessionID)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	var promptID string
	for _, m := range msgs {
		if m.Role == "user" && m.Content == "scheduled prompt" {
			promptID = m.ID
		}
	}
	answered, answeredInjected := false, false
	for _, m := range msgs {
		if m.Role == "assistant" && m.TurnSpanID == promptID {
			answered = true
		}
		if m.Role == "assistant" && m.TurnSpanID == is.injected.ID {
			answeredInjected = true
		}
	}
	if !answered || answeredInjected {
		t.Fatalf("scheduled prompt answered=%v, user's message answered by the scheduled run=%v; want true/false", answered, answeredInjected)
	}
	if rec.OutputSnippet == "" {
		t.Fatal("the run's record has no reply — it did not answer its own prompt")
	}
}

// StartStreamForMessage refuses a message that is not a user row of the
// session rather than falling back to the newest user row.
func TestStartStreamForMessage_RefusesUnknownTarget(t *testing.T) {
	s := buildConversationStack(t)
	ctx := context.Background()
	sess, err := s.sessions.Create(ctx, "x")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := s.sessions.AppendMessage(ctx, sess.ID, "user", "hello"); err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}
	ts, ok := s.llm.(llmview.MessageTargetedStreamer)
	if !ok {
		t.Fatal("the production LLM view does not implement MessageTargetedStreamer")
	}
	if _, err := ts.StartStreamForMessage(ctx, "test-profile", sess.ID, "", "no-such-message"); err != llmview.ErrTargetMessageNotFound {
		t.Fatalf("StartStreamForMessage(unknown) = %v, want ErrTargetMessageNotFound", err)
	}
	if s.model.callCount() != 0 {
		t.Fatal("a refused targeted start still ran the model")
	}
}
