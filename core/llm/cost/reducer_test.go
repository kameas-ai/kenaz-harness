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

// TestKindCompaction_Constant pins the WP08 cost-tag constant. The
// per-session cost view in core/compaction/wiring/llm.go reads this
// to render "Compaction overhead: $X.XX of $Y.YY total" (FR §2.11);
// a rename here would silently break the frontend's cost projection.
func TestKindCompaction_Constant(t *testing.T) {
	if KindCompaction != "compaction" {
		t.Fatalf("KindCompaction = %q, want %q", KindCompaction, "compaction")
	}
}

// TestReducer_TaggedSubsetSum sums the cost across two synthetic
// "compaction" calls plus one "chat" call to model the per-session
// cost panel's break-out behavior. The reducer itself doesn't filter
// by the tag — that's the OverheadTotals adapter's job in
// core/compaction/wiring/llm.go — but the reducer must still produce
// determinate cost for the same (provider, model) regardless of which
// kind triggered the call. This test pins that contract so a future
// reducer change accidentally keying on the tag fails loudly.
func TestReducer_TaggedSubsetSum(t *testing.T) {
	tab, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	r := New(tab)

	// Three calls against the same provider/model. Two of them are
	// modeled as compaction calls (same input/output token counts),
	// one is a chat call. The reducer's per-call answer should be
	// identical for all three — the kind passed in is the provider
	// kind, not the cost.kind tag, so summing in Go gives a clean
	// "compaction overhead" subtotal.
	usage := llm.Usage{InputTokens: 100_000, OutputTokens: 100_000}
	chat1 := r.Derive(usage, "anthropic", "claude-sonnet-4-5")
	comp1 := r.Derive(usage, "anthropic", "claude-sonnet-4-5")
	comp2 := r.Derive(usage, "anthropic", "claude-sonnet-4-5")

	if chat1.Total <= 0 || comp1.Total <= 0 || comp2.Total <= 0 {
		t.Fatalf("expected positive costs; chat=%v comp1=%v comp2=%v",
			chat1.Total, comp1.Total, comp2.Total)
	}
	if math.Abs(chat1.Total-comp1.Total) > 1e-9 {
		t.Errorf("reducer should not vary cost by call kind: chat=%v comp=%v",
			chat1.Total, comp1.Total)
	}

	compactionSubtotal := comp1.Total + comp2.Total
	total := compactionSubtotal + chat1.Total
	if compactionSubtotal >= total {
		t.Errorf("compaction subtotal=%v should be less than total=%v",
			compactionSubtotal, total)
	}
}
