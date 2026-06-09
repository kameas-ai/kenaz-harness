package policy_test

import (
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/policy"
)

func TestAllReasonCodesCoverFR008(t *testing.T) {
	got := policy.AllReasonCodes()
	if len(got) != 9 {
		t.Fatalf("expected 9 reason codes (FR-008), got %d", len(got))
	}
	want := map[policy.ReasonCode]bool{
		policy.ReasonNotInAllowlist:          true,
		policy.ReasonExceedsCeiling:          true,
		policy.ReasonMissingSignature:        true,
		policy.ReasonWrongSigner:             true,
		policy.ReasonNetworkTierNotPermitted: true,
		policy.ReasonCapabilityNotPermitted:  true,
		policy.ReasonSandboxRequired:         true,
		policy.ReasonScheduleNotPermitted:    true,
		policy.ReasonPolicyUnavailable:       true,
	}
	for _, c := range got {
		if !want[c] {
			t.Fatalf("unexpected reason code %q", c)
		}
	}
}

func TestLayerOrder(t *testing.T) {
	order := policy.LayerOrder()
	if len(order) != 3 {
		t.Fatalf("expected 3 layers, got %d", len(order))
	}
	if order[0] != policy.LayerOrg || order[1] != policy.LayerTeam || order[2] != policy.LayerPersonal {
		t.Fatalf("layer order mismatch: %v", order)
	}
}

func TestLayerIsKnown(t *testing.T) {
	cases := map[policy.Layer]bool{
		policy.LayerOrg:      true,
		policy.LayerTeam:     true,
		policy.LayerPersonal: true,
		"":                   false,
		"organization":       false,
		"team-1":             false,
	}
	for in, want := range cases {
		if got := in.IsKnown(); got != want {
			t.Fatalf("Layer(%q).IsKnown() = %v, want %v", in, got, want)
		}
	}
}

func TestEffectivePostureDefaultsToFailClosed(t *testing.T) {
	c := policy.Clause{}
	if c.EffectivePosture() != policy.FailClosed {
		t.Fatalf("default posture should be FailClosed, got %s", c.EffectivePosture())
	}
	c.FailurePosture = policy.FailOpen
	if c.EffectivePosture() != policy.FailOpen {
		t.Fatalf("explicit FailOpen lost: %s", c.EffectivePosture())
	}
	c.FailurePosture = "garbage"
	if c.EffectivePosture() != policy.FailClosed {
		t.Fatalf("unknown posture should fall back to FailClosed, got %s", c.EffectivePosture())
	}
}

func TestContentHashStableAcrossOrderings(t *testing.T) {
	now := time.Date(2026, 4, 25, 0, 0, 0, 0, time.UTC)
	a := policy.PolicyArtifact{
		PolicyID: "p1",
		Name:     "baseline",
		Version:  "1.0.0",
		Layer:    policy.LayerOrg,
		NotBefore: &now,
		Clauses: []policy.Clause{
			{
				ClauseID: "b",
				Kind:     "k1",
				Params:   map[string]any{"alpha": 1, "beta": []any{"x", "y"}},
			},
			{
				ClauseID: "a",
				Kind:     "k2",
				Params:   map[string]any{"gamma": "g"},
			},
		},
	}
	b := policy.PolicyArtifact{
		PolicyID: "p1",
		Name:     "baseline",
		Version:  "1.0.0",
		Layer:    policy.LayerOrg,
		NotBefore: &now,
		Clauses: []policy.Clause{
			{
				ClauseID: "a",
				Kind:     "k2",
				Params:   map[string]any{"gamma": "g"},
			},
			{
				ClauseID: "b",
				Kind:     "k1",
				// Same logical map but with reversed iteration order.
				Params: map[string]any{"beta": []any{"x", "y"}, "alpha": 1},
			},
		},
	}
	if policy.ContentHashOf(a) != policy.ContentHashOf(b) {
		t.Fatalf("content hash should be order-independent\n  a=%s\n  b=%s",
			policy.ContentHashOf(a), policy.ContentHashOf(b))
	}
}

func TestContentHashChangesWithContent(t *testing.T) {
	a := policy.PolicyArtifact{
		PolicyID: "p1",
		Clauses: []policy.Clause{
			{ClauseID: "x", Kind: "k", Params: map[string]any{"v": 1}},
		},
	}
	b := a
	b.Clauses = []policy.Clause{
		{ClauseID: "x", Kind: "k", Params: map[string]any{"v": 2}},
	}
	if policy.ContentHashOf(a) == policy.ContentHashOf(b) {
		t.Fatalf("expected hash divergence on differing params")
	}
}

func TestUnknownKindErrorMessage(t *testing.T) {
	err := &policy.UnknownKindError{PolicyID: "p", ClauseID: "c", Kind: "k"}
	got := err.Error()
	if got == "" {
		t.Fatalf("expected non-empty error message")
	}
	for _, want := range []string{"p", "c", "k", "unknown control kind"} {
		if !contains(got, want) {
			t.Fatalf("error message %q missing %q", got, want)
		}
	}
}

func contains(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	n, m := len(s), len(sub)
	for i := 0; i+m <= n; i++ {
		if s[i:i+m] == sub {
			return i
		}
	}
	return -1
}
