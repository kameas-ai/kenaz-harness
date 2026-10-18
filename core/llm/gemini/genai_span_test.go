package gemini_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/llm"
	"github.com/kameas-ai/kenaz-harness/core/llm/gemini"
	"github.com/kameas-ai/kenaz-harness/core/llm/genaitrace/spantest"
)

// redirect sends every request to the test server.
type redirect struct{ target *url.URL }

func (r redirect) RoundTrip(req *http.Request) (*http.Response, error) {
	req.URL.Scheme = r.target.Scheme
	req.URL.Host = r.target.Host
	return http.DefaultTransport.RoundTrip(req)
}

// TestGeminiAdapter_GenAISpan_ExactAttributes is the Gemini row of the
// per-adapter gen_ai.chat table (llm-call-telemetry-01LLMTEL01 WP01): the
// responseId, modelVersion and the usageMetadata fields (cached content,
// thoughts, tool-use prompt tokens) map; no content does.
func TestGeminiAdapter_GenAISpan_ExactAttributes(t *testing.T) {
	rec := spantest.Record(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		meta := `"responseId":"resp-GmN1","modelVersion":"gemini-2.5-flash-001"`
		frames := []string{
			`{` + meta + `,"candidates":[{"content":{"role":"model","parts":[{"text":"` + spantest.CompletionText + `"}]},"index":0}]}`,
			`{` + meta + `,"candidates":[{"content":{"role":"model","parts":[{"text":"."}]},"finishReason":"STOP","index":0}],` +
				`"usageMetadata":{"promptTokenCount":100,"candidatesTokenCount":7,"totalTokenCount":107,"cachedContentTokenCount":30,"thoughtsTokenCount":5,"toolUsePromptTokenCount":4}}`,
		}
		for _, f := range frames {
			fmt.Fprintf(w, "data: %s\n\n", f)
		}
	}))
	t.Cleanup(srv.Close)
	target, _ := url.Parse(srv.URL)

	a := gemini.New(gemini.WithHTTPClient(&http.Client{Transport: redirect{target: target}}))
	prof := llm.ProviderProfile{ID: "p", Kind: gemini.Kind, Model: "gemini-2.5-flash"}
	resp, span := spantest.Drive(t, rec, a, spantest.Request("p"), prof, []byte("AIza-test-key"), spantest.ProviderCost)

	if resp.Meta == nil || resp.Meta.ResponseID != "resp-GmN1" {
		t.Fatalf("Response.Meta = %+v, want ResponseID resp-GmN1", resp.Meta)
	}
	spantest.AssertExact(t, span, spantest.With(spantest.BaseWant("gcp.gemini", "gemini-2.5-flash"), map[string]any{
		"gen_ai.usage.input_tokens":             int64(100),
		"gen_ai.usage.output_tokens":            int64(7),
		"gen_ai.usage.cache_read.input_tokens":  int64(30),
		"kenaz.llm.reasoning_tokens":            int64(5),
		"kenaz.provider.tool_use_prompt_tokens": int64(4),
		"gen_ai.response.finish_reasons":        []string{"stop"},
		"gen_ai.response.model":                 "gemini-2.5-flash-001",
	}), spantest.Content...)
}
