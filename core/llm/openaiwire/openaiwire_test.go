package openaiwire_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/llm"
	"github.com/kameas-ai/kenaz-harness/core/llm/openaiwire"
)

// TestSerializeTools_RoundTrip verifies that a ToolSpec array round-trips
// through SerializeTools into the OpenAI tools wire shape and back.
// Locks contract referenced as #4185933 in the tasks.
func TestSerializeTools_RoundTrip(t *testing.T) {
	specs := []llm.ToolSpec{
		{
			Name:        "get_weather",
			Description: "Get the current weather",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"location":{"type":"string"}}}`),
		},
		{
			Name:        "search",
			Description: "Search the web",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"query":{"type":"string"}}}`),
		},
	}

	tools, err := openaiwire.SerializeTools(specs)
	if err != nil {
		t.Fatalf("SerializeTools: %v", err)
	}
	if len(tools) != 2 {
		t.Fatalf("expected 2 tools, got %d", len(tools))
	}

	for i, spec := range specs {
		tool := tools[i]
		if got, ok := tool["type"]; !ok || got != "function" {
			t.Errorf("tools[%d].type = %v, want function", i, got)
		}
		fn, ok := tool["function"].(map[string]any)
		if !ok {
			t.Fatalf("tools[%d].function is not a map", i)
		}
		if fn["name"] != spec.Name {
			t.Errorf("tools[%d].function.name = %v, want %s", i, fn["name"], spec.Name)
		}
		if fn["description"] != spec.Description {
			t.Errorf("tools[%d].function.description = %v, want %s", i, fn["description"], spec.Description)
		}
		if fn["parameters"] == nil {
			t.Errorf("tools[%d].function.parameters is nil", i)
		}
	}
}

// TestToolCallAccumulator_DeltaAggregation verifies that streaming
// tool_calls deltas are aggregated correctly across frames.
// Locks contract referenced as #2c710ae in the tasks.
func TestToolCallAccumulator_DeltaAggregation(t *testing.T) {
	acc := &openaiwire.ToolCallAccumulator{}
	events := make(chan llm.StreamEvent, 10)

	// Simulate three frames building one tool call.
	acc.ApplyDelta(0, "call_abc123", "get_weather", "")
	acc.ApplyDelta(0, "", "", `{"loc`)
	acc.ApplyDelta(0, "", "", `ation":"NYC"}`)

	acc.FlushLocked(events)
	close(events)

	var toolEvents []llm.StreamEvent
	for e := range events {
		if e.Kind == llm.StreamTool {
			toolEvents = append(toolEvents, e)
		}
	}
	if len(toolEvents) != 1 {
		t.Fatalf("expected 1 StreamTool event, got %d", len(toolEvents))
	}
	tool := toolEvents[0].Tool
	if tool == nil {
		t.Fatal("StreamTool.Tool is nil")
	}
	if tool.ID != "call_abc123" {
		t.Errorf("tool.ID = %q, want call_abc123", tool.ID)
	}
	if tool.Name != "get_weather" {
		t.Errorf("tool.Name = %q, want get_weather", tool.Name)
	}
	var input map[string]string
	if err := json.Unmarshal(tool.Input, &input); err != nil {
		t.Fatalf("tool.Input parse: %v", err)
	}
	if input["location"] != "NYC" {
		t.Errorf("input.location = %q, want NYC", input["location"])
	}
}

// TestToolCallAccumulator_ToolCallID_RoundTrip verifies that tool_call_id
// survives the delta accumulation and ends up in the final ToolUse.
// Locks contract referenced as #0ca7749 in the tasks.
func TestToolCallAccumulator_ToolCallID_RoundTrip(t *testing.T) {
	acc := &openaiwire.ToolCallAccumulator{}
	events := make(chan llm.StreamEvent, 10)

	acc.ApplyDelta(0, "call_XY99", "search", `{"query":"test"}`)
	acc.FlushLocked(events)
	close(events)

	calls := acc.ToolCalls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(calls))
	}
	if calls[0].ID != "call_XY99" {
		t.Errorf("ToolUse.ID = %q, want call_XY99", calls[0].ID)
	}
}

// TestBuildRequestBody_KnobSerialization verifies that every RequestKnobs
// field is correctly serialized to the OpenAI wire body.
func TestBuildRequestBody_KnobSerialization(t *testing.T) {
	seed := 42
	temp := 0.7
	topP := 0.9
	topK := 10
	freqPen := 0.5
	presPen := -0.2
	parallelTC := false
	jsonMode := true

	req := llm.GenerationRequest{
		ProfileID: "test",
		Model:     "gpt-4o",
		Messages:  []llm.Message{llm.NewTextMessage(llm.RoleUser, "hello")},
		Knobs: &llm.RequestKnobs{
			Seed:              &seed,
			Temperature:       &temp,
			TopP:              &topP,
			TopK:              &topK,
			FrequencyPenalty:  &freqPen,
			PresencePenalty:   &presPen,
			ParallelToolCalls: &parallelTC,
			Reasoning: &llm.ReasoningConfig{
				OpenAIEffort: "high",
			},
			JSONMode: &jsonMode,
		},
	}

	body, err := openaiwire.BuildRequestBody(req, "gpt-4o", nil)
	if err != nil {
		t.Fatalf("BuildRequestBody: %v", err)
	}

	checks := []struct {
		key  string
		want any
	}{
		{"seed", 42}, // stored as int from *k.Seed dereference
		{"temperature", 0.7},
		{"top_p", 0.9},
		{"frequency_penalty", 0.5},
		{"presence_penalty", -0.2},
		{"parallel_tool_calls", false},
		{"reasoning_effort", "high"},
	}
	for _, c := range checks {
		got, ok := body[c.key]
		if !ok {
			t.Errorf("body[%q] not set", c.key)
			continue
		}
		if got != c.want {
			t.Errorf("body[%q] = %v (%T), want %v (%T)", c.key, got, got, c.want, c.want)
		}
	}
	// top_k is NOT an OpenAI wire field; verify it is absent.
	if _, ok := body["top_k"]; ok {
		t.Error("body[top_k] should not be set for OpenAI wire")
	}
}

// TestBuildRequestBody_ToolsRoundTrip verifies the tools array is built
// correctly when tools are present.
func TestBuildRequestBody_ToolsRoundTrip(t *testing.T) {
	req := llm.GenerationRequest{
		ProfileID: "test",
		Model:     "gpt-4o",
		Messages:  []llm.Message{llm.NewTextMessage(llm.RoleUser, "hello")},
		Tools: []llm.ToolSpec{
			{
				Name:        "do_thing",
				Description: "does a thing",
				InputSchema: json.RawMessage(`{"type":"object"}`),
			},
		},
	}
	body, err := openaiwire.BuildRequestBody(req, "gpt-4o", nil)
	if err != nil {
		t.Fatalf("BuildRequestBody: %v", err)
	}
	tools, ok := body["tools"]
	if !ok {
		t.Fatal("body[tools] not set")
	}
	arr, ok := tools.([]map[string]any)
	if !ok {
		t.Fatalf("body[tools] is %T, want []map[string]any", tools)
	}
	if len(arr) != 1 {
		t.Fatalf("len(tools) = %d, want 1", len(arr))
	}
}

// TestParseFrame_ValidFrame verifies SSE frame parsing.
func TestParseFrame_ValidFrame(t *testing.T) {
	raw := []byte(`{"choices":[{"index":0,"delta":{"content":"hello"},"finish_reason":null}],"usage":null}`)
	frame, err := openaiwire.ParseFrame(raw)
	if err != nil {
		t.Fatalf("ParseFrame: %v", err)
	}
	if frame == nil {
		t.Fatal("frame is nil")
	}
	if len(frame.Choices) != 1 {
		t.Fatalf("len(choices) = %d, want 1", len(frame.Choices))
	}
	if frame.Choices[0].Delta.Content != "hello" {
		t.Errorf("delta.content = %q, want hello", frame.Choices[0].Delta.Content)
	}
}

// TestBuildFinalResponse_TextOnly verifies BuildFinalResponse with text only.
func TestBuildFinalResponse_TextOnly(t *testing.T) {
	var buf strings.Builder
	buf.WriteString("Hello, world!")
	usage := llm.Usage{InputTokens: 5, OutputTokens: 3}
	resp := openaiwire.BuildFinalResponse(&buf, nil, usage, "stop")

	if len(resp.Content) != 1 {
		t.Fatalf("len(content) = %d, want 1", len(resp.Content))
	}
	if resp.Content[0].Text != "Hello, world!" {
		t.Errorf("content[0].text = %q, want Hello, world!", resp.Content[0].Text)
	}
	if resp.FinishReason != "stop" {
		t.Errorf("finish_reason = %q, want stop", resp.FinishReason)
	}
	if resp.Usage.InputTokens != 5 {
		t.Errorf("usage.input_tokens = %d, want 5", resp.Usage.InputTokens)
	}
}
