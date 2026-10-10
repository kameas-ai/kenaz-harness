package openrouter_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/llm"
	"github.com/kameas-ai/kenaz-harness/core/llm/genaitrace/spantest"
	"github.com/kameas-ai/kenaz-harness/core/llm/openrouter"
)

// TestOpenRouterAdapter_GenAISpan_ExactAttributes is the OpenRouter row of
// the per-adapter gen_ai.chat table (llm-call-telemetry-01LLMTEL01 WP01).
//
// Pinned on purpose: gen_ai.response.id is OpenRouter's generation id
// ("gen-…"), the key Fleet joins OpenRouter's own per-generation records
// to this span on. Also mapped: the served model, the upstream provider
// OpenRouter routed to (normalised), the inline cache split and the
// inline usage.cost through the cost readout.
func TestOpenRouterAdapter_GenAISpan_ExactAttributes(t *testing.T) {
	rec := spantest.Record(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		meta := `"id":"gen-1760000000-AbCdEf123","model":"anthropic/claude-sonnet-4.5","provider":"Google AI Studio"`
		frames := []string{
			`{` + meta + `,"choices":[{"index":0,"delta":{"role":"assistant","content":"` + spantest.CompletionText + `"},"finish_reason":null}]}`,
			`{` + meta + `,"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
			`{` + meta + `,"choices":[],"usage":{"prompt_tokens":100,"completion_tokens":7,"total_tokens":107,"cost":0.0123,"prompt_tokens_details":{"cached_tokens":30,"cache_write_tokens":20}}}`,
			`[DONE]`,
		}
		for _, f := range frames {
			fmt.Fprintf(w, "data: %s\n\n", f)
		}
	}))
	t.Cleanup(srv.Close)

	a := openrouter.New(openrouter.WithEndpoint(srv.URL), openrouter.WithHTTPClient(srv.Client()))
	prof := llm.ProviderProfile{ID: "p", Kind: openrouter.Kind, Model: "anthropic/claude-sonnet-4.5"}
	resp, span := spantest.Drive(t, rec, a, spantest.Request("p"), prof, []byte("sk-or-test"), spantest.ProviderCost)

	if resp.Meta == nil || resp.Meta.ResponseID != "gen-1760000000-AbCdEf123" {
		t.Fatalf("Response.Meta = %+v, want the generation id as ResponseID", resp.Meta)
	}
	spantest.AssertExact(t, span, spantest.With(spantest.BaseWant("openrouter", "anthropic/claude-sonnet-4.5"), map[string]any{
		"gen_ai.usage.input_tokens":                int64(100),
		"gen_ai.usage.output_tokens":               int64(7),
		"gen_ai.usage.cache_read.input_tokens":     int64(30),
		"gen_ai.usage.cache_creation.input_tokens": int64(20),
		"gen_ai.response.finish_reasons":           []string{"stop"},
		"gen_ai.response.id":                       "gen-1760000000-AbCdEf123",
		"gen_ai.response.model":                    "anthropic/claude-sonnet-4.5",
		"kenaz.provider.upstream":                  "google-ai-studio",
		"kenaz.llm.cost_usd":                       0.0123,
	}), spantest.Content...)
}
