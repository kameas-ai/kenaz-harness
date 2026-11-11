package cedar

import "testing"

// Probe is Evaluate without the Decision log: served-mode bash
// availability (D-h) asks it on every tool listing, and bash is off iff
// the Outcome is Deny.
func TestProbe_MatchesEvaluateAndRecordsNothing(t *testing.T) {
	const (
		forbidBash    = `forbid(principal, action == Action::"use_tool", resource == Tool::"kenaz__bash");`
		forbidBashCtx = `forbid(principal, action == Action::"use_tool", resource == Tool::"kenaz__bash") when { context.workbench_tier == "restricted" };`
	)
	bash := PermissionToolUID("kenaz__bash")

	t.Run("embedded default allows; nothing recorded", func(t *testing.T) {
		e, _ := newDiskEngine(t, nil)
		before := len(e.RecentDecisions(10000))
		if got := e.Probe(UserUID(), ActionUseTool, bash, nil).Outcome; got == Deny {
			t.Fatalf("default policy: Probe outcome %v, want not Deny", got)
		}
		if after := len(e.RecentDecisions(10000)); after != before {
			t.Fatalf("Probe recorded a decision (%d -> %d)", before, after)
		}
	})

	t.Run("fleet forbid denies and is attributed to fleet", func(t *testing.T) {
		e, _ := newDiskEngine(t, nil)
		if err := e.ReplaceHarnessSnippets(FleetTeamSnippetPrefix, map[string][]byte{
			FleetTeamSnippetPrefix + "no-bash": []byte(forbidBash),
		}); err != nil {
			t.Fatal(err)
		}
		d := e.Probe(UserUID(), ActionUseTool, bash, nil)
		if d.Outcome != Deny {
			t.Fatalf("outcome %v, want Deny", d.Outcome)
		}
		id, ok := FleetRuleIDFromPolicyID(d.MatchedPolicy)
		if !ok || id != "no-bash" {
			t.Fatalf("FleetRuleIDFromPolicyID(%q) = %q,%v; want no-bash,true", d.MatchedPolicy, id, ok)
		}
		if !IsFleetDecision(d) {
			t.Fatal("IsFleetDecision false for a fleet forbid")
		}
	})

	t.Run("local forbid denies and is not fleet", func(t *testing.T) {
		e, _ := newDiskEngine(t, nil)
		if err := e.LoadHarnessSnippets(map[string][]byte{"local/no-bash": []byte(forbidBash)}); err != nil {
			t.Fatal(err)
		}
		d := e.Probe(UserUID(), ActionUseTool, bash, nil)
		if d.Outcome != Deny {
			t.Fatalf("outcome %v, want Deny", d.Outcome)
		}
		if IsFleetDecision(d) {
			t.Fatalf("local forbid %q attributed to fleet", d.MatchedPolicy)
		}
	})

	t.Run("context-reading forbid fails closed without context", func(t *testing.T) {
		e, _ := newDiskEngine(t, nil)
		if err := e.ReplaceHarnessSnippets(FleetTeamSnippetPrefix, map[string][]byte{
			FleetTeamSnippetPrefix + "ctx": []byte(forbidBashCtx),
		}); err != nil {
			t.Fatal(err)
		}
		if got := e.Probe(UserUID(), ActionUseTool, bash, nil).Outcome; got != Deny {
			t.Fatalf("erroring forbid: outcome %v, want Deny (fail closed)", got)
		}
	})

	t.Run("nil engine", func(t *testing.T) {
		var e *Engine
		if got := e.Probe(UserUID(), ActionUseTool, bash, nil).Outcome; got != NotApplicable {
			t.Fatalf("nil engine: %v", got)
		}
	})

	t.Run("non-fleet ids", func(t *testing.T) {
		for _, id := range []string{"", "policy0", "harness-self/local/x#0"} {
			if _, ok := FleetRuleIDFromPolicyID(id); ok {
				t.Fatalf("%q attributed to fleet", id)
			}
		}
	})
}
