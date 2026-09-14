package settings

// fleet-org-config-inheritance-01NORGX01 WP02.
//
// Before this WP, Bundle.ProvisionedMCP was signed and transmitted
// (WP01) but compositeConfigApplier.ApplyBundle had ZERO branch for it —
// a bundle carrying a provisioned_mcp section ACKed "applied:true" while
// silently doing nothing (docs/unwired-ledger.md). These tests pin the
// real apply pipeline: an org-provisioned entry becomes the
// highest-precedence recipe in the shared MergedCatalog, a nil catalog
// makes a non-empty section a named apply error (not a silent skip),
// and an empty section (the org's current, now-zero, complete set)
// clears any previously-applied overlay rather than leaving it stale.
//
// Test rule (spec §8 rule 4, same as fleet_wp02_test.go): the fixture
// constructs the seam the way production does — a zero-value fleetState
// wired only via the public setters (SetMCPCatalog), not a hand-built
// internal struct with extra fields poked in.

import (
	"context"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/fleet"
	"github.com/kameas-ai/kenaz-harness/core/mcp/recipes"
)

func TestApplyBundle_ProvisionedMCP_InstallsOrgRecipe(t *testing.T) {
	cat := recipes.NewMergedCatalog(
		func() []recipes.Recipe {
			return []recipes.Recipe{{ID: "slack", Source: recipes.SourceShipped, DisplayName: "Slack", Command: []string{"x"}}}
		},
		nil, nil,
	)
	api := &API{}
	api.SetMCPCatalog(cat)
	applier := &compositeConfigApplier{state: api.fleet}

	b := &fleet.Bundle{
		BundleID: 1,
		ProvisionedMCP: []fleet.ProvisionedMCP{{
			RecipeID:    "slack",
			Transport:   "http",
			URL:         "https://mcp.slack.com",
			PrimaryAuth: "oauth",
			OAuth: &fleet.ProvisionedMCPOAuth{
				ClientID: "org-client-id",
				Scopes:   []string{"chat:write"},
			},
		}},
	}
	errs := applier.ApplyBundle(context.Background(), b)
	if len(errs) != 0 {
		t.Fatalf("ApplyBundle errs = %v, want none", errs)
	}

	r, ok := cat.Get("slack")
	if !ok {
		t.Fatal("slack recipe not found after apply")
	}
	if r.Source != recipes.SourceOrg {
		t.Errorf("Source = %q, want org", r.Source)
	}
	if r.Auth == nil || r.Auth.ClientID != "org-client-id" {
		t.Fatalf("Auth = %+v, want ClientID=org-client-id", r.Auth)
	}
}

// A non-empty provisioned_mcp section with no catalog wired must fail the
// apply, not silently discard a signed org config — mirrors
// TestApplyBundle_CedarDeltaUnwiredEngine_Fails / MandatedSkillsUnwiredRefs
// in fleet_wp02_test.go for this mission's new section.
func TestApplyBundle_ProvisionedMCP_UnwiredCatalog_Fails(t *testing.T) {
	applier := &compositeConfigApplier{state: &fleetState{}}
	b := &fleet.Bundle{
		BundleID:       1,
		ProvisionedMCP: []fleet.ProvisionedMCP{{RecipeID: "slack"}},
	}
	errs := applier.ApplyBundle(context.Background(), b)
	if len(errs) == 0 {
		t.Fatal("expected a non-empty error slice for an unwired MCP catalog; got none")
	}
}

// An empty (or absent) provisioned_mcp section on an unwired catalog is
// NOT an error — there is nothing to apply either way, and an OSS/test
// harness that never carries this section must not fail every apply.
func TestApplyBundle_ProvisionedMCPAbsent_UnwiredCatalog_CleanApply(t *testing.T) {
	applier := &compositeConfigApplier{state: &fleetState{}}
	b := &fleet.Bundle{BundleID: 1, MCPAllowlist: []string{"github"}}
	errs := applier.ApplyBundle(context.Background(), b)
	if len(errs) != 0 {
		t.Fatalf("expected a clean apply, got errors: %v", errs)
	}
}

// The org's bundle carries its CURRENT complete provisioned_mcp set, not a
// delta (Bundle.ProvisionedMCP's own doc: nil/empty/absent are
// equivalent). A subsequent bundle with zero entries must clear whatever a
// prior bundle provisioned — otherwise de-provisioning an entry can never
// actually take effect on the member's device.
func TestApplyBundle_ProvisionedMCP_SubsequentEmptyBundleClearsOverlay(t *testing.T) {
	cat := recipes.NewMergedCatalog(nil, nil, nil)
	api := &API{}
	api.SetMCPCatalog(cat)
	applier := &compositeConfigApplier{state: api.fleet}

	first := &fleet.Bundle{
		BundleID:       1,
		ProvisionedMCP: []fleet.ProvisionedMCP{{RecipeID: "slack", URL: "https://mcp.slack.com"}},
	}
	if errs := applier.ApplyBundle(context.Background(), first); len(errs) != 0 {
		t.Fatalf("first apply errs = %v, want none", errs)
	}
	if _, ok := cat.Get("slack"); !ok {
		t.Fatal("precondition: slack should be provisioned after the first bundle")
	}

	second := &fleet.Bundle{BundleID: 2} // org removed the entry
	if errs := applier.ApplyBundle(context.Background(), second); len(errs) != 0 {
		t.Fatalf("second apply errs = %v, want none", errs)
	}
	if _, ok := cat.Get("slack"); ok {
		t.Error("slack still provisioned after a subsequent bundle stopped carrying it — de-provisioning never took effect")
	}
}

// StopFleetBackground (sign-out) must clear the org overlay, matching the
// spec §5 success criterion "removing fleet / signing out cleanly reverts
// to local-only" and mirroring the existing llmview.ClearFleetModelPrefs
// call in the same function.
func TestStopFleetBackground_ClearsOrgProvisionedRecipes(t *testing.T) {
	cat := recipes.NewMergedCatalog(
		nil, nil,
		func() []recipes.Recipe {
			return []recipes.Recipe{{ID: "slack", Source: recipes.SourceUser, DisplayName: "My Slack", Command: []string{"x"}}}
		},
	)
	api := &API{}
	api.SetMCPCatalog(cat)
	recipes.ApplyProvisionedMCP(cat, []recipes.ProvisionedMCPEntry{{RecipeID: "slack", URL: "https://mcp.slack.com"}})

	r, _ := cat.Get("slack")
	if r.Source != recipes.SourceOrg {
		t.Fatalf("precondition: org should be winning, got Source=%q", r.Source)
	}

	api.StopFleetBackground()

	r, ok := cat.Get("slack")
	if !ok || r.Source != recipes.SourceUser || r.DisplayName != "My Slack" {
		t.Errorf("after StopFleetBackground, Get(slack) = %+v, %v — want the member's own recipe restored", r, ok)
	}
}
