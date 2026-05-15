package gemini

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	llm "github.com/sigil-tech/kaneaz-harness/core/llm"
)

// loadGolden reads a JSON fixture from testdata/wire/<name>.
func loadGolden(t *testing.T, name string) []byte {
	t.Helper()
	path := filepath.Join("testdata", "wire", name)
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("loadGolden(%q): %v", name, err)
	}
	return b
}

// normalizeJSON round-trips through json.Unmarshal / json.Marshal to
// produce a canonical (deterministic) JSON representation, stripping
// any insignificant whitespace differences.
func normalizeJSON(t *testing.T, data []byte) []byte {
	t.Helper()
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		t.Fatalf("normalizeJSON: unmarshal: %v\ninput: %s", err, data)
	}
	out, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("normalizeJSON: marshal: %v", err)
	}
	return out
}

// TestToGeminiContents_SimpleText verifies a single user text message
// is translated to a single geminiContent with role "user".
func TestToGeminiContents_SimpleText(t *testing.T) {
	t.Parallel()
	req := llm.GenerationRequest{
		Messages: []llm.Message{
			llm.NewTextMessage(llm.RoleUser, "Hello, Gemini!"),
		},
	}
	gr, err := ToGeminiRequest(req, llm.ProviderProfile{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gr.SystemInstruction != nil {
		t.Error("expected no system instruction")
	}
	if len(gr.Contents) != 1 {
		t.Fatalf("want 1 content, got %d", len(gr.Contents))
	}
	if gr.Contents[0].Role != "user" {
		t.Errorf("want role=user, got %q", gr.Contents[0].Role)
	}
	if len(gr.Contents[0].Parts) != 1 || gr.Contents[0].Parts[0].Text != "Hello, Gemini!" {
		t.Errorf("unexpected parts: %+v", gr.Contents[0].Parts)
	}

	// Round-trip to golden JSON.
	got, err := json.Marshal(gr)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	want := loadGolden(t, "simple_text_request.json")
	if string(normalizeJSON(t, got)) != string(normalizeJSON(t, want)) {
		t.Errorf("JSON mismatch\ngot:  %s\nwant: %s", got, want)
	}
}

// TestToGeminiContents_SystemPrompt verifies that the system prompt
// goes into systemInstruction, not into contents.
func TestToGeminiContents_SystemPrompt(t *testing.T) {
	t.Parallel()
	req := llm.GenerationRequest{
		System: "You are a helpful assistant.",
		Messages: []llm.Message{
			llm.NewTextMessage(llm.RoleUser, "What is 2+2?"),
		},
	}
	gr, err := ToGeminiRequest(req, llm.ProviderProfile{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gr.SystemInstruction == nil {
		t.Fatal("expected system instruction, got nil")
	}
	if len(gr.SystemInstruction.Parts) != 1 || gr.SystemInstruction.Parts[0].Text != "You are a helpful assistant." {
		t.Errorf("unexpected system instruction: %+v", gr.SystemInstruction)
	}
	if gr.SystemInstruction.Role != "" {
		t.Errorf("system instruction should not have a role, got %q", gr.SystemInstruction.Role)
	}
	if len(gr.Contents) != 1 {
		t.Fatalf("want 1 content, got %d", len(gr.Contents))
	}

	got, err := json.Marshal(gr)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	want := loadGolden(t, "system_prompt_request.json")
	if string(normalizeJSON(t, got)) != string(normalizeJSON(t, want)) {
		t.Errorf("JSON mismatch\ngot:  %s\nwant: %s", got, want)
	}
}

// TestToGeminiContents_MultiTurn verifies assistant role → "model"
// and multi-turn conversation translation.
func TestToGeminiContents_MultiTurn(t *testing.T) {
	t.Parallel()
	req := llm.GenerationRequest{
		Messages: []llm.Message{
			llm.NewTextMessage(llm.RoleUser, "What is the capital of France?"),
			llm.NewTextMessage(llm.RoleAssistant, "The capital of France is Paris."),
			llm.NewTextMessage(llm.RoleUser, "What is its population?"),
		},
	}
	gr, err := ToGeminiRequest(req, llm.ProviderProfile{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(gr.Contents) != 3 {
		t.Fatalf("want 3 contents, got %d", len(gr.Contents))
	}
	if gr.Contents[1].Role != "model" {
		t.Errorf("assistant→model: got %q", gr.Contents[1].Role)
	}

	got, err := json.Marshal(gr)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	want := loadGolden(t, "multiturn_request.json")
	if string(normalizeJSON(t, got)) != string(normalizeJSON(t, want)) {
		t.Errorf("JSON mismatch\ngot:  %s\nwant: %s", got, want)
	}
}

// TestFromGeminiResponse_SimpleText verifies a text response golden.
func TestFromGeminiResponse_SimpleText(t *testing.T) {
	t.Parallel()
	raw := loadGolden(t, "simple_text_response.json")
	var gr geminiResponse
	if err := json.Unmarshal(raw, &gr); err != nil {
		t.Fatalf("unmarshal golden: %v", err)
	}
	resp, err := FromGeminiResponse(gr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.Content) != 1 || resp.Content[0].Text != "Hello! How can I help you today?" {
		t.Errorf("unexpected content: %+v", resp.Content)
	}
	if resp.FinishReason != "stop" {
		t.Errorf("want finish_reason=stop, got %q", resp.FinishReason)
	}
	if resp.Usage.InputTokens != 5 || resp.Usage.OutputTokens != 9 {
		t.Errorf("unexpected usage: %+v", resp.Usage)
	}
}

// TestFromGeminiResponse_ToolCall verifies function calls get synthesised IDs.
func TestFromGeminiResponse_ToolCall(t *testing.T) {
	t.Parallel()
	raw := loadGolden(t, "tool_call_response.json")
	var gr geminiResponse
	if err := json.Unmarshal(raw, &gr); err != nil {
		t.Fatalf("unmarshal golden: %v", err)
	}
	resp, err := FromGeminiResponse(gr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("want 1 tool call, got %d", len(resp.ToolCalls))
	}
	tc := resp.ToolCalls[0]
	if tc.ID != "call_0" {
		t.Errorf("want synthesised id=call_0, got %q", tc.ID)
	}
	if tc.Name != "get_weather" {
		t.Errorf("want name=get_weather, got %q", tc.Name)
	}
}

// TestUnsupportedMediaType verifies that an unknown image MIME type
// returns ErrInvalidRequest.
func TestUnsupportedMediaType(t *testing.T) {
	t.Parallel()
	req := llm.GenerationRequest{
		Messages: []llm.Message{
			{
				Role: llm.RoleUser,
				Content: []llm.ContentBlock{
					{
						Type: "image",
						Source: &llm.MediaSource{
							Kind:      "base64",
							MediaType: "image/bmp", // unsupported
							Data:      "AAAA",
						},
					},
				},
			},
		},
	}
	_, err := ToGeminiRequest(req, llm.ProviderProfile{})
	if err == nil {
		t.Fatal("expected error for unsupported MIME type, got nil")
	}
	var invalidReq *llm.ErrInvalidRequest
	if !errors.As(err, &invalidReq) {
		t.Errorf("want *llm.ErrInvalidRequest, got %T: %v", err, err)
	}
}

// TestReasoningConfig verifies thinkingConfig is emitted for Gemini 2.5
// models when Reasoning.Enabled=true.
func TestReasoningConfig(t *testing.T) {
	t.Parallel()
	req := llm.GenerationRequest{
		Messages: []llm.Message{
			llm.NewTextMessage(llm.RoleUser, "Solve this step-by-step."),
		},
		Reasoning: &llm.ReasoningSpec{
			Enabled:      true,
			BudgetTokens: 4000,
		},
	}
	gr, err := ToGeminiRequest(req, llm.ProviderProfile{Model: "gemini-2.5-pro-preview-04-09"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gr.GenerationConfig == nil {
		t.Fatal("expected generationConfig")
	}
	if gr.GenerationConfig.ThinkingConfig == nil {
		t.Fatal("expected thinkingConfig")
	}
	if gr.GenerationConfig.ThinkingConfig.ThinkingBudget != 4000 {
		t.Errorf("want thinkingBudget=4000, got %d", gr.GenerationConfig.ThinkingConfig.ThinkingBudget)
	}
}

// TestReasoningDisabled verifies no thinkingConfig when reasoning is off.
func TestReasoningDisabled(t *testing.T) {
	t.Parallel()
	req := llm.GenerationRequest{
		Messages: []llm.Message{
			llm.NewTextMessage(llm.RoleUser, "Hello"),
		},
	}
	gr, err := ToGeminiRequest(req, llm.ProviderProfile{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gr.GenerationConfig != nil && gr.GenerationConfig.ThinkingConfig != nil {
		t.Errorf("unexpected thinkingConfig: %+v", gr.GenerationConfig.ThinkingConfig)
	}
}
