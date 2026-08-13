package compactionpolicy

import "testing"

// TestTier_LockedNumerics pins the five tiers' values. Any change to
// these constants is a breaking change to user-facing behavior and
// requires a coordinated migration (plan §2.2).
func TestTier_LockedNumerics(t *testing.T) {
	cases := []struct {
		name     string
		input    CompactionAggressiveness
		expected TierParams
	}{
		{
			name:     "off",
			input:    AggressivenessOff,
			expected: TierParams{Mode: ModeNone},
		},
		{
			name:     "conservative",
			input:    AggressivenessConservative,
			expected: TierParams{TriggerPct: 0.95, SummarizePct: 0.20, Mode: ModeThreshold},
		},
		{
			name:     "balanced",
			input:    AggressivenessBalanced,
			expected: TierParams{TriggerPct: 0.80, SummarizePct: 0.30, Mode: ModeThreshold},
		},
		{
			name:     "aggressive",
			input:    AggressivenessAggressive,
			expected: TierParams{TriggerPct: 0.60, SummarizePct: 0.40, Mode: ModeThreshold},
		},
		{
			name:     "maximal",
			input:    AggressivenessMaximal,
			expected: TierParams{Mode: ModeRolling},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Tier(tc.input)
			if got != tc.expected {
				t.Fatalf("Tier(%q) = %+v, want %+v", tc.input, got, tc.expected)
			}
		})
	}
}

// TestTier_DefaultsToBalanced verifies the documented fallback: an
// unknown or empty value resolves to balanced. Settings deserialization
// relies on this so a missing JSON field naturally lands on the
// default tier.
func TestTier_DefaultsToBalanced(t *testing.T) {
	balanced := Tier(AggressivenessBalanced)

	cases := []struct {
		name  string
		input CompactionAggressiveness
	}{
		{name: "empty string", input: ""},
		{name: "unknown value", input: "ludicrous"},
		{name: "wrong case", input: "BALANCED"},
		{name: "typo", input: "balencd"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Tier(tc.input)
			if got != balanced {
				t.Fatalf("Tier(%q) = %+v, want balanced %+v", tc.input, got, balanced)
			}
		})
	}
}

// TestTier_PureFunction sanity-checks that repeated calls return the
// same value (no hidden state, no I/O). The compiler will catch most
// regressions here; this guards against someone reaching for a global
// later.
func TestTier_PureFunction(t *testing.T) {
	first := Tier(AggressivenessAggressive)
	for i := 0; i < 5; i++ {
		got := Tier(AggressivenessAggressive)
		if got != first {
			t.Fatalf("call %d: got %+v, want %+v", i, got, first)
		}
	}
}

// TestCompactionMode_DistinctValues ensures the iota constants don't
// collide. Cheap insurance against an accidental reorder.
func TestCompactionMode_DistinctValues(t *testing.T) {
	modes := map[CompactionMode]string{
		ModeNone:      "none",
		ModeThreshold: "threshold",
		ModeRolling:   "rolling",
	}
	if len(modes) != 3 {
		t.Fatalf("expected 3 distinct modes, got %d: %+v", len(modes), modes)
	}
}
