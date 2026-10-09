package cost

import (
	"math"
	"testing"

	llm "github.com/kameas-ai/kenaz-harness/core/llm"
)

// cacheTable has cache rates on every provider kind so each convention is
// exercised against the same numbers.
func cacheTable() *Table {
	rates := map[string]float64{
		"input":              10.00,
		"output":             20.00,
		"cached_input_read":  1.00,
		"cached_input_write": 12.50,
	}
	return &Table{Currency: "USD", Entries: []Entry{
		{Kind: "anthropic", Model: "*", PerMillionTokens: rates},
		{Kind: "openrouter", Model: "*", PerMillionTokens: rates},
		{Kind: "gemini", Model: "*", PerMillionTokens: rates},
		{Kind: "openai", Model: "*", PerMillionTokens: map[string]float64{"input": 10.00, "output": 20.00}},
	}}
}

// The same 1M-token prompt, of which 600k were cache reads and 100k cache
// writes, costs the same on every provider once each convention is read
// correctly: Anthropic reports the 300k remainder as InputTokens, the
// inclusive providers report the whole 1M.
func TestReducer_CachedTokensBilledOncePerConvention(t *testing.T) {
	const (
		read  = 600_000
		write = 100_000
		rest  = 300_000
	)
	// 0.3M × $10 + 0.6M × $1 + 0.1M × $12.50 + 0.1M output × $20
	const want = 3.00 + 0.60 + 1.25 + 2.00

	cases := []struct {
		kind        string
		input       int
		wantInput   float64
		wantCached  float64
		wantTotal   float64
		description string
	}{
		{"anthropic", rest, 3.00, 1.85, want, "input_tokens excludes the cache"},
		{"openrouter", rest + read + write, 3.00, 1.85, want, "prompt_tokens includes the cache"},
		{"gemini", rest + read + write, 3.00, 1.85, want, "promptTokenCount includes the cache"},
		// No cache rates: the cached counts stay in the input bucket and
		// are billed once, at the input rate.
		{"openai", rest + read + write, 10.00, 0, 10.00 + 2.00, "no cache rates"},
	}
	r := New(cacheTable())
	for _, tc := range cases {
		t.Run(tc.kind, func(t *testing.T) {
			u := llm.Usage{InputTokens: tc.input, OutputTokens: 100_000, CachedInputRead: read, CachedInputWrite: write}
			c := r.Derive(u, tc.kind, "m")
			if c.Indeterminate {
				t.Fatal("indeterminate")
			}
			if math.Abs(c.InputCost-tc.wantInput) > 1e-9 || math.Abs(c.CachedCost-tc.wantCached) > 1e-9 ||
				math.Abs(c.Total-tc.wantTotal) > 1e-9 {
				t.Errorf("%s: input=%v cached=%v total=%v, want %v/%v/%v",
					tc.description, c.InputCost, c.CachedCost, c.Total, tc.wantInput, tc.wantCached, tc.wantTotal)
			}
		})
	}
}

// A cache count larger than the inclusive InputTokens (a malformed frame)
// never drives the input bucket negative.
func TestReducer_InclusiveCacheCountClampedToInput(t *testing.T) {
	r := New(cacheTable())
	c := r.Derive(llm.Usage{InputTokens: 100, CachedInputRead: 500, CachedInputWrite: 50}, "openrouter", "m")
	if c.InputCost != 0 {
		t.Errorf("input cost = %v, want 0", c.InputCost)
	}
	if want := 100.0 / 1_000_000 * 1.00; math.Abs(c.CachedCost-want) > 1e-12 {
		t.Errorf("cached cost = %v, want %v (reads clamped to the 100 input tokens, nothing left for writes)", c.CachedCost, want)
	}
}

// DeriveWithSource (the pricing-table branch) follows the same rule:
// OpenRouter's Anthropic-family row carries Anthropic's cache rates.
func TestDeriveWithSource_PerProviderConvention(t *testing.T) {
	cases := []struct {
		kind, model string
		usage       llm.Usage
		want        float64
	}{
		{
			kind: "anthropic", model: "claude-sonnet-4-5",
			usage: llm.Usage{InputTokens: 300_000, CachedInputRead: 600_000, CachedInputWrite: 100_000},
			// 0.3 × 3.00 + 0.6 × 0.30 + 0.1 × 3.75
			want: 0.90 + 0.18 + 0.375,
		},
		{
			kind: "openrouter", model: "anthropic/claude-sonnet-4.5",
			usage: llm.Usage{InputTokens: 1_000_000, CachedInputRead: 600_000, CachedInputWrite: 100_000},
			want:  0.90 + 0.18 + 0.375,
		},
		{
			// The wildcard row has no cache rates: billed once at input.
			kind: "openrouter", model: "z-ai/glm-5.2",
			usage: llm.Usage{InputTokens: 1_000_000, CachedInputRead: 600_000},
			want:  2.00,
		},
	}
	for _, tc := range cases {
		t.Run(tc.kind+"/"+tc.model, func(t *testing.T) {
			got, src := DeriveWithSource(tc.usage, tc.kind, tc.model, nil)
			if src != SourceDerived || got == nil {
				t.Fatalf("source=%q cost=%v, want derived", src, got)
			}
			if math.Abs(*got-tc.want) > 1e-9 {
				t.Errorf("cost = %v, want %v", *got, tc.want)
			}
		})
	}
}
