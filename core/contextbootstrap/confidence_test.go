package contextbootstrap

import (
	"testing"
)

func TestScoreNode(t *testing.T) {
	rules := ConfidenceRules{
		AssertMinCorroborations: 3,
		TrustedPersonWeight:     3,
	}

	t.Run("below threshold is not asserted", func(t *testing.T) {
		confidence, isAsserted := scoreNode(1, nil, nil, rules)
		if isAsserted {
			t.Error("expected not asserted for 1 corroboration")
		}
		if confidence >= 1.0 {
			t.Errorf("confidence too high: %f", confidence)
		}
	})

	t.Run("at threshold is asserted", func(t *testing.T) {
		confidence, isAsserted := scoreNode(3, nil, nil, rules)
		if !isAsserted {
			t.Error("expected asserted at threshold")
		}
		if confidence <= 0 {
			t.Errorf("confidence too low: %f", confidence)
		}
	})

	t.Run("trusted person boosts past threshold", func(t *testing.T) {
		trustMap := buildTrustMap([]TrustedPerson{
			{Identifier: "alice@example.com", TrustLevel: "high"},
		})
		// 1 corroboration from a trusted person => effective = 1 + (3-1) = 3 => asserted
		_, isAsserted := scoreNode(1, []string{"alice@example.com"}, trustMap, rules)
		if !isAsserted {
			t.Error("expected trusted person to boost past assert threshold")
		}
	})

	t.Run("unknown sender no boost", func(t *testing.T) {
		trustMap := buildTrustMap([]TrustedPerson{
			{Identifier: "alice@example.com", TrustLevel: "high"},
		})
		// 1 corroboration from unknown sender => not asserted
		_, isAsserted := scoreNode(1, []string{"unknown@example.com"}, trustMap, rules)
		if isAsserted {
			t.Error("expected not asserted for unknown sender with 1 corroboration")
		}
	})

	t.Run("confidence capped at 0.95", func(t *testing.T) {
		confidence, _ := scoreNode(100, nil, nil, rules)
		if confidence > 0.95 {
			t.Errorf("confidence exceeds 0.95: %f", confidence)
		}
	})

	t.Run("zero corroborations has zero confidence", func(t *testing.T) {
		confidence, isAsserted := scoreNode(0, nil, nil, rules)
		if confidence != 0 {
			t.Errorf("expected 0 confidence for 0 corroborations, got %f", confidence)
		}
		if isAsserted {
			t.Error("expected not asserted for 0 corroborations")
		}
	})
}

func TestBuildTrustMap(t *testing.T) {
	people := []TrustedPerson{
		{Identifier: "Alice Smith", TrustLevel: "high"},
		{Identifier: "  BOB  ", TrustLevel: "medium"},
	}
	m := buildTrustMap(people)

	if _, ok := m["alice smith"]; !ok {
		t.Error("trust map missing normalized 'alice smith'")
	}
	if _, ok := m["bob"]; !ok {
		t.Error("trust map missing normalized 'bob'")
	}
}

func TestNormalizeIdentifier(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"Alice", "alice"},
		{"  BOB  ", "bob"},
		{"Alice Smith", "alice smith"},
		{"", ""},
	}
	for _, tc := range cases {
		got := normalizeIdentifier(tc.input)
		if got != tc.want {
			t.Errorf("normalizeIdentifier(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}
