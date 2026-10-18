package anthropic_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/llm"
	"github.com/kameas-ai/kenaz-harness/core/llm/anthropic"
	"github.com/kameas-ai/kenaz-harness/core/llm/genaitrace/spantest"
)

// TestAnthropicAdapter_GenAISpan_ExactAttributes is the Anthropic row of
// the per-adapter gen_ai.chat table (llm-call-telemetry-01LLMTEL01 WP01):
// a recorded Messages stream maps to exactly this attribute set — the
// message id, served model, request-id header and the cache split — and
// none of the request's or response's content.
func TestAnthropicAdapter_GenAISpan_ExactAttributes(t *testing.T) {
	rec := spantest.Record(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("request-id", "req_011CAnthropic")
		frames := []string{
			`{"type":"message_start","message":{"id":"msg_01XYZ","model":"claude-sonnet-4-5-20250929","usage":{"input_tokens":100,"output_tokens":1,"cache_creation_input_tokens":20,"cache_read_input_tokens":30}}}`,
			`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
			`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"` + spantest.CompletionText + `"}}`,
			`{"type":"content_block_stop","index":0}`,
			`{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"toolu_1","name":"search","input":{}}}`,
			`{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"q\":\"` + spantest.ToolArgsText + `\"}"}}`,
			`{"type":"content_block_stop","index":1}`,
			`{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":7}}`,
			`{"type":"message_stop"}`,
		}
		for _, f := range frames {
			fmt.Fprintf(w, "data: %s\n\n", f)
		}
	}))
	t.Cleanup(srv.Close)

	a := anthropic.New(anthropic.WithEndpoint(srv.URL), anthropic.WithHTTPClient(srv.Client()))
	prof := llm.ProviderProfile{ID: "p", Kind: anthropic.Kind, Model: "claude-sonnet-4-5"}
	resp, span := spantest.Drive(t, rec, a, spantest.Request("p"), prof, []byte("sk-ant-test"), spantest.ProviderCost)

	if resp.Meta == nil || resp.Meta.ResponseID != "msg_01XYZ" {
		t.Fatalf("Response.Meta = %+v, want ResponseID msg_01XYZ", resp.Meta)
	}
	spantest.AssertExact(t, span, spantest.With(spantest.BaseWant("anthropic", "claude-sonnet-4-5"), map[string]any{
		"gen_ai.usage.input_tokens":                int64(100),
		"gen_ai.usage.output_tokens":               int64(7),
		"gen_ai.usage.cache_read.input_tokens":     int64(30),
		"gen_ai.usage.cache_creation.input_tokens": int64(20),
		"gen_ai.response.finish_reasons":           []string{"tool_use"},
		"gen_ai.response.model":                    "claude-sonnet-4-5-20250929",
	}), spantest.Content...)
}
