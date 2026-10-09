package cost

import (
	"math"
	"testing"

	llm "github.com/kameas-ai/kenaz-harness/core/llm"
	"github.com/kameas-ai/kenaz-harness/core/llm/pricing"
)

type directRate struct {
	model                      string
	input, output, read, write float64
}

// Current direct-Anthropic models resolve to their own rows, not to an
// older family glob that matches first.
var directRates = []directRate{
	{"claude-opus-4-5-20251101", 5.00, 25.00, 0.50, 6.25},
	{"claude-opus-4-6", 5.00, 25.00, 0.50, 6.25},
	{"claude-haiku-4-5-20251001", 1.00, 5.00, 0.10, 1.25},
}

func TestStarterTable_AnthropicDirectCurrentRates(t *testing.T) {
	tab, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	r := New(tab)
	// 1M uncached input + 1M cache read + 1M cache write + 1M output.
	u := llm.Usage{InputTokens: 1_000_000, OutputTokens: 1_000_000, CachedInputRead: 1_000_000, CachedInputWrite: 1_000_000}
	for _, tc := range directRates {
		t.Run(tc.model, func(t *testing.T) {
			c := r.Derive(u, "anthropic", tc.model)
			if c.Indeterminate {
				t.Fatal("indeterminate")
			}
			if math.Abs(c.InputCost-tc.input) > 1e-9 || math.Abs(c.OutputCost-tc.output) > 1e-9 ||
				math.Abs(c.CachedCost-(tc.read+tc.write)) > 1e-9 {
				t.Errorf("input=%v output=%v cached=%v, want %v/%v/%v",
					c.InputCost, c.OutputCost, c.CachedCost, tc.input, tc.output, tc.read+tc.write)
			}
		})
	}
}

func TestPricingTable_AnthropicDirectCurrentRates(t *testing.T) {
	for _, tc := range directRates {
		t.Run(tc.model, func(t *testing.T) {
			e, ok := pricing.Lookup("anthropic", tc.model)
			if !ok {
				t.Fatal("no row")
			}
			if e.InputPer1MUSD != tc.input || e.OutputPer1MUSD != tc.output ||
				e.CachedInputPer1MUSD != tc.read || e.CachedInputWritePer1MUSD != tc.write {
				t.Errorf("row = %+v, want %v/%v/%v/%v", *e, tc.input, tc.output, tc.read, tc.write)
			}
		})
	}
}
