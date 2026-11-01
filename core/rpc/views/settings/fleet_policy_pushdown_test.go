package settings

// fleet-policy push-down (2026-10-31): the composite applier installs
// cedar_delta as a full snapshot on EVERY bundle and mcp_allowlist with
// replace semantics, and the status views report what is in force.

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	gocedar "github.com/cedar-policy/cedar-go"
	"github.com/kameas-ai/kenaz-harness/core/fleet"
	"github.com/kameas-ai/kenaz-harness/core/mcp/recipes"
	cedarpolicy "github.com/kameas-ai/kenaz-harness/core/policy/cedar"
)

func TestCompositeApplier_CedarSnapshotAndAllowlistReplace(t *testing.T) {
	if recipes.AllowlistSealed() {
		t.Skip("global allow-list sealed (served mode) in this test binary")
	}
	t.Cleanup(func() { recipes.ApplyFleetAllowlist(nil) })

	engine, err := cedarpolicy.NewEngine(cedarpolicy.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := engine.SetPolicyText("base.cedar", []byte(`permit(principal, action, resource);`)); err != nil {
		t.Fatal(err)
	}
	a := &API{fleet: newFleetState()}
	a.SetCedarEngine(engine)
	applier := &compositeConfigApplier{state: a.fleet}
	outcome := func() cedarpolicy.Outcome {
		return engine.Evaluate(context.Background(), gocedar.EntityUID{}, cedarpolicy.ActionUseTool,
			cedarpolicy.ToolUID("github", "search"), nil).Outcome
	}
	apply := func(js string) []error {
		var b fleet.Bundle
		if err := json.Unmarshal([]byte(js), &b); err != nil {
			t.Fatal(err)
		}
		return applier.ApplyBundle(context.Background(), &b)
	}

	if errs := apply(`{"bundle_id":1,"cedar_delta":{"rules":{"r1":"forbid(principal, action == Action::\"use_tool\", resource);"}},"mcp_allowlist":["slack"]}`); len(errs) != 0 {
		t.Fatal(errs)
	}
	if outcome() != cedarpolicy.Deny || recipes.IsAllowed("github") {
		t.Fatal("bundle 1 not enforced")
	}
	st, _ := a.FleetConfigPullStatus(context.Background())
	if !st.FleetRulesActive || len(st.FleetCedarRules) != 1 || st.FleetCedarRules[0] != "r1" || !st.MCPAllowlistActive {
		t.Errorf("status after bundle 1 = %+v", st)
	}

	// Bundle 2 omits both sections: every fleet rule and restriction is gone.
	if errs := apply(`{"bundle_id":2}`); len(errs) != 0 {
		t.Fatal(errs)
	}
	if outcome() != cedarpolicy.Allow || !recipes.IsAllowed("github") {
		t.Fatal("cleared fleet policy still enforced")
	}
	h, _ := a.FleetHealth(context.Background())
	if h.FleetRulesActive || len(h.FleetCedarRules) != 0 || h.MCPAllowlistActive {
		t.Errorf("health after clear = %+v", h)
	}

	// Legacy Fleet shape (rule_text only) must be enforced, not ACKed-and-dropped.
	if errs := apply(`{"bundle_id":3,"cedar_delta":{"rule_text":"forbid(principal, action == Action::\"use_tool\", resource);","published_at":"2026-10-31T00:00:00Z"}}`); len(errs) != 0 {
		t.Fatal(errs)
	}
	if outcome() != cedarpolicy.Deny {
		t.Fatal("rule_text-only cedar_delta not enforced")
	}

	// Unparseable delta: apply error, previous rule stays.
	if errs := apply(`{"bundle_id":4,"cedar_delta":{"rules":{"bad":"forbid(principal"}}}`); len(errs) == 0 {
		t.Fatal("unparseable delta must be an apply error")
	}
	if outcome() != cedarpolicy.Deny {
		t.Fatal("bad delta dropped the previous rule")
	}
}

// A narrowing allow-list apply drives the enforcer, reports the blocked
// recipes in status, and turns a failed stop into an apply error.
func TestCompositeApplier_AllowlistNarrowingStopsRunningServers(t *testing.T) {
	if recipes.AllowlistSealed() {
		t.Skip("global allow-list sealed")
	}
	t.Cleanup(func() { recipes.ApplyFleetAllowlist(nil) })
	a := &API{fleet: newFleetState()}
	calls := 0
	fail := false
	a.SetMCPAllowlistEnforcer(func(context.Context) []MCPAllowlistStop {
		calls++
		if recipes.IsAllowed("slack") {
			return nil
		}
		r := MCPAllowlistStop{RecipeID: "slack", Stopped: !fail}
		if fail {
			r.Err = errors.New("boom")
		}
		return []MCPAllowlistStop{r}
	})
	applier := &compositeConfigApplier{state: a.fleet}
	apply := func(js string) []error {
		var b fleet.Bundle
		if err := json.Unmarshal([]byte(js), &b); err != nil {
			t.Fatal(err)
		}
		return applier.ApplyBundle(context.Background(), &b)
	}
	if errs := apply(`{"bundle_id":1,"mcp_allowlist":["github"]}`); len(errs) != 0 {
		t.Fatal(errs)
	}
	if calls != 1 {
		t.Fatalf("enforcer calls = %d", calls)
	}
	st, _ := a.FleetConfigPullStatus(context.Background())
	if len(st.MCPRecipesBlocked) != 1 || st.MCPRecipesBlocked[0] != "slack" {
		t.Errorf("blocked = %v", st.MCPRecipesBlocked)
	}
	fail = true
	if errs := apply(`{"bundle_id":2,"mcp_allowlist":["github"]}`); len(errs) != 1 {
		t.Fatalf("failed stop must be an apply error, got %v", errs)
	}
	// Widening clears the blocked list.
	if errs := apply(`{"bundle_id":3}`); len(errs) != 0 {
		t.Fatal(errs)
	}
	h, _ := a.FleetHealth(context.Background())
	if len(h.MCPRecipesBlocked) != 0 {
		t.Errorf("blocked after clear = %v", h.MCPRecipesBlocked)
	}
}
