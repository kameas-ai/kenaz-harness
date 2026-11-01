package fleet

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	gocedar "github.com/cedar-policy/cedar-go"
	cedarpolicy "github.com/kameas-ai/kenaz-harness/core/policy/cedar"
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

// ── Snapshot-replace (fleet-policy push-down, contract #1) ──────────────────

const (
	permitAllCedar   = `permit(principal, action, resource);`
	forbidGithubTool = `forbid(principal, action == Action::"use_tool", resource == Tool::"github__search");`
)

// baseEngine is an engine whose base set permits everything, so a fleet
// forbid is observable as Allow → Deny.
func baseEngine(t *testing.T) *cedarpolicy.Engine {
	t.Helper()
	e := newTestEngine(t)
	if err := e.SetPolicyText("base.cedar", []byte(permitAllCedar)); err != nil {
		t.Fatal(err)
	}
	return e
}

func githubOutcome(e *cedarpolicy.Engine) cedarpolicy.Outcome {
	return e.Evaluate(context.Background(), gocedar.EntityUID{}, cedarpolicy.ActionUseTool,
		cedarpolicy.ToolUID("github", "search"), nil).Outcome
}

func applyRaw(t *testing.T, e *cedarpolicy.Engine, raw string) error {
	t.Helper()
	var rm json.RawMessage
	if raw != "" {
		rm = json.RawMessage(raw)
	}
	return ApplyCedarDelta(context.Background(), e, rm)
}

func deltaJSON(t *testing.T, rules map[string]string) string {
	t.Helper()
	b, err := json.Marshal(map[string]any{"rules": rules, "published_at": "2026-10-31T00:00:00Z"})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestApplyCedarDelta_SnapshotReplace(t *testing.T) {
	e := baseEngine(t)
	if got := githubOutcome(e); got != cedarpolicy.Allow {
		t.Fatalf("baseline = %v, want allow", got)
	}

	// Publish a forbid → Deny.
	if err := applyRaw(t, e, deltaJSON(t, map[string]string{"r1": forbidGithubTool})); err != nil {
		t.Fatal(err)
	}
	if got := githubOutcome(e); got != cedarpolicy.Deny {
		t.Fatalf("after forbid publish = %v, want deny", got)
	}
	if got := ActiveFleetCedarRules(e); len(got) != 1 || got[0] != "r1" {
		t.Fatalf("ActiveFleetCedarRules = %v", got)
	}

	// Republish WITHOUT it (new key, as Fleet does on every publish) → Allow.
	if err := applyRaw(t, e, deltaJSON(t, map[string]string{"r2": `permit(principal, action == Action::"use_tool", resource);`})); err != nil {
		t.Fatal(err)
	}
	if got := githubOutcome(e); got != cedarpolicy.Allow {
		t.Fatalf("after republish without forbid = %v, want allow (rule removed in Fleet still enforced)", got)
	}
	if got := ActiveFleetCedarRules(e); len(got) != 1 || got[0] != "r2" {
		t.Fatalf("ActiveFleetCedarRules after republish = %v, want [r2]", got)
	}

	// Re-add, then clear via absent cedar_delta → Allow, no fleet rules.
	if err := applyRaw(t, e, deltaJSON(t, map[string]string{"r3": forbidGithubTool})); err != nil {
		t.Fatal(err)
	}
	if err := applyRaw(t, e, ""); err != nil {
		t.Fatal(err)
	}
	if got := githubOutcome(e); got != cedarpolicy.Allow {
		t.Fatalf("after clear = %v, want allow", got)
	}
	if got := ActiveFleetCedarRules(e); len(got) != 0 {
		t.Fatalf("fleet rules after clear = %v", got)
	}
	for _, f := range e.ListPolicies() {
		if strings.HasPrefix(f.Name, "fleet-team/") {
			t.Errorf("listing still shows %s after clear", f.Name)
		}
	}

	// JSON null clears too.
	if err := applyRaw(t, e, deltaJSON(t, map[string]string{"r4": forbidGithubTool})); err != nil {
		t.Fatal(err)
	}
	if err := applyRaw(t, e, "null"); err != nil {
		t.Fatal(err)
	}
	if got := githubOutcome(e); got != cedarpolicy.Allow {
		t.Fatalf("after null delta = %v, want allow", got)
	}
}

// A rule that shrinks under the SAME key (fewer statements) must not leave
// stale #n policies behind.
func TestApplyCedarDelta_ShrunkRuleSameKey(t *testing.T) {
	e := baseEngine(t)
	two := forbidGithubTool + "\n" + `forbid(principal, action == Action::"use_tool", resource == Tool::"slack__post");`
	if err := applyRaw(t, e, deltaJSON(t, map[string]string{"r": two})); err != nil {
		t.Fatal(err)
	}
	if err := applyRaw(t, e, deltaJSON(t, map[string]string{"r": `permit(principal, action, resource);`})); err != nil {
		t.Fatal(err)
	}
	if got := githubOutcome(e); got != cedarpolicy.Allow {
		t.Fatalf("shrunk rule left a stale forbid: %v", got)
	}
}

func TestApplyCedarDelta_BadDeltaKeepsPreviousRules(t *testing.T) {
	e := baseEngine(t)
	if err := applyRaw(t, e, deltaJSON(t, map[string]string{"r1": forbidGithubTool})); err != nil {
		t.Fatal(err)
	}
	// One valid + one unparseable rule: whole apply refused.
	bad := deltaJSON(t, map[string]string{"ok": permitAllCedar, "bad": `permit(principal, action`})
	if err := applyRaw(t, e, bad); err == nil {
		t.Fatal("unparseable delta: want error")
	}
	if got := githubOutcome(e); got != cedarpolicy.Deny {
		t.Fatalf("after bad delta = %v, want deny (previous rule must stay enforced)", got)
	}
	if got := ActiveFleetCedarRules(e); len(got) != 1 || got[0] != "r1" {
		t.Fatalf("fleet rules after bad delta = %v, want [r1]", got)
	}
	// Undecodable JSON: same.
	if err := applyRaw(t, e, `{"rules": 7}`); err == nil {
		t.Fatal("undecodable delta: want error")
	}
	if got := githubOutcome(e); got != cedarpolicy.Deny {
		t.Fatalf("after undecodable delta = %v, want deny", got)
	}
}

// Regression (coordinator addendum 2026-10-31): prod Fleet emits
// {"rule_text": ..., "published_at": ...} with no "rules" map. Before the
// fix that decoded to zero rules → no-op → ACKed applied while nothing was
// enforced. The harness now applies rule_text as the single rule.
func TestApplyCedarDelta_LegacyRuleTextOnly(t *testing.T) {
	e := baseEngine(t)
	raw, _ := json.Marshal(map[string]string{"rule_text": forbidGithubTool, "published_at": "2026-10-31T00:00:00Z"})
	if err := applyRaw(t, e, string(raw)); err != nil {
		t.Fatal(err)
	}
	if got := githubOutcome(e); got != cedarpolicy.Deny {
		t.Fatalf("rule_text-only delta not enforced: %v", got)
	}
	// rules wins over rule_text when both are present.
	both, _ := json.Marshal(map[string]any{"rules": map[string]string{"k": permitAllCedar}, "rule_text": forbidGithubTool})
	if err := applyRaw(t, e, string(both)); err != nil {
		t.Fatal(err)
	}
	if got := githubOutcome(e); got != cedarpolicy.Allow {
		t.Fatalf("rule_text must be ignored when rules is present: %v", got)
	}
	// An unparseable rule_text is an apply error, not a silent success.
	badLegacy, _ := json.Marshal(map[string]string{"rule_text": "permit(principal"})
	if err := applyRaw(t, e, string(badLegacy)); err == nil {
		t.Fatal("unparseable rule_text: want error")
	}
}

// Fleet snapshot replace must not disturb other harness-self snippets.
func TestApplyCedarDelta_LeavesOtherSnippets(t *testing.T) {
	e := baseEngine(t)
	if err := e.LoadHarnessSnippets(map[string][]byte{"harness_write_forbid.cedar": []byte(forbidGithubTool)}); err != nil {
		t.Fatal(err)
	}
	if err := applyRaw(t, e, deltaJSON(t, map[string]string{"r": permitAllCedar})); err != nil {
		t.Fatal(err)
	}
	if err := applyRaw(t, e, ""); err != nil {
		t.Fatal(err)
	}
	if got := githubOutcome(e); got != cedarpolicy.Deny {
		t.Fatalf("clearing fleet rules removed a non-fleet snippet: %v", got)
	}
}

// Race: applies swap the set while Evaluate runs (go test -race).
func TestApplyCedarDelta_ConcurrentEvaluate(t *testing.T) {
	e := baseEngine(t)
	var wg sync.WaitGroup
	stop := make(chan struct{})
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					_ = githubOutcome(e)
				}
			}
		}()
	}
	for i := 0; i < 50; i++ {
		rules := map[string]string{fmt.Sprintf("r%d", i): forbidGithubTool}
		if i%2 == 1 {
			rules = nil
		}
		if err := applyRaw(t, e, deltaJSON(t, rules)); err != nil {
			t.Fatal(err)
		}
		_ = e.LoadHarnessSnippets(map[string][]byte{"other.cedar": []byte(permitAllCedar)})
	}
	close(stop)
	wg.Wait()
}
