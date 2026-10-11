package openai_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/llm"
	"github.com/kameas-ai/kenaz-harness/core/llm/genaitrace/spantest"
	"github.com/kameas-ai/kenaz-harness/core/llm/openai"
)

// TestOpenAIAdapter_GenAISpan_ExactAttributes is the OpenAI row of the
// per-adapter gen_ai.chat table (llm-call-telemetry-01LLMTEL01 WP01): the
// chatcmpl id, served model, system_fingerprint, x-request-id header and
// prompt_tokens_details.cached_tokens map; no content does.
func TestOpenAIAdapter_GenAISpan_ExactAttributes(t *testing.T) {
	rec := spantest.Record(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("x-request-id", "req_openai_123")
		meta := `"id":"chatcmpl-AB12","model":"gpt-4o-2024-08-06","system_fingerprint":"fp_abc123"`
		frames := []string{
			`{` + meta + `,"choices":[{"index":0,"delta":{"role":"assistant","content":"` + spantest.CompletionText + `"},"finish_reason":null}]}`,
			`{` + meta + `,"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"search","arguments":"{\"q\":\"` + spantest.ToolArgsText + `\"}"}}]},"finish_reason":null}]}`,
			`{` + meta + `,"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
			`{` + meta + `,"choices":[],"usage":{"prompt_tokens":100,"completion_tokens":7,"total_tokens":107,"prompt_tokens_details":{"cached_tokens":64}}}`,
			`[DONE]`,
		}
		for _, f := range frames {
			fmt.Fprintf(w, "data: %s\n\n", f)
		}
	}))
	t.Cleanup(srv.Close)

	a := openai.New(openai.WithEndpoint(srv.URL), openai.WithHTTPClient(srv.Client()))
	prof := llm.ProviderProfile{ID: "p", Kind: openai.Kind, Model: "gpt-4o"}
	resp, span := spantest.Drive(t, rec, a, spantest.Request("p"), prof, []byte("sk-test"), spantest.ProviderCost)

	if resp.Meta == nil || resp.Meta.SystemFingerprint != "fp_abc123" {
		t.Fatalf("Response.Meta = %+v, want system fingerprint fp_abc123", resp.Meta)
	}
	spantest.AssertExact(t, span, spantest.With(spantest.BaseWant("openai", "gpt-4o"), map[string]any{
		"gen_ai.usage.input_tokens":            int64(100),
		"gen_ai.usage.output_tokens":           int64(7),
		"gen_ai.usage.cache_read.input_tokens": int64(64),
		"gen_ai.response.finish_reasons":       []string{"tool_calls"},
		"gen_ai.response.model":                "gpt-4o-2024-08-06",
		"kenaz.provider.system_fingerprint":    "fp_abc123",
		"kenaz.provider.request_id":            "req_openai_123",
	}), spantest.Content...)
}
