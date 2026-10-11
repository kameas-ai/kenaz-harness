package azure_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/llm"
	"github.com/kameas-ai/kenaz-harness/core/llm/azure"
	"github.com/kameas-ai/kenaz-harness/core/llm/genaitrace/spantest"
)

// TestAzureAdapter_GenAISpan_ExactAttributes is the Azure OpenAI row of
// the per-adapter gen_ai.chat table (llm-call-telemetry-01LLMTEL01 WP01):
// id, served model, system_fingerprint, the apim-request-id header and
// cached tokens map; no content does.
func TestAzureAdapter_GenAISpan_ExactAttributes(t *testing.T) {
	rec := spantest.Record(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("apim-request-id", "4b1e2d3c-apim")
		meta := `"id":"chatcmpl-AZ9","model":"gpt-4o-2024-11-20","system_fingerprint":"fp_azure1"`
		frames := []string{
			`{` + meta + `,"choices":[{"index":0,"delta":{"content":"` + spantest.CompletionText + `"},"finish_reason":null}]}`,
			`{` + meta + `,"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
			`{` + meta + `,"choices":[],"usage":{"prompt_tokens":100,"completion_tokens":7,"prompt_tokens_details":{"cached_tokens":50}}}`,
			`[DONE]`,
		}
		for _, f := range frames {
			fmt.Fprintf(w, "data: %s\n\n", f)
		}
	}))
	t.Cleanup(srv.Close)

	a := azure.New(azure.WithHTTPClient(srv.Client()))
	prof := llm.ProviderProfile{
		ID: "p", Kind: azure.Kind, Model: "gpt-4o",
		Endpoint: srv.URL + "/openai/deployments/dep/chat/completions",
	}
	resp, span := spantest.Drive(t, rec, a, spantest.Request("p"), prof, []byte("azure-key"), spantest.ProviderCost)

	if resp.Meta == nil || resp.Meta.RequestID != "4b1e2d3c-apim" {
		t.Fatalf("Response.Meta = %+v, want RequestID from apim-request-id", resp.Meta)
	}
	spantest.AssertExact(t, span, spantest.With(spantest.BaseWant("azure.ai.openai", "gpt-4o"), map[string]any{
		"gen_ai.usage.input_tokens":            int64(100),
		"gen_ai.usage.output_tokens":           int64(7),
		"gen_ai.usage.cache_read.input_tokens": int64(50),
		"gen_ai.response.finish_reasons":       []string{"stop"},
		"gen_ai.response.model":                "gpt-4o-2024-11-20",
		"kenaz.provider.system_fingerprint":    "fp_azure1",
		"kenaz.provider.request_id":            "4b1e2d3c-apim",
	}), spantest.Content...)
}
