package custom_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/llm"
	"github.com/kameas-ai/kenaz-harness/core/llm/custom"
	"github.com/kameas-ai/kenaz-harness/core/llm/genaitrace/spantest"
)

// TestCustomAdapter_GenAISpan_ExactAttributes is the custom
// OpenAI-compatible endpoint row of the per-adapter gen_ai.chat table
// (llm-call-telemetry-01LLMTEL01 WP01): whatever OpenAI-shaped metadata
// the server sends (here a vLLM-style id + model + x-request-id) maps;
// no content does.
func TestCustomAdapter_GenAISpan_ExactAttributes(t *testing.T) {
	rec := spantest.Record(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("x-request-id", "vllm-req-7")
		meta := `"id":"cmpl-vllm-7","model":"meta-llama/Llama-3.1-8B-Instruct"`
		frames := []string{
			`{` + meta + `,"choices":[{"index":0,"delta":{"content":"` + spantest.CompletionText + `"},"finish_reason":null}]}`,
			`{` + meta + `,"choices":[{"index":0,"delta":{},"finish_reason":"length"}],"usage":{"prompt_tokens":100,"completion_tokens":7}}`,
			`[DONE]`,
		}
		for _, f := range frames {
			fmt.Fprintf(w, "data: %s\n\n", f)
		}
	}))
	t.Cleanup(srv.Close)

	a := custom.New(custom.WithHTTPClient(srv.Client()))
	prof := llm.ProviderProfile{
		ID: "p", Kind: custom.Kind, Model: "llama-3.1-8b",
		Endpoint: srv.URL + "/v1",
		Defaults: map[string]any{"auth_scheme": "none"},
	}
	resp, span := spantest.Drive(t, rec, a, spantest.Request("p"), prof, nil, spantest.ProviderCost)

	if resp.Meta == nil || resp.Meta.ResponseID != "cmpl-vllm-7" {
		t.Fatalf("Response.Meta = %+v, want ResponseID cmpl-vllm-7", resp.Meta)
	}
	spantest.AssertExact(t, span, spantest.With(spantest.BaseWant("openai_compatible", "llama-3.1-8b"), map[string]any{
		"gen_ai.usage.input_tokens":      int64(100),
		"gen_ai.usage.output_tokens":     int64(7),
		"gen_ai.response.finish_reasons": []string{"length"},
		"gen_ai.response.id":             "cmpl-vllm-7",
		"gen_ai.response.model":          "meta-llama/Llama-3.1-8B-Instruct",
		"kenaz.provider.request_id":      "vllm-req-7",
	}), spantest.Content...)
}
