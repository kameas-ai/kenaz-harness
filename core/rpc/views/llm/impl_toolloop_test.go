package llm

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	corellm "github.com/sigil-tech/kaneaz-harness/core/llm"
	"github.com/sigil-tech/kaneaz-harness/core/toolloop"
)

// queuedRegistry returns the next queued stream on each Stream call so
// the test can drive the multi-turn loop end-to-end.
type queuedRegistry struct {
	mu       sync.Mutex
	streams  []corellm.Stream
	requests []corellm.GenerationRequest
}

func (r *queuedRegistry) RegisterAdapter(_ corellm.ProviderAdapter) {}
func (r *queuedRegistry) LoadProfiles(profs []corellm.ProviderProfile) error {
	return nil
}
func (r *queuedRegistry) Profile(string) (corellm.ProviderProfile, error) {
	return corellm.ProviderProfile{ID: "p", Kind: "anthropic", Model: "x"}, nil
}
func (r *queuedRegistry) PreflightAll(context.Context) []corellm.PreflightResult { return nil }
func (r *queuedRegistry) Stream(_ context.Context, req corellm.GenerationRequest) (corellm.Stream, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.requests = append(r.requests, req)
	if len(r.streams) == 0 {
		return nil, errInternal("no streams queued")
	}
	s := r.streams[0]
	r.streams = r.streams[1:]
	return s, nil
}

type errInternal string

func (e errInternal) Error() string { return string(e) }

// recordingHistoryRW captures both reads and writes so the test can
// assert the user → assistant → tool → assistant sequence after the
// pump + loop finish.
type recordingHistoryRW struct {
	mu       sync.Mutex
	listed   []SessionMessage
	written  []writeRec
	systemFn func() (string, string, error)
}

type writeRec struct {
	sessionID string
	role      string
	content   string
}

func (h *recordingHistoryRW) ListMessages(_ context.Context, _ string) ([]SessionMessage, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]SessionMessage, len(h.listed))
	copy(out, h.listed)
	return out, nil
}
func (h *recordingHistoryRW) AppendMessage(_ context.Context, sessionID, role, content string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.written = append(h.written, writeRec{sessionID: sessionID, role: role, content: content})
	// Mirror the write into listed so a subsequent read observes it.
	h.listed = append(h.listed, SessionMessage{Role: role, Content: content})
	return nil
}
func (h *recordingHistoryRW) snapshot() []writeRec {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]writeRec, len(h.written))
	copy(out, h.written)
	return out
}

// fixturePool is a pure in-memory MCPPool implementation suitable for
// the views/llm package's test isolation (no dependency on
// core/mcp/fixture).
type fixturePool struct {
	mu    sync.Mutex
	tools []toolloop.Tool
	calls []fixtureCall
	resp  json.RawMessage
}

type fixtureCall struct {
	server, tool string
	args         json.RawMessage
}

func (p *fixturePool) Tools(_ context.Context) ([]toolloop.Tool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]toolloop.Tool, len(p.tools))
	copy(out, p.tools)
	return out, nil
}
func (p *fixturePool) Call(_ context.Context, server, tool string, args json.RawMessage) (json.RawMessage, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls = append(p.calls, fixtureCall{server: server, tool: tool, args: append(json.RawMessage(nil), args...)})
	return p.resp, nil
}

// TestPump_ToolUseTriggersLoop_EndToEnd is the WP01 acceptance proof:
// a fake LLM stream emits a tool_use finish, the pump hands off to the
// loop, the loop dispatches one tool to the in-memory pool, threads
// the result back, the registry's second stream returns end_turn, and
// the session history shows user → assistant(tool_use) → tool →
// assistant(final).
func TestPump_ToolUseTriggersLoop_EndToEnd(t *testing.T) {
	// Initial stream: model emits a single tool_use call, no inline
	// text. FinishReason == "tool_use".
	initialStream := &fakeStream{
		chunks: []corellm.StreamEvent{
			{Kind: corellm.StreamTool, Tool: &corellm.ToolUse{
				ID: "tool_1", Name: "get_issue", Input: json.RawMessage(`{"number":42}`),
			}},
			{Kind: corellm.StreamFinish, Finish: "tool_use"},
		},
		final: corellm.Response{
			FinishReason: "tool_use",
			ToolCalls: []corellm.ToolUse{
				{ID: "tool_1", Name: "get_issue", Input: json.RawMessage(`{"number":42}`)},
			},
		},
	}
	// Second stream (loop's re-invocation): plain end_turn with text.
	finalStream := &fakeStream{
		final: corellm.Response{
			Content:      []corellm.ContentPart{{Type: "text", Text: "Issue 42 is open."}},
			FinishReason: "end_turn",
		},
	}
	reg := &queuedRegistry{streams: []corellm.Stream{initialStream, finalStream}}
	pool := &fixturePool{
		tools: []toolloop.Tool{{Server: "github", Name: "get_issue"}},
		resp:  json.RawMessage(`{"number":42,"title":"hello"}`),
	}
	loop, err := toolloop.New(toolloop.Config{
		Registry: reg,
		Pool:     pool,
		// We don't pass the same history; the pump's writer carries
		// the persistence path. The loop's own SessionHistoryRW will
		// be wired to the same recordingHistoryRW so writes to both
		// surfaces converge in one place.
	})
	if err != nil {
		t.Fatalf("toolloop.New: %v", err)
	}
	hist := &recordingHistoryRW{
		listed: []SessionMessage{{Role: "user", Content: "what's in issue 42?"}},
	}
	// Re-build the loop with the history wired so persistence is
	// observable in this test.
	loop, err = toolloop.New(toolloop.Config{
		Registry: reg,
		Pool:     pool,
		History:  hist,
	})
	if err != nil {
		t.Fatalf("toolloop.New: %v", err)
	}
	sink := &recordingSink{}
	api := New(Config{
		Registry:      reg,
		Sink:          sink,
		History:       hist,
		HistoryWriter: hist,
		ToolLoop:      loop,
	})

	subID, err := api.StartStream(context.Background(), "p", "sess-1", "")
	if err != nil {
		t.Fatalf("StartStream: %v", err)
	}
	if subID == "" {
		t.Fatal("empty sub id")
	}

	// Wait for stream-closed AND the loop to drain. The loop runs
	// synchronously after stream-closed inside the same pump
	// goroutine, so once StopStream returns (or the pump's done
	// channel closes) we can assert.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if hasFinalAssistant(hist.snapshot()) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Two registry calls total: the initial StartStream + the loop's
	// re-invocation.
	if len(reg.requests) != 2 {
		t.Fatalf("registry calls = %d, want 2", len(reg.requests))
	}
	// One pool call: the get_issue dispatch.
	if len(pool.calls) != 1 {
		t.Fatalf("pool calls = %d, want 1", len(pool.calls))
	}
	if pool.calls[0].server != "github" || pool.calls[0].tool != "get_issue" {
		t.Fatalf("pool call meta = %+v", pool.calls[0])
	}

	// The augmented re-invocation request includes the user turn from
	// history plus the synthetic assistant tool_use turn plus the
	// tool result.
	rereq := reg.requests[1]
	roles := make([]string, 0, len(rereq.Messages))
	for _, m := range rereq.Messages {
		roles = append(roles, string(m.Role))
	}
	if got := lastN(roles, 3); !equalStrs(got, []string{"user", "assistant", "tool"}) {
		t.Fatalf("re-invocation message roles tail = %v", got)
	}

	// The session history should contain (user — pre-existing),
	// assistant tool_use envelope, tool result, final assistant
	// text. The pump did NOT persist the assistant turn for the
	// initial stream because deferAssistantToLoop kicked in.
	writes := hist.snapshot()
	if len(writes) != 3 {
		t.Fatalf("history writes = %d, want 3 (assistant tool_use, tool, assistant final): %+v", len(writes), writes)
	}
	if writes[0].role != "assistant" {
		t.Fatalf("writes[0].role = %q", writes[0].role)
	}
	if writes[1].role != "tool" {
		t.Fatalf("writes[1].role = %q", writes[1].role)
	}
	if writes[2].role != "assistant" || writes[2].content != "Issue 42 is open." {
		t.Fatalf("writes[2] = %+v", writes[2])
	}

	// The pump still emits the canonical stream-closed for the
	// initial turn.
	closed, ok := sink.lastClosed()
	if !ok {
		t.Fatal("expected stream-closed event")
	}
	if closed.FinishReason != "tool_use" {
		t.Fatalf("initial close finish = %q, want tool_use", closed.FinishReason)
	}
}

// TestPump_NonToolUseFinishUnaffected is a regression guard: a normal
// end_turn response must not invoke the loop and the existing
// pump-persistence path must still write the assistant text.
func TestPump_NonToolUseFinishUnaffected(t *testing.T) {
	stream := &fakeStream{
		chunks: []corellm.StreamEvent{
			{Kind: corellm.StreamText, Text: "hi"},
			{Kind: corellm.StreamFinish, Finish: "end_turn"},
		},
		final: corellm.Response{
			Content:      []corellm.ContentPart{{Type: "text", Text: "hi"}},
			FinishReason: "end_turn",
		},
	}
	reg := &queuedRegistry{streams: []corellm.Stream{stream}}
	pool := &fixturePool{}
	loop, _ := toolloop.New(toolloop.Config{Registry: reg, Pool: pool})
	hist := &recordingHistoryRW{
		listed: []SessionMessage{{Role: "user", Content: "hello"}},
	}
	api := New(Config{Registry: reg, History: hist, HistoryWriter: hist, ToolLoop: loop})

	if _, err := api.StartStream(context.Background(), "p", "sess", ""); err != nil {
		t.Fatalf("StartStream: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(hist.snapshot()) > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(reg.requests) != 1 {
		t.Fatalf("registry calls = %d, want 1 (no loop)", len(reg.requests))
	}
	if len(pool.calls) != 0 {
		t.Fatalf("pool calls = %d, want 0", len(pool.calls))
	}
	writes := hist.snapshot()
	if len(writes) != 1 || writes[0].role != "assistant" || writes[0].content != "hi" {
		t.Fatalf("history = %+v, want one assistant write", writes)
	}
}

func hasFinalAssistant(writes []writeRec) bool {
	if len(writes) < 1 {
		return false
	}
	last := writes[len(writes)-1]
	// The final assistant write from the loop is a plain text body
	// (not a JSON envelope). Detect that by checking the role + the
	// absence of a leading '{' since envelopes are JSON.
	return last.role == "assistant" && len(last.content) > 0 && last.content[0] != '{'
}

func lastN(s []string, n int) []string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}

func equalStrs(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
