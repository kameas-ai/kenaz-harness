package chat

// llm_request_tools_log_test.go covers the "llm.request.tools" log line
// (per-request tool-catalog visibility): before this line existed there
// was no way to answer "what tools did the model actually see on this
// specific request" straight from ~/.kenaz/harness/<env>/logs/harness.log
// — the closest thing, chat_runner.go's "chat.tool_discovery.ok", logs
// once per StartStream at discovery time, not once per outbound LLM
// request (a single StartStream can drive several Generate() calls via
// the tool-call loop).

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	coreag "github.com/kameas-ai/kenaz-harness/core/agentgraph"
	corellm "github.com/kameas-ai/kenaz-harness/core/llm"
	"github.com/kameas-ai/kenaz-harness/core/logging"
)

// captureChatLog replaces the process-global logger with a buffer-backed
// handler for the duration of f, then restores whatever was there before.
// Returns all JSON-line output produced during f. Mirrors core/rpc's
// captureLog test helper (core/rpc/api_update_construction_test.go); the
// chat package has no equivalent yet, so this is a local copy scoped to
// this file.
func captureChatLog(t *testing.T, f func()) string {
	t.Helper()
	var buf bytes.Buffer
	h := slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	logging.Replace(h)
	t.Cleanup(func() {
		logging.Replace(logging.FileHandler())
	})
	f()
	return buf.String()
}

// TestGenerate_LogsAttachedToolCatalogPerRequest asserts that every
// Generate() call — i.e. every actual outbound LLM request — emits exactly
// one "llm.request.tools" INFO log line carrying the session id, the
// count, and the sorted list of tool names actually attached to that
// request (gen.Tools). This must fire on EACH Generate() call, not once
// per StartStream, so a multi-round tool-call loop leaves one log line per
// round in the on-disk transcript.
func TestGenerate_LogsAttachedToolCatalogPerRequest(t *testing.T) {
	tools := []corellm.ToolSpec{
		{Name: "kenaz__web_search", Description: "search"},
		{Name: "kenaz__bash", Description: "bash"},
		{Name: "kenaz__ask_user_question", Description: "ask"},
	}
	reg := &usageStubRegistry{stream: &usageStubStream{final: corellm.Response{FinishReason: "stop"}}}
	adapter := NewLLMProviderAdapter(reg, "p-openrouter", "z-ai/glm-5.2", tools, nil).
		WithSessionID("sess-42")

	logs := captureChatLog(t, func() {
		if _, err := adapter.Generate(context.Background(), coreag.LLMRequest{
			SystemPrompt: "base",
			Messages:     []coreag.Message{{Role: "user", Content: "hi"}},
		}); err != nil {
			t.Fatalf("Generate: %v", err)
		}
	})

	var found []struct {
		Msg       string   `json:"msg"`
		SessionID string   `json:"session_id"`
		Count     int      `json:"count"`
		Tools     []string `json:"tools"`
	}
	for _, line := range strings.Split(strings.TrimSpace(logs), "\n") {
		if line == "" {
			continue
		}
		var rec struct {
			Msg       string   `json:"msg"`
			SessionID string   `json:"session_id"`
			Count     int      `json:"count"`
			Tools     []string `json:"tools"`
		}
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			continue
		}
		if rec.Msg == "llm.request.tools" {
			found = append(found, rec)
		}
	}

	if len(found) != 1 {
		t.Fatalf("expected exactly 1 llm.request.tools log line, got %d (logs: %s)", len(found), logs)
	}
	rec := found[0]
	if rec.SessionID != "sess-42" {
		t.Errorf("session_id = %q, want %q", rec.SessionID, "sess-42")
	}
	if rec.Count != 3 {
		t.Errorf("count = %d, want 3", rec.Count)
	}
	wantNames := []string{"kenaz__ask_user_question", "kenaz__bash", "kenaz__web_search"} // sorted
	if len(rec.Tools) != len(wantNames) {
		t.Fatalf("tools = %v, want %v", rec.Tools, wantNames)
	}
	for i, want := range wantNames {
		if rec.Tools[i] != want {
			t.Errorf("tools[%d] = %q, want %q (names must be sorted)", i, rec.Tools[i], want)
		}
	}
}

// TestGenerate_LogsEmptyToolCatalog asserts the log line still fires (with
// count 0 and an empty names list) when no tools are attached, so the
// absence of tools is visible in the log rather than silently omitted.
func TestGenerate_LogsEmptyToolCatalog(t *testing.T) {
	reg := &usageStubRegistry{stream: &usageStubStream{final: corellm.Response{FinishReason: "stop"}}}
	adapter := NewLLMProviderAdapter(reg, "p-openrouter", "z-ai/glm-5.2", nil, nil).
		WithSessionID("sess-empty")

	logs := captureChatLog(t, func() {
		if _, err := adapter.Generate(context.Background(), coreag.LLMRequest{
			SystemPrompt: "base",
			Messages:     []coreag.Message{{Role: "user", Content: "hi"}},
		}); err != nil {
			t.Fatalf("Generate: %v", err)
		}
	})

	if !strings.Contains(logs, `"msg":"llm.request.tools"`) {
		t.Fatalf("expected llm.request.tools log line even with an empty catalog, got: %s", logs)
	}
	if !strings.Contains(logs, `"count":0`) {
		t.Errorf("expected count:0 for an empty tool catalog, got: %s", logs)
	}
}
