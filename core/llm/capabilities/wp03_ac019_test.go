package capabilities

import (
	"testing"

	llm "github.com/kameas-ai/kenaz-harness/core/llm"
	"github.com/kameas-ai/kenaz-harness/core/llm/openaiwire"
)

// TestAC019_CustomOpenAI_StructuredOutputRowsMatchEncoderBehaviour is
// AC-019 (model-settings-reach-the-model-01PMZ101, D-12, WP03): routing
// azure and custom through openaiwire.BuildRequestBody makes
// applyResponseFormat/applyJSONMode (body.go:81-90) live for those two
// adapters whether or not anyone asked. custom-openai.yaml's
// structured_output / json_mode rows must not lie about that — "a false
// where the key is emitted, or a true where it is not" (tasks.md WP03
// item 3).
//
// This file's own baseline sets both to false (WP02). The way that stays
// honest is structural, not incidental: CapStructuredOutput and
// CapJSONMode are both requested capabilities
// (llm.GenerationRequest.RequestedCapabilities), so the registry's gate
// (capabilities.Gate.Check) refuses a json_schema/JSONMode request for
// custom-openai *before* the adapter ever calls BuildRequestBody — the
// encoder's willingness to emit response_format is never reached. This
// test proves both halves: the encoder DOES emit the key when called
// directly (so a true row would be honest), and the gate refuses before
// that call happens for custom-openai's actual (false) row (so the false
// row is honest too).
func TestAC019_CustomOpenAI_StructuredOutputRowsMatchEncoderBehaviour(t *testing.T) {
	c := mustCatalog(t)

	// custom-openai.yaml's declared values (WP02).
	desc := c.Describe("custom-openai", "llama3.1")
	if desc.Has(llm.CapStructuredOutput) {
		t.Fatal("custom-openai.yaml structured_output = true; this test's gate-refusal half assumes false")
	}
	if desc.Has(llm.CapJSONMode) {
		t.Fatal("custom-openai.yaml json_mode = true; this test's gate-refusal half assumes false")
	}

	schemaReq := llm.GenerationRequest{
		ResponseFormat: &llm.ResponseFormat{Mode: "json_schema", Schema: []byte(`{"type":"object"}`)},
	}
	jsonModeReq := llm.GenerationRequest{
		JSONMode: &llm.JSONModeSpec{Enabled: true},
	}

	// Half 1: the gate refuses BEFORE any adapter would call
	// BuildRequestBody, for exactly the capabilities this file declares
	// false — so the encoder's behaviour below never actually executes
	// for a real custom-openai request.
	g := NewGate(c)
	prof := llm.ProviderProfile{Kind: "custom-openai", Model: "llama3.1"}
	if _, err := g.Check(schemaReq, prof); err == nil {
		t.Fatal("Gate.Check(json_schema) = nil; want refusal — custom-openai.yaml declares structured_output: false")
	}
	if _, err := g.Check(jsonModeReq, prof); err == nil {
		t.Fatal("Gate.Check(json_mode) = nil; want refusal — custom-openai.yaml declares json_mode: false")
	}

	// Half 2: if a caller bypassed the gate (or a future row flips either
	// flag to true), the shared encoder genuinely does emit the
	// response_format key for both request shapes — so a `true` row
	// would also be honest, not just a `false` one.
	schemaBody, err := openaiwire.BuildRequestBody(schemaReq, "llama3.1", nil)
	if err != nil {
		t.Fatalf("BuildRequestBody(json_schema): %v", err)
	}
	if _, ok := schemaBody["response_format"]; !ok {
		t.Error("BuildRequestBody(json_schema) did not emit response_format — a true structured_output row would be a lie")
	}
	jsonModeBody, err := openaiwire.BuildRequestBody(jsonModeReq, "llama3.1", nil)
	if err != nil {
		t.Fatalf("BuildRequestBody(json_mode): %v", err)
	}
	if _, ok := jsonModeBody["response_format"]; !ok {
		t.Error("BuildRequestBody(json_mode) did not emit response_format — a true json_mode row would be a lie")
	}
}
