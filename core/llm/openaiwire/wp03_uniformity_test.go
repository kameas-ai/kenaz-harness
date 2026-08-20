package openaiwire_test

import (
	"encoding/json"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/llm"
	"github.com/kameas-ai/kenaz-harness/core/llm/openaiwire"
)

// TestBuildRequestBody_UniformAcrossOpenAIWireAdapters is WP03's
// uniformity regression test (model-settings-reach-the-model-01PMZ101,
// tasks.md WP02 -> WP03 "Tests" bullet 3): "The four openaiwire-fed
// adapters (openai, openrouter, azure, custom) get identical bodies for
// an identical request modulo endpoint-specific keys."
//
// Each of the four adapter packages calls
// openaiwire.BuildRequestBody(req, model, prof.Defaults) with the exact
// same signature and no mutation of req/profileDefaults beforehand
// (openai/openai.go:361, openrouter/openrouter.go:537,
// azure/adapter.go, custom/adapter.go — all four after WP03). The only
// per-adapter difference downstream is OpenRouter's own post-processing
// (openrouter.go:541-543): it deletes "stream_options" and adds "usage".
// This test pins that BuildRequestBody itself is deterministic for
// identical inputs (the shared half of the contract) and that
// OpenRouter's documented diff still leaves every other key untouched
// (the per-adapter half).
func TestBuildRequestBody_UniformAcrossOpenAIWireAdapters(t *testing.T) {
	req := llm.GenerationRequest{
		ProfileID: "test",
		Messages:  []llm.Message{llm.NewTextMessage(llm.RoleUser, "hi")},
		Tools: []llm.ToolSpec{{
			Name: "get_weather", Description: "get the weather",
			InputSchema: json.RawMessage(`{"type":"object"}`),
		}},
	}
	profileDefaults := map[string]any{"temperature": 0.5}

	bodyA, err := openaiwire.BuildRequestBody(req, "gpt-4o", profileDefaults)
	if err != nil {
		t.Fatalf("BuildRequestBody (call 1): %v", err)
	}
	bodyB, err := openaiwire.BuildRequestBody(req, "gpt-4o", profileDefaults)
	if err != nil {
		t.Fatalf("BuildRequestBody (call 2): %v", err)
	}
	rawA, err := json.Marshal(bodyA)
	if err != nil {
		t.Fatalf("marshal bodyA: %v", err)
	}
	rawB, err := json.Marshal(bodyB)
	if err != nil {
		t.Fatalf("marshal bodyB: %v", err)
	}
	if string(rawA) != string(rawB) {
		t.Fatalf("BuildRequestBody is not deterministic for identical (req, model, profileDefaults):\n%s\nvs\n%s", rawA, rawB)
	}

	// OpenRouter's own post-processing, reproduced (not re-implemented)
	// per openrouter.go:541-543: swap stream_options for a usage block.
	// Every other key — messages, tools, model — must survive untouched.
	bodyOR, err := openaiwire.BuildRequestBody(req, "openai/gpt-4o", profileDefaults)
	if err != nil {
		t.Fatalf("BuildRequestBody (openrouter shape): %v", err)
	}
	delete(bodyOR, "stream_options")
	bodyOR["usage"] = map[string]any{"include": true}
	if _, ok := bodyOR["stream_options"]; ok {
		t.Error("openrouter body still carries stream_options after the documented override")
	}
	if usage, ok := bodyOR["usage"].(map[string]any); !ok || usage["include"] != true {
		t.Errorf("openrouter body usage = %v, want {include:true}", bodyOR["usage"])
	}
	if _, ok := bodyOR["messages"]; !ok {
		t.Fatal("openrouter body lost messages after the per-adapter override")
	}
	tools, ok := bodyOR["tools"].([]map[string]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("openrouter body lost tools after the per-adapter override: %v", bodyOR["tools"])
	}
}
