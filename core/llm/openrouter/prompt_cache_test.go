package openrouter

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"

	llm "github.com/kameas-ai/kenaz-harness/core/llm"
)

func cacheReqOR(volatile string) llm.GenerationRequest {
	req := minReqOR()
	req.System = "You are the chat node."
	req.SystemVolatile = volatile
	req.Tools = llm.OrderToolsFlat([]llm.ToolSpec{
		{Name: "outlook__send-mail", Description: "Send mail", InputSchema: json.RawMessage(`{"type":"object"}`)},
		{Name: "kenaz__bash", Description: "Run a command", InputSchema: json.RawMessage(`{"type":"object"}`)},
	})
	return req
}

// The marked OpenRouter request: the system message becomes content parts
// with cache_control on the stable part and the per-call part after it;
// the last stable tool carries cache_control beside "type"/"function".
func TestOpenRouterAdapter_PromptCache_MarkerSerialized(t *testing.T) {
	body, err := buildRequestBodyAt(cacheReqOR("Current date: 2026-10-09."), stdProfOR("anthropic/claude-sonnet-4.5"), llm.CacheMarkAll)
	if err != nil {
		t.Fatal(err)
	}
	var p struct {
		Messages []json.RawMessage `json:"messages"`
		Tools    []json.RawMessage `json:"tools"`
	}
	if err := json.Unmarshal(body, &p); err != nil {
		t.Fatal(err)
	}
	wantSys := `{"content":[{"cache_control":{"type":"ephemeral"},"text":"You are the chat node.","type":"text"},{"text":"Current date: 2026-10-09.","type":"text"}],"role":"system"}`
	if string(p.Messages[0]) != wantSys {
		t.Errorf("system message =\n%s\nwant\n%s", p.Messages[0], wantSys)
	}
	if strings.Contains(string(p.Tools[0]), "cache_control") ||
		string(p.Tools[1]) != `{"cache_control":{"type":"ephemeral"},"function":{"description":"Send mail","name":"outlook__send-mail","parameters":{"type":"object"}},"type":"function"}` {
		t.Errorf("tools = %s / %s", p.Tools[0], p.Tools[1])
	}
}

// Unmarked (no marker level, or the legacy builder), the per-call segment
// is folded into the plain system string.
func TestOpenRouterAdapter_SystemVolatile_SystemVolatileSerialized(t *testing.T) {
	body := serialiseOR(t, cacheReqOR("Workspace: 2 entries."), stdProfOR("anthropic/claude-sonnet-4.5"))
	if strings.Contains(string(body), "cache_control") {
		t.Errorf("unmarked body carries a marker: %s", body)
	}
	var p struct {
		Messages []struct {
			Role    string `json:"role"`
			Content any    `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(body, &p); err != nil {
		t.Fatal(err)
	}
	if p.Messages[0].Role != "system" || p.Messages[0].Content != "You are the chat node.\n\nWorkspace: 2 entries." {
		t.Errorf("system message = %+v", p.Messages[0])
	}

	t.Setenv("HARNESS_LLM_USE_OPENAIWIRE", "false")
	legacy, err := buildRequestBodyAt(cacheReqOR("Workspace: 2 entries."), stdProfOR("anthropic/claude-sonnet-4.5"), llm.CacheMarkAll)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(legacy), "cache_control") || !strings.Contains(string(legacy), `Workspace: 2 entries.`) {
		t.Errorf("legacy builder: %s", legacy)
	}
}

// CacheStableTools places the tool marker; a negative value leaves only
// the system marker.
func TestOpenRouterAdapter_CacheStableTools_CacheStableToolsSerialized(t *testing.T) {
	req := cacheReqOR("")
	req.CacheStableTools = 1
	body, err := buildRequestBodyAt(req, stdProfOR("anthropic/claude-sonnet-4.5"), llm.CacheMarkAll)
	if err != nil {
		t.Fatal(err)
	}
	var p struct {
		Tools []map[string]any `json:"tools"`
	}
	if err := json.Unmarshal(body, &p); err != nil {
		t.Fatal(err)
	}
	if _, ok := p.Tools[0]["cache_control"]; !ok {
		t.Errorf("CacheStableTools=1 should mark tool 0: %s", body)
	}
	if _, ok := p.Tools[1]["cache_control"]; ok {
		t.Errorf("tool 1 is outside the stable segment: %s", body)
	}

	req.CacheStableTools = -1
	body, err = buildRequestBodyAt(req, stdProfOR("anthropic/claude-sonnet-4.5"), llm.CacheMarkAll)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(body), "cache_control") != 1 {
		t.Errorf("no stable tools: only the system marker expected: %s", body)
	}
}

// FR-C1 over the OpenRouter wire: the stable system part and the tool
// bytes are identical across two calls whose per-call segment differs.
func TestOpenRouterAdapter_PromptCache_GoldenPrefixStable(t *testing.T) {
	prefix := func(volatile string) (string, string) {
		body, err := buildRequestBodyAt(cacheReqOR(volatile), stdProfOR("~anthropic/claude-sonnet-latest"), llm.CacheMarkAll)
		if err != nil {
			t.Fatal(err)
		}
		var p struct {
			Messages []struct {
				Content json.RawMessage `json:"content"`
			} `json:"messages"`
			Tools json.RawMessage `json:"tools"`
		}
		if err := json.Unmarshal(body, &p); err != nil {
			t.Fatal(err)
		}
		var parts []json.RawMessage
		if err := json.Unmarshal(p.Messages[0].Content, &parts); err != nil {
			t.Fatal(err)
		}
		return string(parts[0]), string(p.Tools)
	}
	s1, t1 := prefix("Current date: 2026-10-09. Workspace: 0 entries.")
	s2, t2 := prefix("Current date: 2026-10-09. Workspace: 5 entries.")
	if s1 != s2 || t1 != t2 {
		t.Errorf("prefix differs:\n%s\n%s\n%s\n%s", s1, s2, t1, t2)
	}
}

// FR-C2 per model: Anthropic-family ids (including the ~ alias) carry the
// markers; every other model carries none.
func TestOpenRouterAdapter_PromptCache_MarkerPerModel(t *testing.T) {
	var (
		mu     sync.Mutex
		bodies []string
	)
	var fs *fakeServer
	fs = newFakeServer(t, func(w http.ResponseWriter, _ *http.Request, _ int) {
		b := fs.body()
		mu.Lock()
		bodies = append(bodies, b)
		mu.Unlock()
		writeOKStream(w)
	})
	a := newAdapter(fs)
	for _, model := range []string{"anthropic/claude-sonnet-4.5", "~anthropic/claude-sonnet-latest", "openai/gpt-4o", "z-ai/glm-5.2"} {
		s, err := a.Stream(context.Background(), cacheReqOR("v"), stdProfOR(model), []byte("sk"))
		if err != nil {
			t.Fatalf("%s: %v", model, err)
		}
		if _, _, err := drain(t, s); err != nil {
			t.Fatalf("%s: %v", model, err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	want := []int{2, 2, 0, 0}
	for i, b := range bodies {
		if n := strings.Count(b, `"cache_control":{"type":"ephemeral"}`); n != want[i] {
			t.Errorf("request %d carries %d markers, want %d: %s", i, n, want[i], b)
		}
	}
}

// The model list narrows the curated table: an Anthropic-family entry
// whose pricing reports no cache-read price is not marked.
func TestOpenRouterAdapter_ListModels_SupportsPromptCache(t *testing.T) {
	fs := newFakeServer(t, func(w http.ResponseWriter, r *http.Request, _ int) {
		if strings.HasSuffix(r.URL.Path, "/models") {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":[
				{"id":"anthropic/claude-sonnet-4.5","name":"Sonnet","pricing":{"prompt":"0.000003","input_cache_read":"0.0000003"}},
				{"id":"anthropic/claude-nocache","name":"No cache","pricing":{"prompt":"0.000003","input_cache_read":"0"}},
				{"id":"anthropic/claude-unpriced","name":"Unpriced"},
				{"id":"anthropic/claude-nocachefield","name":"No cache field","pricing":{"prompt":"0.000003"}},
				{"id":"openai/gpt-4o","name":"GPT-4o","pricing":{"prompt":"0.0000025","input_cache_read":"0.00000125"}}
			]}`))
			return
		}
		writeOKStream(w)
	})
	a := newAdapter(fs)
	models, err := a.ListModels(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, m := range models {
		got[m.ID] = m.SupportsPromptCache
	}
	want := map[string]bool{
		"anthropic/claude-sonnet-4.5":   true,
		"anthropic/claude-nocache":      false,
		"anthropic/claude-unpriced":     true,
		"anthropic/claude-nocachefield": true, // absent price is unknown, not a veto
		"openai/gpt-4o":                 false,
	}
	for id, w := range want {
		if got[id] != w {
			t.Errorf("%s SupportsPromptCache = %v, want %v", id, got[id], w)
		}
	}
	if a.cacheLevel("p-or", "anthropic/claude-nocache") != llm.CacheMarkNone {
		t.Errorf("a listed model without cache pricing must not be marked")
	}
	if a.cacheLevel("p-or", "anthropic/claude-sonnet-4.5") != llm.CacheMarkAll {
		t.Errorf("a listed cache-priced Anthropic model should be marked")
	}
}

// FR-C2 degrade: a 400 naming cache_control is not a failed call. The
// request is resent with fewer markers and the adapter remembers.
func TestOpenRouterAdapter_PromptCache_DegradesOnRejection(t *testing.T) {
	var (
		mu     sync.Mutex
		bodies []string
	)
	var fs *fakeServer
	fs = newFakeServer(t, func(w http.ResponseWriter, _ *http.Request, _ int) {
		b := fs.body()
		mu.Lock()
		bodies = append(bodies, b)
		mu.Unlock()
		if strings.Contains(b, "cache_control") {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"code":400,"message":"Provider returned error: cache_control is not supported"}}`))
			return
		}
		writeOKStream(w)
	})
	a := newAdapter(fs)
	s, err := a.Stream(context.Background(), cacheReqOR("v"), stdProfOR("anthropic/claude-sonnet-4.5"), []byte("sk"))
	if err != nil {
		t.Fatalf("a cache_control rejection must not fail the call: %v", err)
	}
	if _, _, err := drain(t, s); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	n := len(bodies)
	last := bodies[n-1]
	mu.Unlock()
	if n != 3 {
		t.Errorf("requests = %d, want marked, system-only, unmarked", n)
	}
	if strings.Contains(last, "cache_control") || !strings.Contains(last, `You are the chat node.\n\nv`) {
		t.Errorf("final request should be unmarked with the segment folded in: %s", last)
	}
	if a.cacheGuard.Level("p-or", "anthropic/claude-sonnet-4.5") != llm.CacheMarkNone {
		t.Errorf("guard = %v, want none", a.cacheGuard.Level("p-or", "anthropic/claude-sonnet-4.5"))
	}
}

// OpenRouter routes many models through one profile: a cache_control
// rejection for one model strips markers from that model only.
func TestOpenRouterAdapter_PromptCache_DegradeIsPerModel(t *testing.T) {
	var fs *fakeServer
	fs = newFakeServer(t, func(w http.ResponseWriter, _ *http.Request, _ int) {
		b := fs.body()
		if strings.Contains(b, `"model":"anthropic/claude-rejects"`) && strings.Contains(b, "cache_control") {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"code":400,"message":"cache_control not supported"}}`))
			return
		}
		writeOKStream(w)
	})
	a := newAdapter(fs)
	for _, model := range []string{"anthropic/claude-rejects", "anthropic/claude-sonnet-4.5"} {
		s, err := a.Stream(context.Background(), cacheReqOR("v"), stdProfOR(model), []byte("sk"))
		if err != nil {
			t.Fatalf("%s: %v", model, err)
		}
		if _, _, err := drain(t, s); err != nil {
			t.Fatal(err)
		}
	}
	if got := a.cacheGuard.Level("p-or", "anthropic/claude-rejects"); got != llm.CacheMarkNone {
		t.Errorf("rejecting model level = %v, want none", got)
	}
	if got := a.cacheGuard.Level("p-or", "anthropic/claude-sonnet-4.5"); got != llm.CacheMarkAll {
		t.Errorf("other model level = %v, want system+tools", got)
	}
	if b := fs.body(); !strings.Contains(b, "cache_control") {
		t.Errorf("the other model's request lost its markers: %s", b)
	}
}

func writeOKStream(w http.ResponseWriter) {
	writeSSEFrames(w, []string{
		`data: {"choices":[{"delta":{"content":"ok"},"finish_reason":"stop"}]}`,
		``,
		`data: {"choices":[],"usage":{"prompt_tokens":5,"completion_tokens":1,"total_tokens":6}}`,
		``,
		`data: [DONE]`,
		``,
	})
}

// body is the request body the server read for the request being
// handled; the handler runs after the server records it.
func (fs *fakeServer) body() string {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	return string(fs.lastBody)
}
