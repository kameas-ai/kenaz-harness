package anthropic

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	llm "github.com/kameas-ai/kenaz-harness/core/llm"
	"github.com/kameas-ai/kenaz-harness/core/logging"
)

// cacheReq is a request with a stable system prompt, a per-call segment
// and three tools of which the first two are the stable segment.
func cacheReq(volatile string, tools []llm.ToolSpec) llm.GenerationRequest {
	req := minReq()
	req.System = "You are the chat node."
	req.SystemVolatile = volatile
	req.Tools = llm.OrderTools(tools)
	req.CacheStableTools = 2
	return req
}

func cacheTools() []llm.ToolSpec {
	return []llm.ToolSpec{
		{Name: "outlook__send-mail", Description: "Send mail", InputSchema: json.RawMessage(`{"type":"object","properties":{"to":{"type":"string"}}}`)},
		{Name: "kenaz__bash", Description: "Run a command", InputSchema: json.RawMessage(`{"properties":{"cmd":{"type":"string"}},"type":"object"}`)},
		{Name: "kenaz__grep", Description: "Search files", InputSchema: json.RawMessage(`{"type":"object"}`)},
	}
}

// The marked request carries cache_control on the stable system block and
// on the last tool of the stable segment, and the per-call segment as an
// unmarked block after it. This is the exact wire shape.
func TestAnthropicAdapter_PromptCache_MarkerSerialized(t *testing.T) {
	body, err := buildRequestBodyAt(cacheReq("Current date: 2026-10-09.", cacheTools()), stdProf("claude-sonnet-4-5"), llm.CacheMarkAll)
	if err != nil {
		t.Fatal(err)
	}
	var parsed map[string]json.RawMessage
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatal(err)
	}
	wantSystem := `[{"cache_control":{"type":"ephemeral"},"text":"You are the chat node.","type":"text"},{"text":"Current date: 2026-10-09.","type":"text"}]`
	if string(parsed["system"]) != wantSystem {
		t.Errorf("system =\n%s\nwant\n%s", parsed["system"], wantSystem)
	}
	wantTools := `[{"description":"Run a command","input_schema":{"properties":{"cmd":{"type":"string"}},"type":"object"},"name":"kenaz__bash"},` +
		`{"cache_control":{"type":"ephemeral"},"description":"Search files","input_schema":{"type":"object"},"name":"kenaz__grep"},` +
		`{"description":"Send mail","input_schema":{"properties":{"to":{"type":"string"}},"type":"object"},"name":"outlook__send-mail"}]`
	if string(parsed["tools"]) != wantTools {
		t.Errorf("tools =\n%s\nwant\n%s", parsed["tools"], wantTools)
	}
}

// CacheStableTools covers the default (all tools stable) and the
// no-stable-segment case.
func TestAnthropicAdapter_CacheStableTools_CacheStableToolsSerialized(t *testing.T) {
	marked := func(stable int) []bool {
		req := cacheReq("", cacheTools())
		req.CacheStableTools = stable
		body, err := buildRequestBodyAt(req, stdProf("claude-sonnet-4-5"), llm.CacheMarkAll)
		if err != nil {
			t.Fatal(err)
		}
		var parsed struct {
			Tools []map[string]any `json:"tools"`
		}
		if err := json.Unmarshal(body, &parsed); err != nil {
			t.Fatal(err)
		}
		out := make([]bool, len(parsed.Tools))
		for i, tl := range parsed.Tools {
			_, out[i] = tl["cache_control"]
		}
		return out
	}
	if got := marked(0); fmt.Sprint(got) != "[false false true]" {
		t.Errorf("default marks %v, want only the last tool", got)
	}
	if got := marked(-1); fmt.Sprint(got) != "[false false false]" {
		t.Errorf("no stable segment marks %v", got)
	}
}

// At CacheMarkSystemOnly the tool marker is gone and the system marker
// stays; at CacheMarkNone (and from buildRequestBody) the system is one
// string with the per-call segment appended.
func TestAnthropicAdapter_SystemVolatile_SystemVolatileSerialized(t *testing.T) {
	req := cacheReq("Workspace: 3 entries.", cacheTools())

	body, err := buildRequestBodyAt(req, stdProf("claude-sonnet-4-5"), llm.CacheMarkSystemOnly)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(body), "cache_control") != 1 || !strings.Contains(string(body), `"system":[{"cache_control"`) {
		t.Errorf("system-only body should carry exactly the system marker: %s", body)
	}

	plain := serialise(t, req, stdProf("claude-sonnet-4-5"))
	if strings.Contains(string(plain), "cache_control") {
		t.Errorf("unmarked body carries a marker: %s", plain)
	}
	var parsed struct {
		System string `json:"system"`
	}
	if err := json.Unmarshal(plain, &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed.System != "You are the chat node.\n\nWorkspace: 3 entries." {
		t.Errorf("unmarked system = %q", parsed.System)
	}
}

// FR-C1: two consecutive requests with unchanged settings serialise
// byte-identical system and tool bytes up to the marker, whatever order
// the catalog produced the tools in and whatever the per-call segment
// says.
func TestAnthropicAdapter_PromptCache_GoldenPrefixStable(t *testing.T) {
	tools := cacheTools()
	shuffled := []llm.ToolSpec{tools[2], tools[0], tools[1]}

	first, err := buildRequestBodyAt(cacheReq("Current date: 2026-10-09. Workspace: 0 entries.", tools), stdProf("claude-sonnet-4-5"), llm.CacheMarkAll)
	if err != nil {
		t.Fatal(err)
	}
	second, err := buildRequestBodyAt(cacheReq("Current date: 2026-10-09. Workspace: 4 entries.", shuffled), stdProf("claude-sonnet-4-5"), llm.CacheMarkAll)
	if err != nil {
		t.Fatal(err)
	}
	prefix := func(b []byte) (string, string) {
		var p struct {
			System []json.RawMessage `json:"system"`
			Tools  json.RawMessage   `json:"tools"`
		}
		if err := json.Unmarshal(b, &p); err != nil {
			t.Fatal(err)
		}
		return string(p.System[0]), string(p.Tools)
	}
	s1, t1 := prefix(first)
	s2, t2 := prefix(second)
	if s1 != s2 {
		t.Errorf("stable system block differs:\n%s\n%s", s1, s2)
	}
	if t1 != t2 {
		t.Errorf("tool bytes differ:\n%s\n%s", t1, t2)
	}
	if bytes.Equal(first, second) {
		t.Errorf("the per-call segment should differ between the two requests")
	}
}

// recordingServer serves a minimal SSE stream and records every request
// body; reject decides per body whether to answer 400 instead.
type recordingServer struct {
	mu     sync.Mutex
	bodies []string
	reject func(body string) bool
}

func (r *recordingServer) handler(w http.ResponseWriter, req *http.Request) {
	b, _ := io.ReadAll(req.Body)
	r.mu.Lock()
	r.bodies = append(r.bodies, string(b))
	reject := r.reject
	r.mu.Unlock()
	if reject != nil && reject(string(b)) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"type":"error","error":{"type":"invalid_request_error","message":"tools.1.cache_control: Extra inputs are not permitted"}}`))
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)
	for _, f := range []string{
		`{"type":"message_start","message":{"id":"m","role":"assistant","model":"claude-sonnet-4-5","usage":{"input_tokens":3,"output_tokens":1,"cache_read_input_tokens":2000}}}`,
		`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":1}}`,
		`{"type":"message_stop"}`,
	} {
		fmt.Fprintf(w, "data: %s\n\n", f)
	}
}

func (r *recordingServer) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.bodies...)
}

func streamOnce(t *testing.T, a *Adapter, req llm.GenerationRequest, model string) {
	t.Helper()
	s, err := a.Stream(context.Background(), req, stdProf(model), []byte("k"))
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	for range s.Events() {
	}
	if _, err := s.Final(); err != nil {
		t.Fatalf("Final: %v", err)
	}
}

// syncBuffer is a goroutine-safe log sink.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func captureLog(t *testing.T) *syncBuffer {
	t.Helper()
	sink := &syncBuffer{}
	logging.Replace(slog.NewJSONHandler(sink, &slog.HandlerOptions{Level: slog.LevelDebug}))
	t.Cleanup(func() { logging.Replace(logging.FileHandler()) })
	return sink
}

// FR-C2: a Claude model gets the markers on the wire; a model outside the
// curated table gets none.
func TestAnthropicAdapter_PromptCache_MarkerPerModel(t *testing.T) {
	rs := &recordingServer{}
	ts := httptest.NewServer(http.HandlerFunc(rs.handler))
	defer ts.Close()
	a := New(WithEndpoint(ts.URL), WithHTTPClient(ts.Client()))

	streamOnce(t, a, cacheReq("v", cacheTools()), "claude-sonnet-4-5")
	streamOnce(t, a, cacheReq("v", cacheTools()), "not-a-claude-model")
	got := rs.snapshot()
	if len(got) != 2 {
		t.Fatalf("requests = %d", len(got))
	}
	if strings.Count(got[0], `"cache_control":{"type":"ephemeral"}`) != 2 {
		t.Errorf("claude request should carry two markers: %s", got[0])
	}
	if strings.Contains(got[1], "cache_control") {
		t.Errorf("non-claude request carries a marker: %s", got[1])
	}
}

// FR-C2 degrade: a provider that rejects the tool marker gets the request
// again with the system marker only; one that rejects every marker gets
// it with none. The call succeeds, the adapter remembers, and the change
// is logged once per step.
func TestAnthropicAdapter_PromptCache_DegradesOnRejection(t *testing.T) {
	logs := captureLog(t)
	// Rejects a marker inside the tools array ("tools" is the last key
	// json.Marshal writes, so everything after it is the array).
	rs := &recordingServer{reject: func(body string) bool {
		i := strings.Index(body, `"tools":`)
		return i >= 0 && strings.Contains(body[i:], "cache_control")
	}}
	ts := httptest.NewServer(http.HandlerFunc(rs.handler))
	defer ts.Close()
	a := New(WithEndpoint(ts.URL), WithHTTPClient(ts.Client()))

	streamOnce(t, a, cacheReq("v", cacheTools()), "claude-sonnet-4-5")
	got := rs.snapshot()
	if len(got) != 2 {
		t.Fatalf("requests = %d, want the rejected one and the resend", len(got))
	}
	if strings.Count(got[1], "cache_control") != 1 || !strings.Contains(got[1], `"system":[{"cache_control"`) {
		t.Errorf("resend should carry the system marker only: %s", got[1])
	}
	if a.cacheGuard.Level() != llm.CacheMarkSystemOnly {
		t.Errorf("guard = %v, want system-only", a.cacheGuard.Level())
	}

	// The next call starts at the degraded level: one request.
	streamOnce(t, a, cacheReq("v", cacheTools()), "claude-sonnet-4-5")
	if n := len(rs.snapshot()); n != 3 {
		t.Errorf("requests after second call = %d, want 3", n)
	}

	// Now every marker is rejected: the system-only request degrades to
	// none and still succeeds.
	rs.mu.Lock()
	rs.reject = func(body string) bool { return strings.Contains(body, "cache_control") }
	rs.mu.Unlock()
	streamOnce(t, a, cacheReq("v", cacheTools()), "claude-sonnet-4-5")
	got = rs.snapshot()
	if len(got) != 5 || strings.Contains(got[4], "cache_control") {
		t.Fatalf("want a rejected system-only request then an unmarked one; got %d requests, last %s", len(got), got[len(got)-1])
	}
	if !strings.Contains(got[4], `"system":"You are the chat node.\n\nv"`) {
		t.Errorf("unmarked resend should fold the per-call segment into the system string: %s", got[4])
	}
	if n := strings.Count(logs.String(), `"msg":"llm.prompt_cache.unsupported"`); n != 2 {
		t.Errorf("llm.prompt_cache.unsupported logged %d times, want once per degrade step (2):\n%s", n, logs.String())
	}
}

// A 400 that does not name cache_control is returned as-is, with no
// resend.
func TestAnthropicAdapter_PromptCache_UnrelatedRejectionNotRetried(t *testing.T) {
	var calls int
	var mu sync.Mutex
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		calls++
		mu.Unlock()
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"type":"error","error":{"type":"invalid_request_error","message":"max_tokens: too large"}}`))
	}))
	defer ts.Close()
	a := New(WithEndpoint(ts.URL), WithHTTPClient(ts.Client()))
	if _, err := a.Stream(context.Background(), cacheReq("v", cacheTools()), stdProf("claude-sonnet-4-5"), []byte("k")); err == nil {
		t.Fatal("want the 400 back")
	}
	mu.Lock()
	defer mu.Unlock()
	if calls != 1 || a.cacheGuard.Level() != llm.CacheMarkAll {
		t.Errorf("calls=%d guard=%v, want 1 call and no degrade", calls, a.cacheGuard.Level())
	}
}
