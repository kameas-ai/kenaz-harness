package advice

import "testing"

// TestShouldShowChip_ConfidenceBoundary is the review-promoted boundary
// test (laya-advisors-01LAYA001 WP07/WP08 review round, 2026-09-29):
// spec §2d's gate is "confidence >= 75", so 74 must never show and 75
// must always show (given Decision=true) — an off-by-one here would
// silently change the chip's real-world threshold without any test
// noticing, since every other test in this package uses confidences well
// clear of the boundary (60, 90, 95, 99).
func TestShouldShowChip_ConfidenceBoundary(t *testing.T) {
	cases := []struct {
		name       string
		decision   bool
		confidence int
		want       bool
	}{
		{"74 is below the gate", true, 74, false},
		{"75 is exactly the gate", true, 75, true},
		{"76 clears the gate", true, 76, true},
		{"100 (max) clears the gate", true, 100, true},
		{"0 (min) is below the gate", true, 0, false},
		{"75 with decision=false never shows", false, 75, false},
		{"100 with decision=false never shows", false, 100, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := Recommendation{Decision: c.decision, Confidence: c.confidence}
			if got := ShouldShowChip(rec); got != c.want {
				t.Errorf("ShouldShowChip(Decision=%v, Confidence=%d) = %v, want %v",
					c.decision, c.confidence, got, c.want)
			}
		})
	}
}

// TestChipConfidenceThreshold_Value pins the literal threshold spec §2d
// names ("confidence >= 75") as a value, not just as ShouldShowChip's
// behavior — a future refactor that changes ShouldShowChip's formula
// without touching the constant (or vice versa) should fail exactly one
// of these two tests, not silently pass both.
func TestChipConfidenceThreshold_Value(t *testing.T) {
	if ChipConfidenceThreshold != 75 {
		t.Errorf("ChipConfidenceThreshold = %d, want 75 (spec §2d)", ChipConfidenceThreshold)
	}
}
