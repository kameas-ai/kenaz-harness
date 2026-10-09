package chat

import (
	"testing"

	corellm "github.com/kameas-ai/kenaz-harness/core/llm"
)

func TestSumUsage_CostSourceAndIndeterminate(t *testing.T) {
	t.Parallel()
	prov := func(total float64) corellm.Response {
		return corellm.Response{Usage: corellm.Usage{InputTokens: 10, OutputTokens: 1},
			Cost: corellm.Cost{Currency: "USD", Total: total, Source: "provider"}}
	}
	derived := func(total float64) corellm.Response {
		return corellm.Response{Usage: corellm.Usage{InputTokens: 20, OutputTokens: 2},
			Cost: corellm.Cost{Currency: "USD", Total: total, Source: "derived"}}
	}
	indet := corellm.Response{Usage: corellm.Usage{InputTokens: 5}, Cost: corellm.Cost{Indeterminate: true}}

	cases := []struct {
		name       string
		a, b       corellm.Response
		wantSource string
		wantIndet  bool
		wantTotal  float64
	}{
		{"provider+provider", prov(0.1), prov(0.2), "provider", false, 0.3},
		{"derived+derived", derived(0.1), derived(0.2), "derived", false, 0.3},
		{"provider+derived is derived", prov(0.1), derived(0.2), "derived", false, 0.3},
		{"derived+provider is derived", derived(0.2), prov(0.1), "derived", false, 0.3},
		{"zero accumulator does not vote", corellm.Response{}, prov(0.1), "provider", false, 0.1},
		{"indeterminate side makes a derived sum indeterminate", derived(0.1), indet, "derived", true, 0.1},
		{"indeterminate either side, provider sum stays determinate", prov(0.1), corellm.Response{Cost: corellm.Cost{Source: "provider", Indeterminate: true, Total: 0.2}}, "provider", false, 0.3},
	}
	for _, tc := range cases {
		got := sumUsage(tc.a, tc.b)
		if got.Cost.Source != tc.wantSource || got.Cost.Indeterminate != tc.wantIndet {
			t.Errorf("%s: source %q indeterminate %v, want %q %v", tc.name, got.Cost.Source, got.Cost.Indeterminate, tc.wantSource, tc.wantIndet)
		}
		if d := got.Cost.Total - tc.wantTotal; d > 1e-12 || d < -1e-12 {
			t.Errorf("%s: total %v, want %v", tc.name, got.Cost.Total, tc.wantTotal)
		}
		if got.Usage.InputTokens != tc.a.Usage.InputTokens+tc.b.Usage.InputTokens {
			t.Errorf("%s: input tokens not summed", tc.name)
		}
	}
}
