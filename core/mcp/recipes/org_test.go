package recipes_test

import (
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/mcp/recipes"
)

// TestApplyProvisionedMCP_OverridesExistingRecipe proves the spec §3.1
// "slack" example: an org entry naming an existing recipe overrides only
// the fields it sets (transport, url, primary_auth, oauth client_id) while
// preserving everything else (DisplayName, Category, EnvKeys, ...) from
// whichever source already defines that recipe ID — the org admin doesn't
// have to re-declare a recipe from scratch to add an org client_id.
//
// PrimaryAuth is asserted as PrimaryAuthBrowserOAuthPKCE, not the wire
// literal "oauth" the entry sets: that literal is spec.md §3.1's own
// example value, and copying it straight through used to land every
// org-provisioned OAuth recipe on SignInRecipe's unconditional E-006
// fail-closed arm despite carrying a perfectly good client id (WP03 —
// see TestApplyProvisionedMCP_PrimaryAuthOAuthResolvesToWorkingArm below
// for the SignInRecipe-level proof).
func TestApplyProvisionedMCP_OverridesExistingRecipe(t *testing.T) {
	mc := recipes.NewMergedCatalog(
		func() []recipes.Recipe {
			return []recipes.Recipe{{
				ID:          "slack",
				Source:      recipes.SourceShipped,
				DisplayName: "Slack",
				Category:    "communication",
				Command:     []string{"slack-mcp"},
			}}
		},
		nil, nil,
	)

	errs := recipes.ApplyProvisionedMCP(mc, []recipes.ProvisionedMCPEntry{{
		RecipeID:      "slack",
		Transport:     "http",
		URL:           "https://mcp.slack.com",
		PrimaryAuth:   "oauth",
		OAuthClientID: "org-public-client-id",
		OAuthScopes:   []string{"channels:read", "chat:write"},
	}})
	if len(errs) != 0 {
		t.Fatalf("ApplyProvisionedMCP errs = %v, want none", errs)
	}

	r, ok := mc.Get("slack")
	if !ok {
		t.Fatal("Get(slack) not found after apply")
	}
	if r.Source != recipes.SourceOrg {
		t.Errorf("Source = %q, want org", r.Source)
	}
	// Overridden fields. PrimaryAuth is remapped from the wire literal
	// "oauth" to the working browser_oauth_pkce arm (WP03) — see the
	// doc comment above.
	if r.Transport != "http" || r.URL != "https://mcp.slack.com" || r.PrimaryAuth != recipes.PrimaryAuthBrowserOAuthPKCE {
		t.Errorf("override fields not applied: transport=%q url=%q primary_auth=%q", r.Transport, r.URL, r.PrimaryAuth)
	}
	if r.Auth == nil || r.Auth.ClientID != "org-public-client-id" {
		t.Fatalf("Auth = %+v, want ClientID=org-public-client-id", r.Auth)
	}
	if len(r.Auth.Scopes) != 2 || r.Auth.Scopes[0] != "channels:read" {
		t.Errorf("Auth.Scopes = %v, want [channels:read chat:write]", r.Auth.Scopes)
	}
	// Preserved fields from the shipped base.
	if r.DisplayName != "Slack" || r.Category != "communication" {
		t.Errorf("base fields not preserved: DisplayName=%q Category=%q", r.DisplayName, r.Category)
	}
}

// TestApplyProvisionedMCP_UnknownRecipeIDSynthesizesMinimalRecipe proves an
// org-only internal server the harness has never shipped a recipe for is
// still representable, not silently dropped.
func TestApplyProvisionedMCP_UnknownRecipeIDSynthesizesMinimalRecipe(t *testing.T) {
	mc := recipes.NewMergedCatalog(nil, nil, nil)
	errs := recipes.ApplyProvisionedMCP(mc, []recipes.ProvisionedMCPEntry{{
		RecipeID:  "acme-internal-tools",
		Transport: "http",
		URL:       "https://mcp.acme.internal",
	}})
	if len(errs) != 0 {
		t.Fatalf("errs = %v, want none", errs)
	}
	r, ok := mc.Get("acme-internal-tools")
	if !ok {
		t.Fatal("unknown recipe_id was dropped instead of synthesized")
	}
	if r.DisplayName != "acme-internal-tools" {
		t.Errorf("DisplayName = %q, want the recipe_id as a fallback label", r.DisplayName)
	}
	if r.URL != "https://mcp.acme.internal" || r.Transport != "http" {
		t.Errorf("synthesized recipe missing entry fields: %+v", r)
	}
}

// TestApplyProvisionedMCP_EmptyRecipeIDIsAnErrorNotSilentlyDropped ensures a
// malformed entry surfaces as a returned error (FR-012-style partial-success
// pattern) rather than vanishing without a trace.
func TestApplyProvisionedMCP_EmptyRecipeIDIsAnErrorNotSilentlyDropped(t *testing.T) {
	mc := recipes.NewMergedCatalog(nil, nil, nil)
	errs := recipes.ApplyProvisionedMCP(mc, []recipes.ProvisionedMCPEntry{
		{RecipeID: ""},
		{RecipeID: "good-one", URL: "https://example.com"},
	})
	if len(errs) != 1 {
		t.Fatalf("errs = %v, want exactly 1 (the empty recipe_id)", errs)
	}
	// The well-formed entry alongside the bad one must still apply.
	if _, ok := mc.Get("good-one"); !ok {
		t.Error("good-one was not applied even though only the OTHER entry was malformed")
	}
}

// TestApplyProvisionedMCP_NilCatalogIsAnError guards the caller-bug path:
// a nil catalog must never look like a silent no-op success.
func TestApplyProvisionedMCP_NilCatalogIsAnError(t *testing.T) {
	errs := recipes.ApplyProvisionedMCP(nil, []recipes.ProvisionedMCPEntry{{RecipeID: "x"}})
	if len(errs) != 1 {
		t.Fatalf("errs = %v, want exactly 1 (nil catalog)", errs)
	}
}

// TestApplyProvisionedMCP_DefaultPrimaryAuthAlsoResolvesToWorkingArm proves
// the same WP03 fix applies to an org entry that leaves primary_auth unset
// entirely (not just the spec's literal "oauth" example): before the fix,
// the un-set-primary_auth default ALSO landed on the dead recipes.PrimaryAuthOAuth
// arm the instant a client id was present, which was the more common case
// (most org admins never set primary_auth explicitly — the field exists to
// let it be overridden, not required).
func TestApplyProvisionedMCP_DefaultPrimaryAuthAlsoResolvesToWorkingArm(t *testing.T) {
	mc := recipes.NewMergedCatalog(nil, nil, nil)
	errs := recipes.ApplyProvisionedMCP(mc, []recipes.ProvisionedMCPEntry{{
		RecipeID:      "github",
		URL:           "https://mcp.github.example.com",
		OAuthClientID: "org-public-client-id",
	}})
	if len(errs) != 0 {
		t.Fatalf("errs = %v, want none", errs)
	}
	r, ok := mc.Get("github")
	if !ok {
		t.Fatal("Get(github) not found after apply")
	}
	if r.PrimaryAuth != recipes.PrimaryAuthBrowserOAuthPKCE {
		t.Errorf("PrimaryAuth = %q, want %q (mutation: reverting to the old PrimaryAuthOAuth default must fail this)",
			r.PrimaryAuth, recipes.PrimaryAuthBrowserOAuthPKCE)
	}
}

// TestApplyProvisionedMCP_ExplicitNonOAuthPrimaryAuthIsLeftAlone proves the
// WP03 remap is scoped to the "oauth"/unset case only: an org admin who
// deliberately names a different working arm (e.g. browser_oauth_dcr) is
// not silently overridden.
func TestApplyProvisionedMCP_ExplicitNonOAuthPrimaryAuthIsLeftAlone(t *testing.T) {
	mc := recipes.NewMergedCatalog(nil, nil, nil)
	errs := recipes.ApplyProvisionedMCP(mc, []recipes.ProvisionedMCPEntry{{
		RecipeID:      "vercel",
		URL:           "https://mcp.vercel.example.com",
		PrimaryAuth:   "browser_oauth_dcr",
		OAuthClientID: "org-public-client-id",
	}})
	if len(errs) != 0 {
		t.Fatalf("errs = %v, want none", errs)
	}
	r, ok := mc.Get("vercel")
	if !ok {
		t.Fatal("Get(vercel) not found after apply")
	}
	if r.PrimaryAuth != recipes.PrimaryAuthBrowserOAuthDCR {
		t.Errorf("PrimaryAuth = %q, want the org-declared %q left untouched", r.PrimaryAuth, recipes.PrimaryAuthBrowserOAuthDCR)
	}
}

// TestApplyProvisionedMCP_EmptyEntriesClearsOverlay proves ApplyBundle's
// "the section carries the org's CURRENT complete set, not a delta"
// contract at the recipes-layer function itself: calling with zero entries
// after a previous non-empty apply clears the overlay rather than leaving
// a stale org recipe behind (the exact "signed removal never applies"
// shape the mission exists to close).
func TestApplyProvisionedMCP_EmptyEntriesClearsOverlay(t *testing.T) {
	mc := recipes.NewMergedCatalog(nil, nil, nil)
	recipes.ApplyProvisionedMCP(mc, []recipes.ProvisionedMCPEntry{{RecipeID: "slack", URL: "https://mcp.slack.com"}})
	if _, ok := mc.Get("slack"); !ok {
		t.Fatal("precondition: slack should be provisioned")
	}

	errs := recipes.ApplyProvisionedMCP(mc, nil)
	if len(errs) != 0 {
		t.Fatalf("errs = %v, want none", errs)
	}
	if _, ok := mc.Get("slack"); ok {
		t.Error("slack still present after the org de-provisioned it (empty entries) — stale org config never expires")
	}
}
