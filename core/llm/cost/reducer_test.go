package cost

import (
	"math"
	"testing"

	llm "github.com/sigil-tech/kaneaz-harness/core/llm"
)

func TestStarterTable_Loads(t *testing.T) {
	tab, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	if tab.Currency != "USD" || !tab.BestEffort {
		t.Fatalf("starter table fields: %+v", tab)
	}
	if len(tab.Entries) == 0 {
		t.Fatal("starter table empty")
	}
}

func TestReducer_AnthropicSonnetCost(t *testing.T) {
	tab, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	r := New(tab)
	cost := r.Derive(llm.Usage{InputTokens: 1_000_000, OutputTokens: 1_000_000}, "anthropic", "claude-sonnet-4-7-20260420")
	if cost.Indeterminate {
		t.Fatal("expected determinate cost")
	}
	want := 3.00 + 15.00
	if math.Abs(cost.Total-want) > 1e-6 {
		t.Fatalf("total=%v want=%v", cost.Total, want)
	}
	if cost.Currency != "USD" {
		t.Fatalf("currency=%s", cost.Currency)
	}
}

func TestReducer_UnknownModelIsIndeterminate(t *testing.T) {
	tab, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	r := New(tab)
	cost := r.Derive(llm.Usage{InputTokens: 1000}, "anthropic", "claude-future-9000")
	if !cost.Indeterminate {
		t.Fatalf("expected indeterminate, got %+v", cost)
	}
}

func TestMerge_OverrideReplaces(t *testing.T) {
	base := &Table{Currency: "USD", Entries: []Entry{{Kind: "openai", Model: "gpt-4o*", PerMillionTokens: map[string]float64{"input": 2.50, "output": 10.00}}}}
	override := &Table{Currency: "USD", Entries: []Entry{{Kind: "openai", Model: "gpt-4o*", PerMillionTokens: map[string]float64{"input": 1.00, "output": 5.00}}}}
	merged := Merge(base, override)
	if len(merged.Entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(merged.Entries))
	}
	if merged.Entries[0].PerMillionTokens["input"] != 1.00 {
		t.Fatalf("override didn't take: %v", merged.Entries[0].PerMillionTokens)
	}
}

func TestMerge_AddsNewEntries(t *testing.T) {
	base := &Table{Currency: "USD", Entries: []Entry{{Kind: "anthropic", Model: "claude-sonnet-*", PerMillionTokens: map[string]float64{"input": 3.0}}}}
	override := &Table{Entries: []Entry{{Kind: "openrouter", Model: "*", PerMillionTokens: map[string]float64{"input": 2.0}}}}
	merged := Merge(base, override)
	if len(merged.Entries) != 2 {
		t.Fatalf("expected 2 entries: %+v", merged.Entries)
	}
}

func TestReducer_NilTableSafe(t *testing.T) {
	var r *Reducer
	cost := r.Derive(llm.Usage{InputTokens: 100}, "openai", "gpt-4o-mini")
	if !cost.Indeterminate {
		t.Fatal("nil reducer should yield indeterminate cost")
	}
}
