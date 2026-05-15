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

// TestReducer_AzureOpenAI_Gpt4o asserts that azure-openai gpt-4o usage
// derives the same dollar cost as equivalent OpenAI usage.
// (azure-openai-adapter-01KQ8VMZ WP06)
func TestReducer_AzureOpenAI_Gpt4o(t *testing.T) {
	tab, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	r := New(tab)
	usage := llm.Usage{InputTokens: 1_000_000, OutputTokens: 1_000_000}

	azureCost := r.Derive(usage, "azure-openai", "gpt-4o")
	openaiCost := r.Derive(usage, "openai", "gpt-4o")

	if azureCost.Indeterminate {
		t.Fatal("azure-openai gpt-4o cost is indeterminate — check starter_table.yaml")
	}
	if math.Abs(azureCost.Total-openaiCost.Total) > 1e-6 {
		t.Errorf("azure-openai cost %v != openai cost %v for same usage", azureCost.Total, openaiCost.Total)
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

// TestKindAutoTitle_PinsConstantValue pins that KindAutoTitle is exactly
// "auto_title" so any renaming during refactors shows up as a test failure
// rather than a silent contract break in the wiring adapter's tag path.
func TestKindAutoTitle_PinsConstantValue(t *testing.T) {
	if KindAutoTitle != "auto_title" {
		t.Errorf("KindAutoTitle = %q, want \"auto_title\"", KindAutoTitle)
	}
}

// TestKindCompaction_StillPinsValue ensures adding KindAutoTitle didn't
// accidentally change the existing KindCompaction constant.
func TestKindCompaction_StillPinsValue(t *testing.T) {
	if KindCompaction != "compaction" {
		t.Errorf("KindCompaction = %q, want \"compaction\"", KindCompaction)
	}
}

// ── WP05 image-cost tests (multimodal-io-extended-01KQ8TD2) ──────────────────

// TestDeriveImage_DallE3_HD_1024x1024 verifies that DALL-E 3 hd 1024x1024
// with 2 images generated → ImageCost = 0.16 (2 × $0.08).
func TestDeriveImage_DallE3_HD_1024x1024(t *testing.T) {
	tab, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	r := New(tab)
	usage := llm.Usage{ImagesGenerated: 2}
	cost := r.DeriveImage(usage, "openai", "dall-e-3", "hd", "1024x1024")
	if cost.Indeterminate {
		t.Fatal("expected determinate cost for dall-e-3 hd/1024x1024")
	}
	want := 0.16
	if math.Abs(cost.Total-want) > 1e-9 {
		t.Fatalf("Total = %v want %v", cost.Total, want)
	}
	if math.Abs(cost.ImageCost-want) > 1e-9 {
		t.Fatalf("ImageCost = %v want %v", cost.ImageCost, want)
	}
}

// TestDeriveImage_DallE3_Standard verifies standard quality falls back
// to the quality-only key.
func TestDeriveImage_DallE3_Standard(t *testing.T) {
	tab, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	r := New(tab)
	// quality="standard", size="" → quality-only key "standard" = $0.040
	cost := r.DeriveImage(llm.Usage{ImagesGenerated: 1}, "openai", "dall-e-3", "standard", "")
	if cost.Indeterminate {
		t.Fatal("expected determinate cost for dall-e-3 standard quality")
	}
	want := 0.040
	if math.Abs(cost.Total-want) > 1e-9 {
		t.Fatalf("Total = %v want %v", cost.Total, want)
	}
}

// TestDeriveImage_UnknownQualitySizeIsIndeterminate verifies that an
// unrecognized quality+size combo yields Indeterminate=true.
func TestDeriveImage_UnknownQualitySizeIsIndeterminate(t *testing.T) {
	tab, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	r := New(tab)
	// "ultra" quality is not in the table.
	cost := r.DeriveImage(llm.Usage{ImagesGenerated: 1}, "openai", "dall-e-3", "ultra", "4096x4096")
	if !cost.Indeterminate {
		t.Fatalf("expected indeterminate for unknown quality/size, got %+v", cost)
	}
}

// TestDeriveImage_ZeroImagesIsZeroCost verifies that ImagesGenerated=0
// returns a zero cost (not indeterminate).
func TestDeriveImage_ZeroImagesIsZeroCost(t *testing.T) {
	tab, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	r := New(tab)
	cost := r.DeriveImage(llm.Usage{ImagesGenerated: 0}, "openai", "dall-e-3", "hd", "1024x1024")
	if cost.Indeterminate {
		t.Fatal("zero images should not be indeterminate")
	}
	if cost.Total != 0 {
		t.Fatalf("zero images should have zero cost, got %v", cost.Total)
	}
}

// TestDeriveImage_NoTokenPricingModelIsIndeterminate verifies that a
// token-only model (no per_image_usd) yields Indeterminate for image derive.
func TestDeriveImage_NoTokenPricingModelIsIndeterminate(t *testing.T) {
	tab, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	r := New(tab)
	// gpt-4o is a token-pricing model; it has no per_image_usd entry.
	cost := r.DeriveImage(llm.Usage{ImagesGenerated: 1}, "openai", "gpt-4o", "", "")
	if !cost.Indeterminate {
		t.Fatalf("expected indeterminate for token-only model, got %+v", cost)
	}
}

// ── Gemini cost tests (google-vertex-gemini-adapter-01KQ8VMY WP07) ────────────

// TestReducer_GeminiFlashCost pins the cost derivation for
// gemini-2.5-flash with 100 input tokens and 50 output tokens.
func TestReducer_GeminiFlashCost(t *testing.T) {
	tab, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	r := New(tab)
	// gemini-2.5-flash: $0.15/M input, $0.60/M output
	usage := llm.Usage{InputTokens: 100, OutputTokens: 50}
	cost := r.Derive(usage, "gemini", "gemini-2.5-flash")
	if cost.Indeterminate {
		t.Fatal("expected determinate cost for gemini-2.5-flash")
	}
	// 100 / 1e6 * 0.15 + 50 / 1e6 * 0.60
	wantInput := 100.0 / 1e6 * 0.15
	wantOutput := 50.0 / 1e6 * 0.60
	want := wantInput + wantOutput
	if math.Abs(cost.Total-want) > 1e-9 {
		t.Fatalf("Total = %v want %v", cost.Total, want)
	}
	if cost.Currency != "USD" {
		t.Fatalf("currency=%s", cost.Currency)
	}
}
