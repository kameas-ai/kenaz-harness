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
// Pinned on purpose: OpenRouter's generation id ("gen-…") is on the
// stored model-call record (response_id, which the generation-stats pull
// keys on) and ABSENT from the span — Fleet/counsel D32 (2026-10-10):
// OpenRouter's own traces carry the same id, so on a member-attributed
// span it would allow a join that needs legal review first. Also mapped:
// the served model, the upstream provider OpenRouter routed to
// (normalised), the inline cache split and the inline usage.cost through
// the cost readout.
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
	resp, span, stored := spantest.DriveRecorded(t, rec, a, spantest.Request("p"), prof, []byte("sk-or-test"), spantest.ProviderCost)

	const genID = "gen-1760000000-AbCdEf123"
	if resp.Meta == nil || resp.Meta.ResponseID != genID {
		t.Fatalf("Response.Meta = %+v, want the generation id as ResponseID", resp.Meta)
	}
	// The stored record carries the generation id (the stats pull's key)...
	if stored.ResponseID != genID || stored.Provider != "openrouter" || !stored.OK {
		t.Fatalf("stored record = %+v, want ResponseID %s on an ok openrouter record", stored, genID)
	}
	// ...and the span does not, under any key: AssertExact pins the full
	// key set below, and no value may contain the id.
	if _, onSpan := spantest.Attrs(span)[spantest.ResponseIDKey]; onSpan {
		t.Fatalf("span carries %s", spantest.ResponseIDKey)
	}
	spantest.AssertExact(t, span, spantest.With(spantest.BaseWant("openrouter", "anthropic/claude-sonnet-4.5"), map[string]any{
		"gen_ai.usage.input_tokens":                int64(100),
		"gen_ai.usage.output_tokens":               int64(7),
		"gen_ai.usage.cache_read.input_tokens":     int64(30),
		"gen_ai.usage.cache_creation.input_tokens": int64(20),
		"gen_ai.response.finish_reasons":           []string{"stop"},
		"gen_ai.response.model":                    "anthropic/claude-sonnet-4.5",
		"kenaz.provider.upstream":                  "google-ai-studio",
		"kenaz.llm.cost_usd":                       0.0123,
	}), append(append([]string(nil), spantest.Content...), genID)...)
}
