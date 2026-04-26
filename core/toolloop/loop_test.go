package toolloop

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	corellm "github.com/sigil-tech/kaneaz-harness/core/llm"
)

// fakeRegistry returns a queued sequence of streams. Each Stream call
// pops the next entry; running out of streams panics so test failures
// are obvious.
type fakeRegistry struct {
	mu       sync.Mutex
	streams  []corellm.Stream
	requests []corellm.GenerationRequest
}

func (r *fakeRegistry) RegisterAdapter(_ corellm.ProviderAdapter)                 {}
func (r *fakeRegistry) LoadProfiles([]corellm.ProviderProfile) error              { return nil }
func (r *fakeRegistry) Profile(string) (corellm.ProviderProfile, error)           { return corellm.ProviderProfile{}, nil }
func (r *fakeRegistry) PreflightAll(context.Context) []corellm.PreflightResult    { return nil }
func (r *fakeRegistry) Stream(_ context.Context, req corellm.GenerationRequest) (corellm.Stream, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.requests = append(r.requests, req)
	if len(r.streams) == 0 {
		return nil, errors.New("fakeRegistry: no more streams queued")
	}
	s := r.streams[0]
	r.streams = r.streams[1:]
	return s, nil
}

// scriptedStream replays a fixed list of events and a terminal Response.
type scriptedStream struct {
	events []corellm.StreamEvent
	final  corellm.Response
	once   sync.Once
	out    chan corellm.StreamEvent
}

func (s *scriptedStream) Events() <-chan corellm.StreamEvent {
	s.once.Do(func() {
		s.out = make(chan corellm.StreamEvent, len(s.events))
		for _, ev := range s.events {
			s.out <- ev
		}
		close(s.out)
	})
	return s.out
}
func (s *scriptedStream) Cancel() error                  { return nil }
func (s *scriptedStream) Final() (corellm.Response, error) { return s.final, nil }

// fakePool implements MCPPool with deterministic behavior. We don't
// reuse core/mcp/fixture.Pool here because that package satisfies
// mcp.Pool (the wider interface) and the loop only needs the narrower
// MCPPool surface.
type fakePool struct {
	mu       sync.Mutex
	tools    []Tool
	handlers map[string]func(context.Context, json.RawMessage) (json.RawMessage, error)
	calls    []poolCall
}

type poolCall struct {
	server, tool string
	args         json.RawMessage
}

func newFakePool() *fakePool {
	return &fakePool{handlers: map[string]func(context.Context, json.RawMessage) (json.RawMessage, error){}}
}
func (p *fakePool) register(server, tool string, h func(context.Context, json.RawMessage) (json.RawMessage, error)) {
	p.tools = append(p.tools, Tool{Server: server, Name: tool})
	p.handlers[server+"\x00"+tool] = h
}
func (p *fakePool) Tools(_ context.Context) ([]Tool, error) {
	out := make([]Tool, len(p.tools))
	copy(out, p.tools)
	return out, nil
}
func (p *fakePool) Call(ctx context.Context, server, tool string, args json.RawMessage) (json.RawMessage, error) {
	p.mu.Lock()
	p.calls = append(p.calls, poolCall{server: server, tool: tool, args: append(json.RawMessage(nil), args...)})
	h := p.handlers[server+"\x00"+tool]
	p.mu.Unlock()
	if h == nil {
		return nil, errors.New("no handler")
	}
	return h(ctx, args)
}

// recordingHistory captures every AppendMessage call so tests can
// assert the user → assistant → tool → assistant sequence.
type recordingHistory struct {
	mu      sync.Mutex
	entries []historyEntry
}

type historyEntry struct {
	sessionID string
	role      string
	content   string
}

func (h *recordingHistory) AppendMessage(_ context.Context, sessionID, role, content string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.entries = append(h.entries, historyEntry{sessionID: sessionID, role: role, content: content})
	return nil
}

func (h *recordingHistory) snapshot() []historyEntry {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]historyEntry, len(h.entries))
	copy(out, h.entries)
	return out
}

func TestLoop_NewRequiresRegistryAndPool(t *testing.T) {
	if _, err := New(Config{}); err == nil {
		t.Fatal("expected error with empty config")
	}
	if _, err := New(Config{Registry: &fakeRegistry{}}); err == nil {
		t.Fatal("expected error without pool")
	}
	if _, err := New(Config{Pool: newFakePool()}); err == nil {
		t.Fatal("expected error without registry")
	}
	loop, err := New(Config{Registry: &fakeRegistry{}, Pool: newFakePool()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if loop.maxIter != DefaultMaxIter {
		t.Fatalf("maxIter = %d, want %d", loop.maxIter, DefaultMaxIter)
	}
}

func TestLoop_NonToolUseFinishExitsCleanly(t *testing.T) {
	reg := &fakeRegistry{}
	pool := newFakePool()
	hist := &recordingHistory{}
	loop, err := New(Config{Registry: reg, Pool: pool, History: hist})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	resp := &corellm.Response{FinishReason: "end_turn"}
	if err := loop.Run(context.Background(), "sess-1", "sub-1", resp, corellm.GenerationRequest{}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	// No registry calls, no pool calls, no history writes — pure no-op.
	if len(reg.requests) != 0 {
		t.Fatalf("expected zero registry calls, got %d", len(reg.requests))
	}
	if len(pool.calls) != 0 {
		t.Fatalf("expected zero pool calls, got %d", len(pool.calls))
	}
	if len(hist.snapshot()) != 0 {
		t.Fatalf("expected zero history writes, got %d", len(hist.snapshot()))
	}
}

func TestLoop_NilResponseRejected(t *testing.T) {
	loop, _ := New(Config{Registry: &fakeRegistry{}, Pool: newFakePool()})
	if err := loop.Run(context.Background(), "s", "p", nil, corellm.GenerationRequest{}); err == nil {
		t.Fatal("expected error on nil response")
	}
}

func TestLoop_SingleToolHappyPath(t *testing.T) {
	// Pool: github.get_issue returns a static result.
	pool := newFakePool()
	pool.register("github", "get_issue", func(_ context.Context, args json.RawMessage) (json.RawMessage, error) {
		return json.RawMessage(`{"number":42,"title":"hello"}`), nil
	})

	// Registry: the second invocation (after the tool result is
	// threaded back) returns end_turn with a final assistant text.
	reg := &fakeRegistry{
		streams: []corellm.Stream{
			&scriptedStream{
				final: corellm.Response{
					Content:      []corellm.ContentPart{{Type: "text", Text: "Issue 42 says hello."}},
					FinishReason: "end_turn",
				},
			},
		},
	}

	hist := &recordingHistory{}
	loop, err := New(Config{Registry: reg, Pool: pool, History: hist})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// The terminal response from the *first* stream the pump observed:
	// model emitted one tool_use call.
	initial := &corellm.Response{
		FinishReason: "tool_use",
		ToolCalls: []corellm.ToolUse{
			{ID: "tool_1", Name: "get_issue", Input: json.RawMessage(`{"number":42}`)},
		},
	}
	originalReq := corellm.GenerationRequest{
		ProfileID: "prof",
		Messages: []corellm.Message{
			{Role: corellm.RoleUser, Content: []corellm.ContentPart{{Type: "text", Text: "what's in issue 42?"}}},
		},
	}

	if err := loop.Run(context.Background(), "sess-1", "sub-1", initial, originalReq); err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Pool got dispatched once, with the model's args.
	if len(pool.calls) != 1 {
		t.Fatalf("expected 1 pool call, got %d", len(pool.calls))
	}
	if pool.calls[0].server != "github" || pool.calls[0].tool != "get_issue" {
		t.Fatalf("call resolved to wrong (server, tool): %+v", pool.calls[0])
	}
	if string(pool.calls[0].args) != `{"number":42}` {
		t.Fatalf("args mismatch: %s", pool.calls[0].args)
	}

	// Registry was re-invoked exactly once with the augmented messages.
	if len(reg.requests) != 1 {
		t.Fatalf("expected 1 registry call, got %d", len(reg.requests))
	}
	gotMsgs := reg.requests[0].Messages
	// Expected: [user, assistant(tool_use), tool(result)] — the original
	// user turn plus the two synthetic turns the loop appended.
	if len(gotMsgs) != 3 {
		t.Fatalf("augmented messages = %d, want 3 (user, assistant, tool)", len(gotMsgs))
	}
	if gotMsgs[0].Role != corellm.RoleUser {
		t.Fatalf("msg[0].role = %q", gotMsgs[0].Role)
	}
	if gotMsgs[1].Role != corellm.RoleAssistant {
		t.Fatalf("msg[1].role = %q", gotMsgs[1].Role)
	}
	if gotMsgs[2].Role != corellm.RoleTool {
		t.Fatalf("msg[2].role = %q", gotMsgs[2].Role)
	}
	// The tool message must carry the tool_use_id so adapter
	// translation can pair it with the original call.
	toolPart := gotMsgs[2].Content[0]
	if toolPart.Type != "tool_result" {
		t.Fatalf("tool part type = %q", toolPart.Type)
	}
	var payload map[string]any
	if err := json.Unmarshal(toolPart.ToolData, &payload); err != nil {
		t.Fatalf("tool_data unmarshal: %v", err)
	}
	if payload["tool_use_id"] != "tool_1" {
		t.Fatalf("tool_use_id = %v", payload["tool_use_id"])
	}

	// Session history: assistant tool_use turn, tool result, final
	// assistant text — three entries (user turn was already persisted
	// before the loop ran, by the rpc pump).
	entries := hist.snapshot()
	if len(entries) != 3 {
		t.Fatalf("history entries = %d, want 3 (assistant tool_use, tool, assistant final): %+v", len(entries), entries)
	}
	if entries[0].role != "assistant" {
		t.Fatalf("entries[0].role = %q", entries[0].role)
	}
	if entries[1].role != "tool" {
		t.Fatalf("entries[1].role = %q", entries[1].role)
	}
	if entries[2].role != "assistant" {
		t.Fatalf("entries[2].role = %q", entries[2].role)
	}
	if entries[2].content != "Issue 42 says hello." {
		t.Fatalf("final assistant content = %q", entries[2].content)
	}
}

func TestLoop_UnknownToolNameSurfacesAsErrorResult(t *testing.T) {
	// Pool advertises no tools — an unknown name must yield a synthetic
	// error result (not crash the loop).
	pool := newFakePool()
	reg := &fakeRegistry{
		streams: []corellm.Stream{
			&scriptedStream{
				final: corellm.Response{
					Content:      []corellm.ContentPart{{Type: "text", Text: "I tried but the tool isn't available."}},
					FinishReason: "end_turn",
				},
			},
		},
	}
	loop, err := New(Config{Registry: reg, Pool: pool, History: &recordingHistory{}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	initial := &corellm.Response{
		FinishReason: "tool_use",
		ToolCalls: []corellm.ToolUse{
			{ID: "x", Name: "ghost_tool", Input: json.RawMessage(`{}`)},
		},
	}
	if err := loop.Run(context.Background(), "sess", "sub", initial, corellm.GenerationRequest{}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	// Registry was still invoked with the synthetic error result so
	// the model can recover.
	if len(reg.requests) != 1 {
		t.Fatalf("expected 1 registry call, got %d", len(reg.requests))
	}
	msgs := reg.requests[0].Messages
	last := msgs[len(msgs)-1]
	if last.Role != corellm.RoleTool {
		t.Fatalf("last msg role = %q", last.Role)
	}
	var payload map[string]any
	_ = json.Unmarshal(last.Content[0].ToolData, &payload)
	if payload["is_error"] != true {
		t.Fatalf("is_error = %v, want true", payload["is_error"])
	}
}

func TestLoop_PoolErrorSurfacesAsToolFailure(t *testing.T) {
	pool := newFakePool()
	pool.register("svc", "boom", func(_ context.Context, _ json.RawMessage) (json.RawMessage, error) {
		return nil, errors.New("upstream 500")
	})
	reg := &fakeRegistry{
		streams: []corellm.Stream{
			&scriptedStream{
				final: corellm.Response{
					Content:      []corellm.ContentPart{{Type: "text", Text: "tool failed; sorry"}},
					FinishReason: "end_turn",
				},
			},
		},
	}
	loop, err := New(Config{Registry: reg, Pool: pool})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	initial := &corellm.Response{
		FinishReason: "tool_use",
		ToolCalls:    []corellm.ToolUse{{ID: "1", Name: "boom", Input: json.RawMessage(`{}`)}},
	}
	if err := loop.Run(context.Background(), "s", "p", initial, corellm.GenerationRequest{}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	// The single call landed on the pool.
	if len(pool.calls) != 1 {
		t.Fatalf("expected 1 pool call, got %d", len(pool.calls))
	}
	// Registry was re-invoked with the tool failure threaded as a
	// tool result message.
	if len(reg.requests) != 1 {
		t.Fatalf("expected 1 registry call, got %d", len(reg.requests))
	}
}

func TestLoop_MaxIterGuardsRunaway(t *testing.T) {
	// Pool always succeeds.
	pool := newFakePool()
	pool.register("s", "t", func(_ context.Context, _ json.RawMessage) (json.RawMessage, error) {
		return json.RawMessage(`"ok"`), nil
	})
	// Registry always returns tool_use → never converges.
	makeStream := func() corellm.Stream {
		return &scriptedStream{
			final: corellm.Response{
				FinishReason: "tool_use",
				ToolCalls:    []corellm.ToolUse{{ID: "x", Name: "t", Input: json.RawMessage(`{}`)}},
			},
		}
	}
	reg := &fakeRegistry{}
	for i := 0; i < 20; i++ {
		reg.streams = append(reg.streams, makeStream())
	}
	loop, err := New(Config{Registry: reg, Pool: pool, MaxIter: 3})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	initial := &corellm.Response{
		FinishReason: "tool_use",
		ToolCalls:    []corellm.ToolUse{{ID: "0", Name: "t", Input: json.RawMessage(`{}`)}},
	}
	err = loop.Run(context.Background(), "s", "p", initial, corellm.GenerationRequest{})
	if err == nil {
		t.Fatal("expected max-iter error")
	}
	// 3 iterations of dispatch → registry invocation: 3 pool calls,
	// 3 registry calls. The initial response is supplied by the
	// caller, so the first iteration uses it without invoking the
	// registry.
	if len(pool.calls) != 3 {
		t.Fatalf("pool calls = %d, want 3", len(pool.calls))
	}
	if len(reg.requests) != 3 {
		t.Fatalf("registry calls = %d, want 3", len(reg.requests))
	}
}

func TestLoop_ContextCancellation(t *testing.T) {
	pool := newFakePool()
	pool.register("s", "t", func(_ context.Context, _ json.RawMessage) (json.RawMessage, error) {
		return json.RawMessage(`"ok"`), nil
	})
	reg := &fakeRegistry{}
	loop, _ := New(Config{Registry: reg, Pool: pool})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	initial := &corellm.Response{
		FinishReason: "tool_use",
		ToolCalls:    []corellm.ToolUse{{ID: "0", Name: "t", Input: json.RawMessage(`{}`)}},
	}
	err := loop.Run(ctx, "s", "p", initial, corellm.GenerationRequest{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}
