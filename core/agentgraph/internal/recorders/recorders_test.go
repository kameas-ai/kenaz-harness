package recorders_test

import (
	"context"
	"strings"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/agentgraph"
	"github.com/kameas-ai/kenaz-harness/core/agentgraph/internal/recorders"
	"github.com/kameas-ai/kenaz-harness/core/elicitation"
)

// ── LLMProvider ───────────────────────────────────────────────────────────────

func TestLLMProvider_RecordsCall(t *testing.T) {
	r := &recorders.LLMProvider{Resp: agentgraph.LLMResponse{Content: "hello"}}
	req := agentgraph.LLMRequest{Provider: "test", Model: "m1", SystemPrompt: "be helpful"}

	resp, err := r.Generate(context.Background(), req)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if resp.Content != "hello" {
		t.Errorf("content = %q, want %q", resp.Content, "hello")
	}
	call := r.RequireCalledOnce(t)
	if call.Req.Provider != "test" {
		t.Errorf("Provider = %q, want %q", call.Req.Provider, "test")
	}
}

func TestLLMProvider_RequireProvider(t *testing.T) {
	r := &recorders.LLMProvider{}
	_, _ = r.Generate(context.Background(), agentgraph.LLMRequest{Provider: "anthropic"})
	r.RequireProvider(t, "anthropic")
}

func TestLLMProvider_RequireSystemPrompt(t *testing.T) {
	r := &recorders.LLMProvider{}
	_, _ = r.Generate(context.Background(), agentgraph.LLMRequest{Provider: "test", SystemPrompt: "you are helpful"})
	r.RequireSystemPrompt(t, "you are helpful")
}

func TestLLMProvider_RequireMessageCount(t *testing.T) {
	r := &recorders.LLMProvider{}
	req := agentgraph.LLMRequest{
		Provider: "test",
		Messages: []agentgraph.Message{
			{Role: "user", Content: "hi"},
			{Role: "assistant", Content: "hello"},
			{Role: "user", Content: "how are you?"},
		},
	}
	_, _ = r.Generate(context.Background(), req)
	r.RequireMessageCount(t, 3)
}

func TestLLMProvider_RequireCalledWith(t *testing.T) {
	r := &recorders.LLMProvider{}
	_, _ = r.Generate(context.Background(), agentgraph.LLMRequest{Provider: "openai", Model: "gpt-4o"})
	r.RequireCalledWith(t, func(c recorders.LLMCall) bool {
		return c.Req.Model == "gpt-4o"
	}, "model=gpt-4o")
}

func TestLLMProvider_RespFn(t *testing.T) {
	callCount := 0
	r := &recorders.LLMProvider{
		RespFn: func(req agentgraph.LLMRequest) (agentgraph.LLMResponse, error) {
			callCount++
			return agentgraph.LLMResponse{Content: "call-" + req.Provider}, nil
		},
	}
	resp, _ := r.Generate(context.Background(), agentgraph.LLMRequest{Provider: "x"})
	if resp.Content != "call-x" {
		t.Errorf("content = %q", resp.Content)
	}
	if callCount != 1 {
		t.Errorf("RespFn called %d times, want 1", callCount)
	}
}

// ── StreamSink ────────────────────────────────────────────────────────────────

func TestStreamSink_RecordsEmit(t *testing.T) {
	r := &recorders.StreamSink{}
	r.Emit(agentgraph.StreamEvent{Kind: agentgraph.StreamEventText, Text: "hello"})
	r.Emit(agentgraph.StreamEvent{Kind: agentgraph.StreamEventFinish, Finish: "stop"})
	r.Close()

	if r.EventCount() != 2 {
		t.Errorf("EventCount = %d, want 2", r.EventCount())
	}
	if !r.Closed() {
		t.Error("Closed = false, want true")
	}
	r.RequireTextEvent(t, "hello")
	r.RequireFinishEvent(t)
}

func TestStreamSink_RequireClosed(t *testing.T) {
	r := &recorders.StreamSink{}
	r.Close()
	r.RequireClosed(t)
}

// ── ToolRegistry ──────────────────────────────────────────────────────────────

func TestToolRegistry_RecordsCall(t *testing.T) {
	r := &recorders.ToolRegistry{}
	r.Register("search", agentgraph.ToolResult{Content: "results"}, nil)

	if !r.Has("search") {
		t.Fatal("Has(search) = false, want true")
	}
	if r.Has("nonexistent") {
		t.Fatal("Has(nonexistent) = true, want false")
	}

	res, err := r.Call(context.Background(), agentgraph.ToolCall{Name: "search"})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if res.Content != "results" {
		t.Errorf("content = %q, want %q", res.Content, "results")
	}
	r.RequireCalledWithTool(t, "search")
}

func TestToolRegistry_UnregisteredTool(t *testing.T) {
	r := &recorders.ToolRegistry{}
	_, err := r.Call(context.Background(), agentgraph.ToolCall{Name: "missing"})
	if err == nil {
		t.Fatal("expected error for unregistered tool, got nil")
	}
	if !strings.Contains(err.Error(), "missing") {
		t.Errorf("error = %q, want to contain 'missing'", err.Error())
	}
}

// ── MemoryStore ───────────────────────────────────────────────────────────────

func TestMemoryStore_Write(t *testing.T) {
	r := &recorders.MemoryStore{}
	r.SetNextID("mem-42")

	id, deduped, err := r.Write(context.Background(), agentgraph.MemoryWrite{
		Scope:   "project",
		Content: "some memory",
	})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if id != "mem-42" {
		t.Errorf("id = %q, want %q", id, "mem-42")
	}
	if deduped {
		t.Error("deduped = true, want false")
	}
	r.RequireWriteWithScope(t, "project")
}

func TestMemoryStore_Read(t *testing.T) {
	r := &recorders.MemoryStore{
		ReadResult: []agentgraph.MemoryHit{{ID: "h1", Content: "hit"}},
	}
	hits, err := r.Read(context.Background(), agentgraph.MemoryReadFilter{Query: "search"})
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(hits) != 1 {
		t.Errorf("len(hits) = %d, want 1", len(hits))
	}
	r.RequireReadCalledWith(t, func(c recorders.MemoryReadCall) bool {
		return c.Filter.Query == "search"
	}, "query=search")
}

// ── HistoryReader ─────────────────────────────────────────────────────────────

func TestHistoryReader_RecordsCall(t *testing.T) {
	r := &recorders.HistoryReader{
		Result: []agentgraph.Message{
			{Role: "user", Content: "old message"},
		},
	}
	msgs, err := r.History(context.Background(), "sess-1", 10)
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(msgs) != 1 {
		t.Errorf("len(msgs) = %d, want 1", len(msgs))
	}
	call := r.RequireCalledOnce(t)
	if call.SessionID != "sess-1" {
		t.Errorf("SessionID = %q, want %q", call.SessionID, "sess-1")
	}
	r.RequireCalledWithSession(t, "sess-1")
}

func TestHistoryReader_NotCalledAssertion(t *testing.T) {
	r := &recorders.HistoryReader{}
	r.RequireNotCalled(t)
}

// ── HistoryWriter ─────────────────────────────────────────────────────────────

func TestHistoryWriter_RecordsCall(t *testing.T) {
	r := &recorders.HistoryWriter{NextID: "msg-99"}
	id, err := r.AppendEntry(context.Background(), "sess-1",
		agentgraph.HistoryEntry{Role: "user", Content: "hello"})
	if err != nil {
		t.Fatalf("AppendEntry: %v", err)
	}
	if id != "msg-99" {
		t.Errorf("id = %q, want %q", id, "msg-99")
	}
	r.RequireCalledWithRole(t, "user")
}

// ── AttachmentResolver ────────────────────────────────────────────────────────

func TestAttachmentResolver_RecordsCall(t *testing.T) {
	r := &recorders.AttachmentResolver{}
	r.Register("att-1", agentgraph.AttachmentBlock{MIME: "image/png", Title: "screenshot"})

	block, err := r.Resolve(context.Background(), "att-1")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if block.MIME != "image/png" {
		t.Errorf("MIME = %q, want %q", block.MIME, "image/png")
	}
	r.RequireCalledWith(t, "att-1")
}

func TestAttachmentResolver_UnregisteredID(t *testing.T) {
	r := &recorders.AttachmentResolver{}
	_, err := r.Resolve(context.Background(), "missing")
	if err == nil {
		t.Fatal("expected error for unregistered attachment, got nil")
	}
}

// ── AttachmentRegistrar ───────────────────────────────────────────────────────

func TestAttachmentRegistrar_RecordsCall(t *testing.T) {
	r := &recorders.AttachmentRegistrar{NextID: "att-new"}
	id, err := r.Register(context.Background(), agentgraph.AttachmentBlock{MIME: "text/plain"})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if id != "att-new" {
		t.Errorf("id = %q, want %q", id, "att-new")
	}
	call := r.RequireCalledOnce(t)
	if call.Block.MIME != "text/plain" {
		t.Errorf("MIME = %q, want %q", call.Block.MIME, "text/plain")
	}
}

// ── PolicyGate ────────────────────────────────────────────────────────────────

func TestPolicyGate_AllowAll(t *testing.T) {
	r := &recorders.PolicyGate{}
	if err := r.CheckFileRead(context.Background(), "/foo"); err != nil {
		t.Errorf("CheckFileRead: %v", err)
	}
	if err := r.CheckFileWrite(context.Background(), "/bar"); err != nil {
		t.Errorf("CheckFileWrite: %v", err)
	}
	r.RequireFileReadChecked(t, "/foo")
	r.RequireFileWriteChecked(t, "/bar")
}

func TestPolicyGate_DenyAll(t *testing.T) {
	r := &recorders.PolicyGate{DenyAll: true}
	err := r.CheckFileRead(context.Background(), "/secret")
	if err == nil {
		t.Fatal("expected denial, got nil")
	}
	if !strings.Contains(err.Error(), "denied") {
		t.Errorf("error = %q, want to contain 'denied'", err.Error())
	}
}

func TestPolicyGate_DenyFn(t *testing.T) {
	r := &recorders.PolicyGate{
		DenyFn: func(method, arg string) error {
			if strings.HasPrefix(arg, "/secret/") {
				return &testDenyError{method: method, arg: arg}
			}
			return nil
		},
	}
	if err := r.CheckFileRead(context.Background(), "/safe/file"); err != nil {
		t.Errorf("/safe/file should be allowed, got: %v", err)
	}
	if err := r.CheckFileRead(context.Background(), "/secret/creds"); err == nil {
		t.Error("/secret/creds should be denied, got nil")
	}
}

type testDenyError struct{ method, arg string }

func (e *testDenyError) Error() string { return "denied: " + e.method + " " + e.arg }

// ── TraceSink ─────────────────────────────────────────────────────────────────

func TestTraceSink_RecordsSpan(t *testing.T) {
	r := &recorders.TraceSink{}
	ctx, end := r.Span(context.Background(), "kernel.run", map[string]any{"run_id": "r1"})
	end(nil)
	if ctx == nil {
		t.Fatal("Span returned nil context")
	}
	r.RequireSpanCalled(t, "kernel.run")
}

// ── AskBus ────────────────────────────────────────────────────────────────────

func TestAskBus_RecordsPending(t *testing.T) {
	r := &recorders.AskBus{}
	if err := r.Pending(context.Background(), "run-1", "node-1", elicitation.Question{Text: "What is 2+2?"}); err != nil {
		t.Fatalf("Pending: %v", err)
	}
	r.RequirePendingQuestion(t, "What is 2+2?")
}

func TestAskBus_LookupAnswer(t *testing.T) {
	r := &recorders.AskBus{}
	r.SetAnswer("run-1", "node-1", "4")
	ans, ok := r.LookupAnswer(context.Background(), "run-1", "node-1")
	if !ok {
		t.Fatal("LookupAnswer ok=false, want true")
	}
	if ans.Text != "4" {
		t.Errorf("answer = %q, want %q", ans.Text, "4")
	}
	r.RequireLookupCalled(t)
}

func TestAskBus_NoAnswer(t *testing.T) {
	r := &recorders.AskBus{}
	_, ok := r.LookupAnswer(context.Background(), "run-1", "node-1")
	if ok {
		t.Error("LookupAnswer ok=true for missing answer, want false")
	}
}

// ── Compactor ─────────────────────────────────────────────────────────────────

func TestCompactor_RecordsCall(t *testing.T) {
	r := &recorders.Compactor{}
	in := agentgraph.CompactionInput{
		Site:     agentgraph.CompactionSitePreCall,
		Messages: []agentgraph.Message{{Role: "user", Content: "hi"}},
	}
	out, err := r.Compact(context.Background(), in)
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if len(out.Messages) != 1 {
		t.Errorf("Messages count = %d, want 1", len(out.Messages))
	}
	r.RequireCalledAtSite(t, agentgraph.CompactionSitePreCall)
}

func TestCompactor_TransformFn(t *testing.T) {
	r := &recorders.Compactor{
		TransformFn: func(in agentgraph.CompactionInput) (agentgraph.CompactionOutput, error) {
			// Drop all but the last message.
			msgs := in.Messages
			if len(msgs) > 1 {
				msgs = msgs[len(msgs)-1:]
			}
			return agentgraph.CompactionOutput{Messages: msgs}, nil
		},
	}
	in := agentgraph.CompactionInput{
		Site: agentgraph.CompactionSitePreCall,
		Messages: []agentgraph.Message{
			{Role: "user", Content: "old"},
			{Role: "user", Content: "new"},
		},
	}
	out, err := r.Compact(context.Background(), in)
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if len(out.Messages) != 1 {
		t.Errorf("Messages count = %d, want 1", len(out.Messages))
	}
	if out.Messages[0].Content != "new" {
		t.Errorf("Messages[0].Content = %q, want %q", out.Messages[0].Content, "new")
	}
}

// ── BashOutputStore ───────────────────────────────────────────────────────────

func TestBashOutputStore_RecordsGet(t *testing.T) {
	r := &recorders.BashOutputStore{}
	r.RegisterOutput("run-abc", agentgraph.BashOutput{RunID: "run-abc", Stdout: "hello"})

	out, err := r.Get(context.Background(), "run-abc")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if out.Stdout != "hello" {
		t.Errorf("Stdout = %q, want %q", out.Stdout, "hello")
	}
	r.RequireCalledWithRunID(t, "run-abc")
}

func TestBashOutputStore_UnregisteredRunID(t *testing.T) {
	r := &recorders.BashOutputStore{}
	_, err := r.Get(context.Background(), "missing-run")
	if err == nil {
		t.Fatal("expected error for unregistered run ID, got nil")
	}
}

// ── ActivityCatalog ───────────────────────────────────────────────────────────

func TestActivityCatalog_RecordsResolve(t *testing.T) {
	r := &recorders.ActivityCatalog{}
	g := &agentgraph.Graph{ID: "my-activity"}
	r.RegisterActivity("my-activity", "1.0", g)

	got, err := r.Resolve("my-activity", "1.0")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.ID != "my-activity" {
		t.Errorf("ID = %q, want %q", got.ID, "my-activity")
	}
	r.RequireCalledWith(t, "my-activity")
}

func TestActivityCatalog_UnregisteredActivity(t *testing.T) {
	r := &recorders.ActivityCatalog{}
	_, err := r.Resolve("missing", "1.0")
	if err == nil {
		t.Fatal("expected error for unregistered activity, got nil")
	}
}

// ── CorpusBackend ─────────────────────────────────────────────────────────────

func TestCorpusBackend_Search(t *testing.T) {
	r := &recorders.CorpusBackend{
		SearchResult: []agentgraph.CorpusHit{{CorpusID: "c1", Snippet: "found"}},
	}
	hits, err := r.Search(context.Background(), []string{"c1"}, "query", 5)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) != 1 {
		t.Errorf("len(hits) = %d, want 1", len(hits))
	}
	r.RequireSearchCalledWith(t, func(c recorders.SearchCall) bool {
		return c.Query == "query"
	}, "query=query")
}

func TestCorpusBackend_Enqueue(t *testing.T) {
	r := &recorders.CorpusBackend{NextJobID: "job-123"}
	jobID, err := r.Enqueue(context.Background(), "corpus-1", "/path/to/file.md")
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if jobID != "job-123" {
		t.Errorf("jobID = %q, want %q", jobID, "job-123")
	}
	r.RequireEnqueueCalledWith(t, func(c recorders.EnqueueCall) bool {
		return c.CorpusID == "corpus-1"
	}, "corpusID=corpus-1")
}
