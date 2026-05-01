package autonomy

import "testing"

func TestTierStringRoundTrip(t *testing.T) {
	cases := []struct {
		tier Tier
		name string
	}{
		{TierStrict, "strict"},
		{TierCautious, "cautious"},
		{TierDefault, "default"},
		{TierBold, "bold"},
		{TierAutonomous, "autonomous"},
	}
	for _, c := range cases {
		if got := c.tier.String(); got != c.name {
			t.Errorf("Tier(%d).String() = %q, want %q", c.tier, got, c.name)
		}
		got, err := ParseTier(c.name)
		if err != nil {
			t.Errorf("ParseTier(%q) err = %v", c.name, err)
		}
		if got != c.tier {
			t.Errorf("ParseTier(%q) = %v, want %v", c.name, got, c.tier)
		}
	}
}

func TestParseTierCaseInsensitive(t *testing.T) {
	cases := map[string]Tier{
		"STRICT":      TierStrict,
		"Cautious":    TierCautious,
		"  default  ": TierDefault,
		"BoLd":        TierBold,
		"AUTONOMOUS":  TierAutonomous,
	}
	for in, want := range cases {
		got, err := ParseTier(in)
		if err != nil {
			t.Fatalf("ParseTier(%q) err = %v", in, err)
		}
		if got != want {
			t.Errorf("ParseTier(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestParseTierInvalid(t *testing.T) {
	bad := []string{"", "extreme", "tier(2)", "0", "Default!"}
	for _, in := range bad {
		if _, err := ParseTier(in); err == nil {
			t.Errorf("ParseTier(%q) expected error, got nil", in)
		}
	}
}

func TestTierStringUnknownFallback(t *testing.T) {
	got := Tier(99).String()
	if got != "tier(99)" {
		t.Errorf("Tier(99).String() = %q, want \"tier(99)\"", got)
	}
}
