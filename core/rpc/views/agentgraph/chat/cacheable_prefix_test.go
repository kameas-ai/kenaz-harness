package chat

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	coreag "github.com/kameas-ai/kenaz-harness/core/agentgraph"
	corellm "github.com/kameas-ai/kenaz-harness/core/llm"
	"github.com/kameas-ai/kenaz-harness/core/llm/anthropic"
)

// steppingClock returns a clock the test advances by hand.
type steppingClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *steppingClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *steppingClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

// tool-context-budget-01TCBUD01 WP05 / FR-C1: two consecutive model
// calls with unchanged settings — one minute apart, across midnight, with
// the workspace changed in between and pending hook context on one of
// them — send a byte-identical system prefix and tool list. Everything
// per-call travels in SystemVolatile, which the Anthropic wire puts after
// the cache marker.
func TestGenerate_CacheablePrefixStableAcrossCalls(t *testing.T) {
	ws := t.TempDir()
	clock := &steppingClock{now: time.Date(2026, time.October, 8, 23, 59, 30, 0, time.UTC)}
	tools := []corellm.ToolSpec{
		{Name: "outlook__send-mail", Description: "Send mail", InputSchema: json.RawMessage(`{"type":"object"}`)},
		{Name: "kenaz__bash", Description: "Run a command", InputSchema: json.RawMessage(`{"type":"object"}`)},
		{Name: "kenaz__read_file", Description: "Read a file", InputSchema: json.RawMessage(`{"type":"object"}`)},
	}
	reg := &capturingRegistry{}
	q := newPendingContextQueue()
	adapter := NewLLMProviderAdapter(reg, "p-ant", "claude-sonnet-4-5", tools, nil).
		WithSessionID("sess-cache").
		WithEnvContext(clock.Now, ws, "").
		WithCustomInstructions(func() string { return "Prefer tables." }).
		withPendingContext(q)

	call := func(pending bool) corellm.GenerationRequest {
		if pending {
			if err := q.AppendSystemContext(context.Background(), "sess-cache", "repo uses tabs"); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := adapter.Generate(context.Background(), coreag.LLMRequest{
			SystemPrompt: "You are the chat node.",
			Messages:     []coreag.Message{{Role: "user", Content: "hi"}},
			StreamToChat: true,
		}); err != nil {
			t.Fatalf("Generate: %v", err)
		}
		return reg.snapshot()
	}

	first := call(false)
	clock.Advance(time.Minute)
	if err := os.WriteFile(filepath.Join(ws, "notes.md"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	second := call(true)

	if first.System != second.System {
		t.Fatalf("stable system prompt changed between calls:\n--- first ---\n%s\n--- second ---\n%s", first.System, second.System)
	}
	for _, perCall := range []string{"2026-10-08", "2026-10-09", "Workspace contents", "repo uses tabs", "available across"} {
		if strings.Contains(first.System, perCall) {
			t.Errorf("per-call material %q is in the cacheable system prefix:\n%s", perCall, first.System)
		}
	}
	if !strings.Contains(first.SystemVolatile, "Current date: 2026-10-08.") || !strings.Contains(first.SystemVolatile, "Workspace contents: empty.") {
		t.Errorf("first call's per-call segment = %q", first.SystemVolatile)
	}
	if !strings.Contains(second.SystemVolatile, "Current date: 2026-10-09.") ||
		!strings.Contains(second.SystemVolatile, "Workspace contents: 1 entry.") ||
		!strings.Contains(second.SystemVolatile, "repo uses tabs") {
		t.Errorf("second call's per-call segment = %q", second.SystemVolatile)
	}
	names := make([]string, len(first.Tools))
	for i, tl := range first.Tools {
		names[i] = tl.Name
	}
	if strings.Join(names, ",") != "kenaz__bash,kenaz__read_file,outlook__send-mail" {
		t.Errorf("tools not in OrderTools order: %v", names)
	}

	// The same two requests on the Anthropic wire: the marked system
	// block and the tools array are byte-identical.
	sysA, toolsA := anthropicPrefix(t, first)
	sysB, toolsB := anthropicPrefix(t, second)
	if sysA != sysB {
		t.Errorf("marked system block differs on the wire:\n%s\n%s", sysA, sysB)
	}
	if toolsA != toolsB {
		t.Errorf("tools differ on the wire:\n%s\n%s", toolsA, toolsB)
	}
	if !strings.Contains(sysA, `"cache_control":{"type":"ephemeral"}`) || !strings.Contains(toolsA, `"cache_control":{"type":"ephemeral"}`) {
		t.Errorf("Anthropic request carries no cache marker: %s / %s", sysA, toolsA)
	}
}

// anthropicPrefix sends gen through the real Anthropic adapter and returns
// the wire bytes of the first (stable, marked) system block and of the
// tools array.
func anthropicPrefix(t *testing.T, gen corellm.GenerationRequest) (string, string) {
	t.Helper()
	var (
		mu   sync.Mutex
		body []byte
	)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		body = b
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("data: {\"type\":\"message_stop\"}\n\n"))
	}))
	defer ts.Close()
	a := anthropic.New(anthropic.WithEndpoint(ts.URL), anthropic.WithHTTPClient(ts.Client()))
	s, err := a.Stream(context.Background(), gen, corellm.ProviderProfile{ID: "p-ant", Kind: "anthropic", Model: "claude-sonnet-4-5"}, []byte("k"))
	if err != nil {
		t.Fatalf("anthropic Stream: %v", err)
	}
	for range s.Events() {
	}
	_, _ = s.Final()
	mu.Lock()
	defer mu.Unlock()
	var p struct {
		System []json.RawMessage `json:"system"`
		Tools  json.RawMessage   `json:"tools"`
	}
	if err := json.Unmarshal(body, &p); err != nil {
		t.Fatalf("parse wire body: %v\n%s", err, body)
	}
	if len(p.System) == 0 {
		t.Fatalf("no system blocks on the wire: %s", body)
	}
	return string(p.System[0]), string(p.Tools)
}
