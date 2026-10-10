package openrouter

// generation_stats.go — OpenRouter's per-generation stats endpoint
// (llm-call-telemetry-01LLMTEL01 WP02): GET {base}/api/v1/generation?id=
// <generation id>, authenticated with the profile's own API key. Called by
// the local model-call store's stats pull (core/modelcalls) through
// registry.FetchProviderStats, after a call that returned a generation id.
//
// Only numeric fields and two provider enum tokens (provider_name,
// native_finish_reason) are decoded; no text field of the response —
// origin, model names, user ids, anything free-form — is read. The values
// are still RAW here: genaitrace.BoundProviderStats applies the allowlist
// bounds before anything is stored.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/kameas-ai/kenaz-harness/core/llm"
	"github.com/kameas-ai/kenaz-harness/core/llm/genaitrace"
)

// DefaultGenerationURL is the stats endpoint for a profile with no
// endpoint override.
const DefaultGenerationURL = "https://openrouter.ai/api/v1/generation"

// GenerationURL derives the stats URL for genID from a profile endpoint
// override: ".../chat/completions" and ".../v1" map to the sibling
// "/generation"; any other override keeps its scheme and host with the
// standard path; empty is DefaultGenerationURL.
func GenerationURL(endpoint, genID string) (string, error) {
	base := DefaultGenerationURL
	if ep := strings.TrimRight(strings.TrimSpace(endpoint), "/"); ep != "" {
		switch {
		case strings.HasSuffix(ep, "/chat/completions"):
			base = strings.TrimSuffix(ep, "/chat/completions") + "/generation"
		case strings.HasSuffix(ep, "/v1"):
			base = ep + "/generation"
		default:
			pu, err := url.Parse(ep)
			if err != nil || pu.Scheme == "" || pu.Host == "" {
				return "", fmt.Errorf("openrouter: unusable endpoint for the generation-stats pull")
			}
			base = pu.Scheme + "://" + pu.Host + "/api/v1/generation"
		}
	}
	return base + "?id=" + url.QueryEscape(genID), nil
}

// FetchGenerationStats performs one GET of the stats endpoint. A 404 (the
// stats land a few seconds after the stream ends), a 429, a 5xx or a
// transport failure returns an error wrapping llm.ErrProviderStatsNotReady
// — retry later; any other non-200 is a plain error — do not retry.
func FetchGenerationStats(ctx context.Context, httpc *http.Client, endpoint string, cred []byte, genID string) (genaitrace.RawProviderStats, error) {
	u, err := GenerationURL(endpoint, genID)
	if err != nil {
		return genaitrace.RawProviderStats{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return genaitrace.RawProviderStats{}, err
	}
	req.Header.Set("Authorization", "Bearer "+string(cred))
	req.Header.Set("Accept", "application/json")
	if httpc == nil {
		httpc = http.DefaultClient
	}
	resp, err := httpc.Do(req)
	if err != nil {
		return genaitrace.RawProviderStats{}, fmt.Errorf("%w: transport", llm.ErrProviderStatsNotReady)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	switch {
	case resp.StatusCode == http.StatusOK:
	case resp.StatusCode == http.StatusNotFound, resp.StatusCode == http.StatusTooManyRequests,
		resp.StatusCode >= 500:
		return genaitrace.RawProviderStats{}, fmt.Errorf("%w: HTTP %d", llm.ErrProviderStatsNotReady, resp.StatusCode)
	default:
		return genaitrace.RawProviderStats{}, fmt.Errorf("openrouter: generation-stats pull refused (HTTP %d)", resp.StatusCode)
	}
	var env struct {
		Data struct {
			ProviderName           *string  `json:"provider_name"`
			NativeFinishReason     *string  `json:"native_finish_reason"`
			Latency                *float64 `json:"latency"`
			GenerationTime         *float64 `json:"generation_time"`
			TotalCost              *float64 `json:"total_cost"`
			NativeTokensPrompt     *float64 `json:"native_tokens_prompt"`
			NativeTokensCompletion *float64 `json:"native_tokens_completion"`
			NativeTokensReasoning  *float64 `json:"native_tokens_reasoning"`
			NativeTokensCached     *float64 `json:"native_tokens_cached"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		return genaitrace.RawProviderStats{}, fmt.Errorf("openrouter: generation-stats pull: malformed body")
	}
	d := env.Data
	raw := genaitrace.RawProviderStats{
		LatencyMS:             d.Latency,
		GenerationTimeMS:      d.GenerationTime,
		CostUSD:               d.TotalCost,
		NativeInputTokens:     toInt64(d.NativeTokensPrompt),
		NativeOutputTokens:    toInt64(d.NativeTokensCompletion),
		NativeReasoningTokens: toInt64(d.NativeTokensReasoning),
		NativeCachedTokens:    toInt64(d.NativeTokensCached),
	}
	if d.ProviderName != nil {
		raw.Upstream = *d.ProviderName
	}
	if d.NativeFinishReason != nil {
		raw.NativeFinishReason = *d.NativeFinishReason
	}
	return raw, nil
}

func toInt64(f *float64) *int64 {
	if f == nil {
		return nil
	}
	i := int64(*f)
	return &i
}
