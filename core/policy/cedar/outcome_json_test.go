package cedar

import (
	"encoding/json"
	"testing"
)

// TestOutcome_JSONRoundTrip pins Outcome.MarshalJSON/UnmarshalJSON
// (consent-surfaces-truth-01PMTR01 WP06). Found while wiring the first
// real frontend consumer of cedar.Decision (the policy view's denial
// panel): Outcome is `type Outcome int`, and without a custom
// MarshalJSON, encoding/json serialises it as a bare ordinal (0/1/2).
// Every frontend type — frontend/src/lib/types.ts's PolicyDecision.outcome
// and the hand-written WailsBindingsLike / CedarPolicyClient interfaces
// in harnessClient.ts — declares outcome as the STRING union
// 'allow' | 'deny' | 'not_applicable' | 'unknown'. Before this fix that
// declaration was a lie: RecentDecisions crossed the RPC boundary as a
// number no frontend consumer's type said it could be.
//
// Mutation: delete Outcome.MarshalJSON (or Outcome.UnmarshalJSON) →
// TestOutcome_JSONRoundTrip/marshals_as_the_string_form must fail (the
// marshaled bytes become "0"/"1"/"2" instead of "\"allow\""/etc.).
func TestOutcome_JSONRoundTrip(t *testing.T) {
	t.Run("marshals as the string form, not the bare ordinal", func(t *testing.T) {
		cases := []struct {
			outcome Outcome
			want    string
		}{
			{Allow, `"allow"`},
			{Deny, `"deny"`},
			{NotApplicable, `"not_applicable"`},
		}
		for _, tc := range cases {
			b, err := json.Marshal(tc.outcome)
			if err != nil {
				t.Fatalf("Marshal(%v): %v", tc.outcome, err)
			}
			if string(b) != tc.want {
				t.Fatalf("json.Marshal(%v) = %s; want %s — a frontend expecting a string union would receive a bare ordinal",
					tc.outcome, b, tc.want)
			}
		}
	})

	t.Run("Decision.Outcome marshals inside the wire struct, matching PolicyDecision.outcome's string union", func(t *testing.T) {
		dec := Decision{
			Outcome:       Deny,
			Action:        ActionMemoryWrite,
			Principal:     `User::"local"`,
			Resource:      `Memory::"global"`,
			MatchedPolicy: "zz.cedar#0#0",
			Reason:        "forbid policy matched",
		}
		b, err := json.Marshal(dec)
		if err != nil {
			t.Fatalf("Marshal(Decision): %v", err)
		}
		var round map[string]any
		if err := json.Unmarshal(b, &round); err != nil {
			t.Fatalf("Unmarshal into map: %v", err)
		}
		got, ok := round["outcome"].(string)
		if !ok {
			t.Fatalf("Decision JSON's \"outcome\" field is %T (%v); want a string — "+
				"this is exactly what a Wails RPC response looks like to the frontend",
				round["outcome"], round["outcome"])
		}
		if got != "deny" {
			t.Fatalf("outcome = %q; want %q", got, "deny")
		}
	})

	t.Run("round-trips through Unmarshal", func(t *testing.T) {
		var o Outcome
		if err := json.Unmarshal([]byte(`"deny"`), &o); err != nil {
			t.Fatalf("Unmarshal: %v", err)
		}
		if o != Deny {
			t.Fatalf("o = %v; want Deny", o)
		}
	})

	t.Run("Unmarshal rejects an unknown string rather than silently zeroing to Allow", func(t *testing.T) {
		var o Outcome = NotApplicable // start non-zero so a silent no-op is visible
		err := json.Unmarshal([]byte(`"sideways"`), &o)
		if err == nil {
			t.Fatalf("Unmarshal(\"sideways\") succeeded; want an error for an unrecognised outcome")
		}
	})
}
