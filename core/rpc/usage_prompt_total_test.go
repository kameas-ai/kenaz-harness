package rpc

import (
	"testing"

	corellm "github.com/kameas-ai/kenaz-harness/core/llm"
)

// tool-context-budget-01TCBUD01 WP05: the context bar and the usage row
// record the whole prompt under either provider convention, so an
// Anthropic call served mostly from cache does not under-report.
func TestUsageRecords_PromptTotalPerProviderConvention(t *testing.T) {
	cases := []struct {
		kind       string
		usage      corellm.Usage
		wantPrompt int
	}{
		// Anthropic: input_tokens is the uncached remainder.
		{"anthropic", corellm.Usage{InputTokens: 300, OutputTokens: 10, CachedInputRead: 9000, CachedInputWrite: 700}, 10000},
		// OpenRouter: prompt_tokens already includes the cache.
		{"openrouter", corellm.Usage{InputTokens: 10000, OutputTokens: 10, CachedInputRead: 9000, CachedInputWrite: 700}, 10000},
	}
	for _, tc := range cases {
		t.Run(tc.kind, func(t *testing.T) {
			resp := corellm.Response{Usage: tc.usage}
			snap := lastUsageSnapshot(resp, tc.kind)
			if snap.PromptTokens != tc.wantPrompt || snap.TotalTokens != tc.wantPrompt+10 {
				t.Errorf("snapshot prompt/total = %d/%d, want %d/%d", snap.PromptTokens, snap.TotalTokens, tc.wantPrompt, tc.wantPrompt+10)
			}
			row := usageTurnRecord("s", "m", tc.kind, "model", resp)
			if row.PromptTokens != tc.wantPrompt {
				t.Errorf("usage row prompt = %d, want %d", row.PromptTokens, tc.wantPrompt)
			}
			if row.CachedTokens != 9000 || row.CacheWriteTokens != 700 {
				t.Errorf("usage row cache = %d/%d, want 9000/700", row.CachedTokens, row.CacheWriteTokens)
			}
		})
	}
}
