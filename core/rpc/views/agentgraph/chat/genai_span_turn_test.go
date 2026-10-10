package chat

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	coreag "github.com/kameas-ai/kenaz-harness/core/agentgraph"
	corellm "github.com/kameas-ai/kenaz-harness/core/llm"
	"github.com/kameas-ai/kenaz-harness/core/llm/genaitrace"
	"github.com/kameas-ai/kenaz-harness/core/llm/genaitrace/spantest"
	"github.com/kameas-ai/kenaz-harness/core/llm/registry"
)

// scriptedSpanAdapter is a race-safe provider adapter whose Stream calls
// play a fixed script: a transient failure, then a tool call, then text.
type scriptedSpanAdapter struct {
	mu    sync.Mutex
	calls int
}

func (a *scriptedSpanAdapter) Kind() string { return "openrouter" }
func (a *scriptedSpanAdapter) Capabilities(string) corellm.CapabilityDescriptor {
	return corellm.CapabilityDescriptor{Supported: map[corellm.Capability]bool{
		corellm.CapStreaming: true, corellm.CapToolCalling: true, corellm.CapUsageReporting: true,
	}}
}

func (a *scriptedSpanAdapter) Stream(context.Context, corellm.GenerationRequest, corellm.ProviderProfile, []byte) (corellm.Stream, error) {
	a.mu.Lock()
	a.calls++
	n := a.calls
	a.mu.Unlock()
	switch n {
	case 1: // the forced retry: a pre-stream 503
		return nil, &corellm.ErrTransient{Status: 503, Message: "upstream overloaded"}
	case 2: // tool call
		tu := corellm.ToolUse{ID: "call_1", Name: "search__web", Input: json.RawMessage(`{"q":"` + spantest.ToolArgsText + `"}`)}
		return newSpanFixtureStream([]corellm.StreamEvent{{Kind: corellm.StreamTool, Tool: &tu}},
			corellm.Response{ToolCalls: []corellm.ToolUse{tu}, FinishReason: "tool_calls",
				Usage: corellm.Usage{InputTokens: 100, OutputTokens: 9},
				Meta:  &corellm.CallMeta{ResponseID: "gen-turn-1", ResponseModel: "openai/gpt-4o"}}), nil
	default: // final text
		return newSpanFixtureStream([]corellm.StreamEvent{{Kind: corellm.StreamText, Text: spantest.CompletionText}},
			corellm.Response{Content: []corellm.ContentBlock{corellm.ContentBlockFromText(spantest.CompletionText)},
				FinishReason: "stop", Usage: corellm.Usage{InputTokens: 140, OutputTokens: 5},
				Meta: &corellm.CallMeta{ResponseID: "gen-turn-2", ResponseModel: "openai/gpt-4o"}}), nil
	}
}

func (a *scriptedSpanAdapter) callCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.calls
}

type spanFixtureStream struct {
	ch   chan corellm.StreamEvent
	resp corellm.Response
}

func newSpanFixtureStream(evs []corellm.StreamEvent, resp corellm.Response) *spanFixtureStream {
	ch := make(chan corellm.StreamEvent, len(evs))
	for _, e := range evs {
		ch <- e
	}
	close(ch)
	return &spanFixtureStream{ch: ch, resp: resp}
}

func (s *spanFixtureStream) Events() <-chan corellm.StreamEvent { return s.ch }
func (s *spanFixtureStream) Cancel() error                      { return nil }
func (s *spanFixtureStream) Final() (corellm.Response, error)   { return s.resp, nil }

// TestChatTurn_GenAISpans_NestedWithRetryAndToolCall drives one real chat
// turn — the production chat graph, the real LLMProviderAdapter, the real
// registry pipeline with its retry middleware — against a scripted
// provider: a 503, then a tool call, then the final text. It pins the
// llm-call-telemetry-01LLMTEL01 WP01 span shape:
//
//   - exactly one kenaz.chat.turn span and three gen_ai.chat spans (the
//     failed attempt is a span of its own);
//   - every gen_ai.chat span is a child of the turn span, in its trace;
//   - the retry is attempt 1 of the first model request, with error.type
//     on attempt 0; the second model request starts again at attempt 0;
//   - no content anywhere.
func TestChatTurn_GenAISpans_NestedWithRetryAndToolCall(t *testing.T) {
	rec := spantest.Record(t)

	reg, err := registry.New(registry.Options{})
	if err != nil {
		t.Fatalf("registry.New: %v", err)
	}
	provider := &scriptedSpanAdapter{}
	reg.RegisterAdapter(provider)
	if err := reg.LoadProfiles([]corellm.ProviderProfile{{
		ID: "profile-1", Kind: "openrouter", Model: "openai/gpt-4o",
		Cred:  corellm.CredentialReference{Kind: "env", Locator: "KENAZ_TEST_UNUSED"},
		Retry: &corellm.RetryPolicy{MaxAttempts: 3, BaseMS: 1, MaxMS: 2, Jitter: "none"},
	}}); err != nil {
		t.Fatalf("LoadProfiles: %v", err)
	}

	tools := newStubTools("search__web")
	tools.push(coreag.ToolResult{Content: spantest.ToolResultText})
	broker := &recordingBroker{}
	graph := loadProductionChatGraph(t)
	runner, err := New(Config{
		Kernel:        coreag.NewKernel(),
		Registry:      reg,
		Broker:        broker,
		HistoryWriter: &recordingHistoryWriter{},
		History:       staticHistoryReader{msgs: []coreag.Message{{Role: "user", Content: spantest.PromptText}}},
		GraphLoader:   func() (coreag.Graph, error) { return graph, nil },
		MaxTurns:      func() int { return 25 },
		EnvDefaults:   func(env *coreag.Env) { env.Tools = tools },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := runner.StartStream(context.Background(), "profile-1", spantest.SessionID, "", testTurn(spantest.PromptText)); err != nil {
		t.Fatalf("StartStream: %v", err)
	}
	closed := waitForClosed(t, broker)
	if closed.Reason == "backend-error" {
		t.Fatalf("turn failed: %q", closed.Message)
	}
	if err := runner.Drain(context.Background()); err != nil {
		t.Fatalf("Drain: %v", err)
	}
	if got := provider.callCount(); got != 3 {
		t.Fatalf("provider calls = %d, want 3 (503, tool call, text)", got)
	}

	turns := spantest.Named(rec, genaitrace.SpanNameTurn)
	calls := spantest.Named(rec, genaitrace.SpanNameChat)
	if len(turns) != 1 || len(calls) != 3 {
		t.Fatalf("spans: %d %s + %d %s, want 1 + 3", len(turns), genaitrace.SpanNameTurn, len(calls), genaitrace.SpanNameChat)
	}
	turn := turns[0]
	spantest.AssertExact(t, turn, map[string]any{
		"kenaz.session.hash": genaitrace.SessionHash(spantest.SessionID),
	}, spantest.Content...)

	type want struct {
		attempt  int64
		errType  string
		finish   []string
		respID   string
		inTokens int64
	}
	wants := []want{
		{attempt: 0, errType: "provider_unavailable"},
		{attempt: 1, finish: []string{"tool_calls"}, respID: "gen-turn-1", inTokens: 100},
		{attempt: 0, finish: []string{"stop"}, respID: "gen-turn-2", inTokens: 140},
	}
	for i, s := range calls {
		w := wants[i]
		if s.Parent().SpanID() != turn.SpanContext().SpanID() || s.SpanContext().TraceID() != turn.SpanContext().TraceID() {
			t.Errorf("gen_ai.chat #%d is not a child of the turn span", i)
		}
		spantest.AssertAllowlisted(t, s, spantest.Content...)
		got := spantest.Attrs(s)
		if got["kenaz.llm.attempt"] != w.attempt {
			t.Errorf("gen_ai.chat #%d attempt = %v, want %d", i, got["kenaz.llm.attempt"], w.attempt)
		}
		if got["gen_ai.provider.name"] != "openrouter" || got["gen_ai.request.model"] != "openai/gpt-4o" ||
			got["kenaz.session.hash"] != genaitrace.SessionHash(spantest.SessionID) || got["kenaz.llm.fallback"] != false {
			t.Errorf("gen_ai.chat #%d request attrs = %v", i, got)
		}
		if w.errType != "" {
			if got["error.type"] != w.errType {
				t.Errorf("gen_ai.chat #%d error.type = %v, want %s", i, got["error.type"], w.errType)
			}
			if _, has := got["gen_ai.usage.input_tokens"]; has {
				t.Errorf("failed attempt #%d carries usage", i)
			}
			continue
		}
		if _, has := got["error.type"]; has {
			t.Errorf("gen_ai.chat #%d has error.type %v", i, got["error.type"])
		}
		if got["gen_ai.response.id"] != w.respID || got["gen_ai.usage.input_tokens"] != w.inTokens {
			t.Errorf("gen_ai.chat #%d response attrs = %v", i, got)
		}
		if fr, _ := got["gen_ai.response.finish_reasons"].([]string); len(fr) != 1 || fr[0] != w.finish[0] {
			t.Errorf("gen_ai.chat #%d finish_reasons = %v, want %v", i, fr, w.finish)
		}
	}
}
