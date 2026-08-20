package chat

// trust-surfaces-that-fire-01PMZ202 WP09 / UNIT-8, AC-06 / AC-06b.
//
// env.ToolSchemas (core/agentgraph/executor.go:376) was declared,
// documented ("Populated by the LLMProviderAdapter at StartStream time
// from the discovered ToolSpec slice"), and read at
// exec_dispatch.go:195-196 — but nothing ever wrote it, so schemaJSON was
// always nil and validateToolArgs only ever ran its parses-as-object
// check (tool_validation.go:45's `if len(schemaJSON) == 0 { return "" }`
// short-circuit). These tests exercise the real per-run wiring
// (chat_runner.go's toolSchemasFromSpecs, driven by a real
// ToolCatalogDiscoverer through StartStream) rather than hand-setting
// env.ToolSchemas in a literal.

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	coreag "github.com/kameas-ai/kenaz-harness/core/agentgraph"
	corellm "github.com/kameas-ai/kenaz-harness/core/llm"
)

// fakeToolDiscoverer is a ToolCatalogDiscoverer returning a fixed
// ToolSpec slice regardless of session id.
type fakeToolDiscoverer struct {
	specs []corellm.ToolSpec
}

func (f fakeToolDiscoverer) Tools(_ context.Context, _ string) ([]corellm.ToolSpec, error) {
	return f.specs, nil
}

// TestChatRunner_ToolSchemasReachEnv proves the discovered ToolSpec
// slice's InputSchema reaches env.ToolSchemas on a real StartStream —
// the wiring half of AC-06b.
//
// Mutation: comment out the `ToolSchemas: toolSchemasFromSpecs(toolCatalog)`
// line in chat_runner.go's Env literal. Confirmed by hand during
// development: with the line removed, this test failed with
// "env.ToolSchemas is nil"; restored, it passes.
func TestChatRunner_ToolSchemasReachEnv(t *testing.T) {
	t.Parallel()

	spec := corellm.ToolSpec{
		Name:        "get_weather",
		Description: "Get the current weather",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"location":{"type":"string"}},"required":["location"]}`),
	}

	llm := &stubLLM{}
	llm.push(stubLLMResponse{
		stream: []coreag.StreamEvent{{Kind: coreag.StreamEventText, Text: "hi"}},
		resp:   coreag.LLMResponse{Content: "hi", FinishReason: "stop"},
	})

	graph := loadProductionChatGraph(t)
	broker := &recordingBroker{}

	var mu sync.Mutex
	var seenSchemas map[string][]byte
	var envDefaultsRan bool

	runner, err := New(Config{
		Kernel:         coreag.NewKernel(),
		Registry:       stubRegistry{},
		Broker:         broker,
		HistoryWriter:  &recordingHistoryWriter{},
		History:        staticHistoryReader{msgs: []coreag.Message{{Role: "user", Content: "what's the weather"}}},
		GraphLoader:    func() (coreag.Graph, error) { return graph, nil },
		MaxTurns:       func() int { return 25 },
		ToolDiscoverer: fakeToolDiscoverer{specs: []corellm.ToolSpec{spec}},
		EnvDefaults: func(env *coreag.Env) {
			mu.Lock()
			envDefaultsRan = true
			seenSchemas = env.ToolSchemas
			mu.Unlock()
			env.LLM = llm
			env.Tools = newStubTools("get_weather")
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if _, err := runner.StartStream(context.Background(), "profile-1", "session-1", "", "what's the weather"); err != nil {
		t.Fatalf("StartStream: %v", err)
	}
	waitForClosed(t, broker)

	mu.Lock()
	defer mu.Unlock()
	if !envDefaultsRan {
		t.Fatal("EnvDefaults never ran — cannot observe env.ToolSchemas")
	}
	if seenSchemas == nil {
		t.Fatal("env.ToolSchemas is nil — the discovered ToolSpec slice never reached the Env")
	}
	got, ok := seenSchemas["get_weather"]
	if !ok {
		t.Fatalf("env.ToolSchemas has no entry for get_weather; got keys: %v", keysOf(seenSchemas))
	}
	var parsed map[string]any
	if err := json.Unmarshal(got, &parsed); err != nil {
		t.Fatalf("env.ToolSchemas[\"get_weather\"] is not valid JSON: %v (raw=%s)", err, got)
	}
	if req, ok := parsed["required"].([]any); !ok || len(req) != 1 || req[0] != "location" {
		t.Errorf("env.ToolSchemas[\"get_weather\"] required = %v, want [\"location\"]", parsed["required"])
	}
}

func keysOf(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// TestChatGraph_ToolSchemas_BouncesMissingRequiredArg drives a full
// production chat_default.yaml run where the model calls a tool omitting
// a schema-required argument. Asserts the tool implementation is NEVER
// invoked (exec_dispatch.go's validateToolArgs bounces the call with a
// self-correction message before dispatch) and the run still completes
// once the model's second turn supplies a valid call.
//
// Mutation: comment out `ToolSchemas: toolSchemasFromSpecs(toolCatalog)`
// in chat_runner.go. Confirmed by hand: with the mutation, this test's
// tool-call assertion flips — the FIRST (invalid) call reaches
// tools.Call, because validateToolArgs sees a nil schema and only checks
// well-formedness. Restored, it passes.
func TestChatGraph_ToolSchemas_BouncesMissingRequiredArg(t *testing.T) {
	t.Parallel()

	spec := corellm.ToolSpec{
		Name:        "get_weather",
		Description: "Get the current weather for a location",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"location":{"type":"string"}},"required":["location"]}`),
	}

	llm := &stubLLM{}
	// Turn 1: the model supplies a non-empty args object that omits the
	// required "location" property. Deliberately NOT `{}` —
	// validateToolArgs' step 1 treats an empty object as "no args, valid"
	// and returns before ever consulting the schema's "required" list, so
	// an empty-object args would vacuously pass regardless of whether
	// ToolSchemas is wired.
	llm.push(stubLLMResponse{
		stream: []coreag.StreamEvent{
			{Kind: coreag.StreamEventTool, ToolID: "tu-1", ToolName: "get_weather", ToolArgs: `{"units":"celsius"}`},
		},
		resp: coreag.LLMResponse{
			Content:      "",
			FinishReason: "tool_use",
			ToolCalls: []coreag.ToolCallRequest{
				{ID: "tu-1", Name: "get_weather", Arguments: `{"units":"celsius"}`},
			},
		},
	})
	// Turn 2: the model self-corrects with the required argument.
	llm.push(stubLLMResponse{
		stream: []coreag.StreamEvent{
			{Kind: coreag.StreamEventTool, ToolID: "tu-2", ToolName: "get_weather", ToolArgs: `{"location":"London"}`},
		},
		resp: coreag.LLMResponse{
			Content:      "",
			FinishReason: "tool_use",
			ToolCalls: []coreag.ToolCallRequest{
				{ID: "tu-2", Name: "get_weather", Arguments: `{"location":"London"}`},
			},
		},
	})
	// Turn 3: finish.
	llm.push(stubLLMResponse{
		stream: []coreag.StreamEvent{{Kind: coreag.StreamEventText, Text: "It is sunny in London."}},
		resp:   coreag.LLMResponse{Content: "It is sunny in London.", FinishReason: "stop"},
	})

	tools := newStubTools("get_weather")
	tools.push(coreag.ToolResult{Content: `{"forecast":"sunny"}`})

	broker := &recordingBroker{}
	writer := &recordingHistoryWriter{}
	graph := loadProductionChatGraph(t)

	runner, err := New(Config{
		Kernel:         coreag.NewKernel(),
		Registry:       stubRegistry{},
		Broker:         broker,
		HistoryWriter:  writer,
		History:        staticHistoryReader{msgs: []coreag.Message{{Role: "user", Content: "weather in london"}}},
		GraphLoader:    func() (coreag.Graph, error) { return graph, nil },
		MaxTurns:       func() int { return 25 },
		ToolDiscoverer: fakeToolDiscoverer{specs: []corellm.ToolSpec{spec}},
		EnvDefaults: func(env *coreag.Env) {
			env.LLM = llm
			env.Tools = tools
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if _, err := runner.StartStream(context.Background(), "profile-1", "session-1", "", "weather in london"); err != nil {
		t.Fatalf("StartStream: %v", err)
	}
	closed := waitForClosed(t, broker)
	if closed.Reason == "backend-error" {
		t.Fatalf("Reason = %q, want non-error; msg=%q", closed.Reason, closed.Message)
	}

	calls := tools.snapshotCalls()
	if len(calls) != 1 {
		t.Fatalf("len(tools.calls) = %d, want exactly 1 (only the corrected call should ever dispatch); calls=%+v", len(calls), calls)
	}
	if loc, _ := calls[0].Args["location"].(string); loc != "London" {
		t.Errorf("the one dispatched call had Args = %v, want location=London — the INVALID first call must never reach tools.Call", calls[0].Args)
	}

	// The model must have been called 3 times: invalid tool_use (bounced
	// without dispatch), corrected tool_use (dispatched), then finish.
	if got := llm.calls.Load(); got != 3 {
		t.Errorf("llm.calls = %d, want 3 (bounced turn + corrected turn + finish turn)", got)
	}
}
