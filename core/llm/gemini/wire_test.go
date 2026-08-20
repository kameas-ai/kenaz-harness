package gemini

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	llm "github.com/kameas-ai/kenaz-harness/core/llm"
	"github.com/kameas-ai/kenaz-harness/core/llm/capabilities"
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

// TestResponseFormat_JSONSchema_CarriesMimeTypeAndSchema is
// structured-output-is-reachable-01PMZE14 WP04's AC-004: before this
// fix, gemini/wire.go read req.JSONMode only and had no field at all
// for a schema (geminiGenConfig had eight fields, none a schema —
// spec §1.3/§5.2). Asserts Mode:"json_schema" produces BOTH the MIME
// type and the schema on the wire — asserting only responseMimeType
// would pass with today's (pre-fix) behaviour and the schema still
// dropped, which is exactly what this AC must not tolerate.
func TestResponseFormat_JSONSchema_CarriesMimeTypeAndSchema(t *testing.T) {
	t.Parallel()
	schema := json.RawMessage(`{"type":"object","properties":{"verdict":{"type":"string"}}}`)
	req := llm.GenerationRequest{
		Messages:       []llm.Message{llm.NewTextMessage(llm.RoleUser, "hi")},
		ResponseFormat: &llm.ResponseFormat{Mode: "json_schema", Schema: schema},
	}
	gr, err := ToGeminiRequest(req, llm.ProviderProfile{Model: "gemini-2.5-pro"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gr.GenerationConfig == nil {
		t.Fatal("expected generationConfig")
	}
	if gr.GenerationConfig.ResponseMimeType != "application/json" {
		t.Errorf("responseMimeType = %q, want application/json", gr.GenerationConfig.ResponseMimeType)
	}
	if len(gr.GenerationConfig.ResponseSchema) == 0 {
		t.Fatal("responseSchema is empty; the schema constraint did not reach the wire")
	}
	var got map[string]any
	if err := json.Unmarshal(gr.GenerationConfig.ResponseSchema, &got); err != nil {
		t.Fatalf("responseSchema is not valid JSON: %v", err)
	}
}

// TestResponseFormat_Json_MimeTypeOnlyNoSchema asserts Mode:"json"
// carries only the MIME type — matching every other adapter's
// unconstrained-JSON-mode behaviour.
func TestResponseFormat_Json_MimeTypeOnlyNoSchema(t *testing.T) {
	t.Parallel()
	req := llm.GenerationRequest{
		Messages:       []llm.Message{llm.NewTextMessage(llm.RoleUser, "hi")},
		ResponseFormat: &llm.ResponseFormat{Mode: "json"},
	}
	gr, err := ToGeminiRequest(req, llm.ProviderProfile{Model: "gemini-2.5-pro"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gr.GenerationConfig == nil || gr.GenerationConfig.ResponseMimeType != "application/json" {
		t.Fatalf("expected responseMimeType=application/json, got %+v", gr.GenerationConfig)
	}
	if len(gr.GenerationConfig.ResponseSchema) != 0 {
		t.Errorf("responseSchema = %s, want empty for Mode=json", gr.GenerationConfig.ResponseSchema)
	}
}

// TestResponseFormat_Grammar_ReturnsTypedError is AC-005: grammar mode
// must return *llm.ErrUnsupportedFormat, matching
// azure/adapter.go:496-497's shape. A nil/generic error would let an
// unconstrained request reach Gemini and be repaired into looking
// correct (registry.go:533's repair-once loop).
func TestResponseFormat_Grammar_ReturnsTypedError(t *testing.T) {
	t.Parallel()
	req := llm.GenerationRequest{
		Messages:       []llm.Message{llm.NewTextMessage(llm.RoleUser, "hi")},
		ResponseFormat: &llm.ResponseFormat{Mode: "grammar", Grammar: []byte("root ::= \"x\"")},
	}
	_, err := ToGeminiRequest(req, llm.ProviderProfile{Model: "gemini-2.5-pro"})
	if err == nil {
		t.Fatal("expected an error for grammar mode, got nil")
	}
	var uf *llm.ErrUnsupportedFormat
	if !errors.As(err, &uf) {
		t.Fatalf("expected *llm.ErrUnsupportedFormat, got %T: %v", err, err)
	}
	if uf.Provider != Kind || uf.Model != "gemini-2.5-pro" || uf.Mode != "grammar" {
		t.Errorf("ErrUnsupportedFormat = %+v, want Provider=%q Model=gemini-2.5-pro Mode=grammar", uf, Kind)
	}
}

// TestJSONModeSpec_Schema_NoLongerDiscarded is the other half of AC-004:
// before this fix, req.JSONMode.Schema was read nowhere — only
// JSONMode.Enabled gated the MIME type. spec §1.3's table: "gemini |
// partial — sets responseMimeType... and discards JSONModeSpec.Schema."
func TestJSONModeSpec_Schema_NoLongerDiscarded(t *testing.T) {
	t.Parallel()
	schema := json.RawMessage(`{"type":"object","properties":{"x":{"type":"string"}}}`)
	req := llm.GenerationRequest{
		Messages: []llm.Message{llm.NewTextMessage(llm.RoleUser, "hi")},
		JSONMode: &llm.JSONModeSpec{Enabled: true, Schema: schema},
	}
	gr, err := ToGeminiRequest(req, llm.ProviderProfile{Model: "gemini-2.5-pro"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(gr.GenerationConfig.ResponseSchema) == 0 {
		t.Fatal("JSONModeSpec.Schema was discarded — responseSchema is empty")
	}
}

// TestResponseFormat_RejectedKeyword_ProducesTypedError asserts a
// schema carrying a keyword Gemini's dialect does not support fails
// with a typed error naming the keyword, rather than silently dropping
// to responseMimeType alone (spec §5.2's explicit prohibition — that
// would be "the same lie in a new costume" via the repair-once loop).
func TestResponseFormat_RejectedKeyword_ProducesTypedError(t *testing.T) {
	t.Parallel()
	schema := json.RawMessage(`{"type":"object","additionalProperties":false}`)
	req := llm.GenerationRequest{
		Messages:       []llm.Message{llm.NewTextMessage(llm.RoleUser, "hi")},
		ResponseFormat: &llm.ResponseFormat{Mode: "json_schema", Schema: schema},
	}
	_, err := ToGeminiRequest(req, llm.ProviderProfile{Model: "gemini-2.5-pro"})
	if err == nil {
		t.Fatal("expected an error for a schema carrying additionalProperties, got nil")
	}
	var kw *ErrUnsupportedSchemaKeyword
	if !errors.As(err, &kw) {
		t.Fatalf("expected *ErrUnsupportedSchemaKeyword, got %T: %v", err, err)
	}
	if kw.Keyword != "additionalProperties" {
		t.Errorf("Keyword = %q, want additionalProperties", kw.Keyword)
	}
}

// TestStructuredOutputRow_MatchesAdapterBehavior is WP05's AC-006/D-2:
// FR-002 (the adapter arm) and FR-003 (the capability rows) are one
// unit and must agree. Loaded through the REAL production capability
// data (capabilities.LoadDefault(), not a hand-rolled fixture) so a
// gemini.yaml edit that drifts from the adapter is caught here, not
// discovered by a user. For every gemini model: a structured_output:
// true row must both (a) pass Gate.Check for a json_schema request and
// (b) have the adapter actually emit responseSchema on the wire; a
// false row must have Gate.Check refuse the same request before the
// adapter is ever reached.
func TestStructuredOutputRow_MatchesAdapterBehavior(t *testing.T) {
	t.Parallel()
	cat, err := capabilities.LoadDefault()
	if err != nil {
		t.Fatalf("capabilities.LoadDefault: %v", err)
	}
	gate := capabilities.NewGate(cat)
	schema := json.RawMessage(`{"type":"object","properties":{"verdict":{"type":"string"}}}`)

	models := []string{
		"gemini-2.5-pro", "gemini-2.5-flash",
		"gemini-2.0-flash", "gemini-2.0-pro",
		"gemini-1.5-pro", "gemini-1.5-flash",
		"gemini-1.0-pro",
	}
	for _, model := range models {
		t.Run(model, func(t *testing.T) {
			prof := llm.ProviderProfile{Kind: Kind, Model: model}
			req := llm.GenerationRequest{
				Messages:       []llm.Message{llm.NewTextMessage(llm.RoleUser, "hi")},
				ResponseFormat: &llm.ResponseFormat{Mode: "json_schema", Schema: schema},
			}
			desc, gateErr := gate.Check(req, prof)
			rowSaysTrue := desc.Has(llm.CapStructuredOutput)

			gr, wireErr := ToGeminiRequest(req, prof)

			if rowSaysTrue {
				if gateErr != nil {
					t.Fatalf("row says structured_output:true but Gate.Check refused: %v — coverage_registry.yaml and gemini.yaml disagree", gateErr)
				}
				if wireErr != nil {
					t.Fatalf("row says structured_output:true but the adapter failed to translate the schema: %v", wireErr)
				}
				if gr.GenerationConfig == nil || len(gr.GenerationConfig.ResponseSchema) == 0 {
					t.Fatalf("row says structured_output:true but the adapter did not emit responseSchema on the wire — the row and the adapter disagree, coverage_registry.yaml's live defect class (spec §1.8)")
				}
			} else {
				var ce *llm.ErrCapabilityUnsupported
				if !errors.As(gateErr, &ce) {
					t.Fatalf("row says structured_output:false but Gate.Check did not refuse (got %v) — a request would reach the wire unconstrained and the repair-once loop would paper over it", gateErr)
				}
			}
		})
	}
}

// TestResponseFormat_RejectedKeyword_Nested asserts the keyword scan is
// recursive, not top-level-only — the class of bug a shallow check
// would miss (a $ref nested under "properties").
func TestResponseFormat_RejectedKeyword_Nested(t *testing.T) {
	t.Parallel()
	schema := json.RawMessage(`{"type":"object","properties":{"nested":{"$ref":"#/defs/thing"}}}`)
	req := llm.GenerationRequest{
		Messages:       []llm.Message{llm.NewTextMessage(llm.RoleUser, "hi")},
		ResponseFormat: &llm.ResponseFormat{Mode: "json_schema", Schema: schema},
	}
	_, err := ToGeminiRequest(req, llm.ProviderProfile{Model: "gemini-2.5-pro"})
	var kw *ErrUnsupportedSchemaKeyword
	if !errors.As(err, &kw) || kw.Keyword != "$ref" {
		t.Fatalf("expected *ErrUnsupportedSchemaKeyword{Keyword:\"$ref\"}, got %v", err)
	}
}
