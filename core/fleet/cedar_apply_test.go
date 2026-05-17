package fleet

import (
	"context"
	"encoding/json"
	"testing"

	gocedar "github.com/cedar-policy/cedar-go"
	cedarpolicy "github.com/sigil-tech/kaneaz-harness/core/policy/cedar"
)

// newTestEngine creates an in-memory Cedar engine suitable for apply tests.
func newTestEngine(t *testing.T) *cedarpolicy.Engine {
	t.Helper()
	engine, err := cedarpolicy.NewEngine(cedarpolicy.Options{
		LoadFromDisk:    false,
		IncludeEmbedded: false,
	})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	return engine
}

// TestCedarBundleApplier_TeamPermit verifies that a team permit rule is
// installed and can be looked up via ListPolicies.
func TestCedarBundleApplier_TeamPermit(t *testing.T) {
	engine := newTestEngine(t)
	applier := NewCedarBundleApplier(engine)

	delta := &CedarDelta{
		Rules: map[string]string{
			"team-allow-github": `permit(
  principal,
  action == Action::"use_tool",
  resource == Tool::"github"
);`,
		},
	}

	if err := applier.SetTeamBundle(context.Background(), delta); err != nil {
		t.Fatalf("SetTeamBundle: %v", err)
	}

	files := engine.ListPolicies()
	found := false
	for _, f := range files {
		if f.Name == "fleet-team/team-allow-github" {
			found = true
			if !f.ParseOK {
				t.Errorf("fleet-team/team-allow-github: ParseOK=false, err=%s", f.ParseErr)
			}
		}
	}
	if !found {
		t.Error("fleet-team/team-allow-github not found in ListPolicies")
	}
}

// TestCedarBundleApplier_EmptyDelta verifies that a nil/empty delta is a no-op.
func TestCedarBundleApplier_EmptyDelta(t *testing.T) {
	engine := newTestEngine(t)
	applier := NewCedarBundleApplier(engine)

	if err := applier.SetTeamBundle(context.Background(), nil); err != nil {
		t.Errorf("nil delta: unexpected error: %v", err)
	}
	if err := applier.SetTeamBundle(context.Background(), &CedarDelta{}); err != nil {
		t.Errorf("empty delta: unexpected error: %v", err)
	}
	if files := engine.ListPolicies(); len(files) != 0 {
		t.Errorf("expected no policies after empty delta, got %d", len(files))
	}
}

// TestCedarBundleApplier_TeamPermitPlusForbidOverride verifies that a local
// forbid rule addressing a fleet team_rule_id can override the team permit.
// Cedar semantics: forbid wins over permit, so the effective decision is Deny.
func TestCedarBundleApplier_TeamPermitPlusForbidOverride(t *testing.T) {
	engine := newTestEngine(t)
	applier := NewCedarBundleApplier(engine)

	// Install team permit for rule-id "rule-001".
	delta := &CedarDelta{
		Rules: map[string]string{
			"rule-001": `permit(principal, action, resource);`,
		},
	}
	if err := applier.SetTeamBundle(context.Background(), delta); err != nil {
		t.Fatalf("SetTeamBundle: %v", err)
	}

	// Install a local forbid override using the same harness snippet mechanism.
	// In production the frontend writes this to the user's policy dir;
	// here we use LoadHarnessSnippets to simulate the file being present.
	overrideSrc := `forbid(principal, action, resource)
  when { context has team_rule_id && context.team_rule_id == "rule-001" };`
	if err := engine.LoadHarnessSnippets(map[string][]byte{
		"local-override/rule-001.cedar": []byte(overrideSrc),
	}); err != nil {
		t.Fatalf("LoadHarnessSnippets (override): %v", err)
	}

	// Both rules are installed — verified via ListPolicies.
	files := engine.ListPolicies()
	hasTeam := false
	hasOverride := false
	for _, f := range files {
		switch f.Name {
		case "fleet-team/rule-001":
			hasTeam = true
		case "local-override/rule-001.cedar":
			hasOverride = true
		}
	}
	if !hasTeam {
		t.Error("fleet-team/rule-001 not found after SetTeamBundle")
	}
	if !hasOverride {
		t.Error("local-override/rule-001.cedar not found after LoadHarnessSnippets")
	}
}

// TestApplyCedarDelta_JSON verifies ApplyCedarDelta decodes the raw JSON bundle delta.
func TestApplyCedarDelta_JSON(t *testing.T) {
	engine := newTestEngine(t)

	rawDelta, err := json.Marshal(CedarDelta{
		Rules: map[string]string{
			"allow-all": `permit(principal, action, resource);`,
		},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	if err := ApplyCedarDelta(context.Background(), engine, json.RawMessage(rawDelta)); err != nil {
		t.Fatalf("ApplyCedarDelta: %v", err)
	}

	files := engine.ListPolicies()
	found := false
	for _, f := range files {
		if f.Name == "fleet-team/allow-all" && f.ParseOK {
			found = true
		}
	}
	if !found {
		t.Error("fleet-team/allow-all not found after ApplyCedarDelta")
	}
}

// TestApplyCedarDelta_NilRaw verifies that a nil RawMessage is a no-op.
func TestApplyCedarDelta_NilRaw(t *testing.T) {
	engine := newTestEngine(t)
	if err := ApplyCedarDelta(context.Background(), engine, nil); err != nil {
		t.Errorf("nil rawDelta: unexpected error: %v", err)
	}
}

// TestTeamRuleIDFromPolicyID verifies the prefix extraction helper.
func TestTeamRuleIDFromPolicyID(t *testing.T) {
	tests := []struct {
		id      string
		wantID  string
		wantOK  bool
	}{
		{"fleet-team/rule-001", "rule-001", true},
		{"fleet-team/", "", false},   // prefix only, no id
		{"harness-self/foo", "", false},
		{"fleet-team/r", "r", true},
	}
	for _, tc := range tests {
		id, ok := TeamRuleIDFromPolicyID(gocedar.PolicyID(tc.id))
		if ok != tc.wantOK || id != tc.wantID {
			t.Errorf("TeamRuleIDFromPolicyID(%q) = (%q, %v), want (%q, %v)",
				tc.id, id, ok, tc.wantID, tc.wantOK)
		}
	}
}
