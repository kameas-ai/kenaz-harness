package ollama_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/llm"
	"github.com/kameas-ai/kenaz-harness/core/llm/genaitrace/spantest"
	"github.com/kameas-ai/kenaz-harness/core/llm/ollama"
)

// TestOllamaAdapter_GenAISpan_ExactAttributes is the Ollama row of the
// per-adapter gen_ai.chat table (llm-call-telemetry-01LLMTEL01 WP01).
//
// The adapter speaks Ollama's OpenAI-compatible /v1/chat/completions,
// which returns id / model / system_fingerprint and token counts but NOT
// the native /api/chat eval_count / eval_duration / prompt_eval_duration
// fields, so those are absent from the span (reading them would need the
// native endpoint — a different request, not an extra on this one).
func TestOllamaAdapter_GenAISpan_ExactAttributes(t *testing.T) {
	rec := spantest.Record(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		meta := `"id":"chatcmpl-412","model":"llama3.1","system_fingerprint":"fp_ollama"`
		frames := []string{
			`{` + meta + `,"choices":[{"index":0,"delta":{"content":"` + spantest.CompletionText + `"},"finish_reason":null}]}`,
			`{` + meta + `,"choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":100,"completion_tokens":7}}`,
			`[DONE]`,
		}
		for _, f := range frames {
			fmt.Fprintf(w, "data: %s\n\n", f)
		}
	}))
	t.Cleanup(srv.Close)

	a := ollama.New(ollama.WithHTTPClient(srv.Client()))
	prof := llm.ProviderProfile{ID: "p", Kind: ollama.Kind, Model: "llama3.1", Endpoint: srv.URL}
	resp, span := spantest.Drive(t, rec, a, spantest.Request("p"), prof, nil, spantest.ProviderCost)

	if resp.Meta == nil || resp.Meta.ResponseID != "chatcmpl-412" {
		t.Fatalf("Response.Meta = %+v, want ResponseID chatcmpl-412", resp.Meta)
	}
	spantest.AssertExact(t, span, spantest.With(spantest.BaseWant("ollama", "llama3.1"), map[string]any{
		"gen_ai.usage.input_tokens":         int64(100),
		"gen_ai.usage.output_tokens":        int64(7),
		"gen_ai.response.finish_reasons":    []string{"stop"},
		"gen_ai.response.model":             "llama3.1",
		"kenaz.provider.system_fingerprint": "fp_ollama",
	}), spantest.Content...)
}
