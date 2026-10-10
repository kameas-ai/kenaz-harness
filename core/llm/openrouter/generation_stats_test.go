package openrouter_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/llm"
	"github.com/kameas-ai/kenaz-harness/core/llm/openrouter"
)

func TestGenerationURL(t *testing.T) {
	t.Parallel()
	for ep, want := range map[string]string{
		"": "https://openrouter.ai/api/v1/generation?id=gen-1",
		"https://openrouter.ai/api/v1/chat/completions": "https://openrouter.ai/api/v1/generation?id=gen-1",
		"https://proxy.example/api/v1/":                 "https://proxy.example/api/v1/generation?id=gen-1",
		"https://gw.example/openrouter/chat":            "https://gw.example/api/v1/generation?id=gen-1",
	} {
		got, err := openrouter.GenerationURL(ep, "gen-1")
		if err != nil || got != want {
			t.Errorf("GenerationURL(%q) = %q, %v; want %q", ep, got, err, want)
		}
	}
	if _, err := openrouter.GenerationURL("not a url", "g"); err == nil {
		t.Error("an unusable endpoint must be refused")
	}
}

// TestFetchGenerationStats_StatusClassesAndFields: a 200 decodes only the
// numeric fields and the two enum tokens (raw, unbounded here); 404 / 429
// / 5xx are "not ready, retry"; 401 is final; the request carries the
// key and the generation id.
func TestFetchGenerationStats_StatusClassesAndFields(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		code     int
		notReady bool
		final    bool
	}{{200, false, false}, {404, true, false}, {429, true, false}, {502, true, false}, {401, false, true}} {
		t.Run(fmt.Sprint(tc.code), func(t *testing.T) {
			t.Parallel()
			var gotAuth, gotID, gotPath string
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotAuth, gotID, gotPath = r.Header.Get("Authorization"), r.URL.Query().Get("id"), r.URL.Path
				if tc.code != 200 {
					w.WriteHeader(tc.code)
					return
				}
				_, _ = w.Write([]byte(`{"data":{"provider_name":"Google AI Studio","native_finish_reason":"STOP",
					"latency":812,"generation_time":1490.6,"total_cost":0.0042,"native_tokens_prompt":101,
					"native_tokens_completion":21,"native_tokens_reasoning":null,"native_tokens_cached":40,
					"origin":"https://SECRET-ORIGIN.example"}}`))
			}))
			defer ts.Close()
			raw, err := openrouter.FetchGenerationStats(context.Background(), ts.Client(),
				ts.URL+"/api/v1/chat/completions", []byte("sk-or-test"), "gen-abc")
			if gotAuth != "Bearer sk-or-test" || gotID != "gen-abc" || gotPath != "/api/v1/generation" {
				t.Fatalf("request auth=%q id=%q path=%q", gotAuth, gotID, gotPath)
			}
			switch {
			case tc.notReady:
				if !errors.Is(err, llm.ErrProviderStatsNotReady) {
					t.Fatalf("HTTP %d: err %v, want ErrProviderStatsNotReady", tc.code, err)
				}
			case tc.final:
				if err == nil || errors.Is(err, llm.ErrProviderStatsNotReady) {
					t.Fatalf("HTTP %d: err %v, want a final error", tc.code, err)
				}
			default:
				if err != nil {
					t.Fatal(err)
				}
				if raw.Upstream != "Google AI Studio" || raw.NativeFinishReason != "STOP" || *raw.LatencyMS != 812 ||
					*raw.GenerationTimeMS != 1490.6 || *raw.CostUSD != 0.0042 || *raw.NativeInputTokens != 101 ||
					*raw.NativeOutputTokens != 21 || raw.NativeReasoningTokens != nil || *raw.NativeCachedTokens != 40 {
					t.Fatalf("decoded: %+v", raw)
				}
			}
		})
	}
}
