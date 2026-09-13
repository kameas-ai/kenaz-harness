// Package recipes — org.go
//
// Org-provisioned MCP recipe layer (fleet-org-config-inheritance-01NORGX01
// WP02). An IT admin pushes a signed ProvisionedMCP entry through the
// fleet ConfigBundle; ApplyProvisionedMCP turns each entry into a Recipe
// that wins over every other source in MergedCatalog (org_wins_readonly —
// spec §2 rule 3, mirroring the mandated_skills precedent at FR-301). The
// member's own recipe of the same ID is never mutated or deleted — only
// the merged/effective view is shadowed (see merged.go's Recipes() doc).
//
// This file deliberately does NOT import core/fleet: core/fleet already
// imports core/mcp/recipes (core/fleet/sites_reconciler.go), so a reverse
// import would cycle — the same reason ApplyFleetAllowlist takes a plain
// []string instead of a fleet type. ProvisionedMCPEntry mirrors
// fleet.ProvisionedMCP's wire shape field-for-field (json tags included so
// it round-trips an org_config["mcp_recipes"] payload unmarshal directly);
// core/rpc/views/settings/fleet.go converts fleet.ProvisionedMCP to this
// type for the bespoke-bundle-field call path.
package recipes

import (
	"encoding/json"
	"fmt"
)

// ProvisionedMCPEntry is the recipes-local mirror of fleet.ProvisionedMCP.
// See that type's doc for the security rationale — no field here can ever
// carry credential bytes (FR-008): ClientID is a public PKCE client id,
// never a bearer/bot token, and Config is restricted by contract (not
// code — see the FR-008 negative test) to non-secret config_options
// overrides.
type ProvisionedMCPEntry struct {
	RecipeID      string          `json:"recipe_id"`
	Transport     string          `json:"transport,omitempty"`
	URL           string          `json:"url,omitempty"`
	PrimaryAuth   string          `json:"primary_auth,omitempty"`
	OAuthClientID string          `json:"oauth_client_id,omitempty"`
	OAuthScopes   []string        `json:"oauth_scopes,omitempty"`
	Config        json.RawMessage `json:"config,omitempty"`
}

// ApplyProvisionedMCP computes the org-provisioned recipe overlay for cat
// from entries and installs it as cat's org layer via SetOrgRecipes
// (highest precedence — ConflictPolicyOrgWinsReadonly).
//
// Each entry is layered onto the current merged recipe for RecipeID
// (preserving DisplayName/Description/Category/EnvKeys/Capabilities/etc.
// from whichever of shipped/registry/user already defines that ID) so an
// org entry can override just transport/URL/auth for an existing recipe —
// the common case (spec §3.1's "slack" example). When RecipeID matches
// nothing in the pre-org merged view, a minimal recipe is synthesized
// from the entry alone (DisplayName defaults to RecipeID) so an org-only
// internal server the harness has never shipped a recipe for is still
// representable rather than silently dropped.
//
// Returns one error per malformed entry (empty RecipeID) — all other
// well-formed entries are still applied, matching the partial-success
// pattern used by ApplyMandatedSkills and every other ApplyBundle
// section. cat is required; a nil catalog is a caller bug and returns a
// single error without touching anything.
//
// Called from two wire entry points that must never diverge in behavior:
//   - compositeConfigApplier.ApplyBundle when Bundle.ProvisionedMCP is
//     non-empty (core/rpc/views/settings/fleet.go) — today's live wire
//     shape (fleet-org-config-inheritance-01NORGX01 WP01).
//   - the "mcp_recipes" SyncKind's ScopeOrg apply closure
//     (core/rpc/sync_categories.go), reached when a fleet-generic-sync-
//     framework-01NSYNC02 org_config["mcp_recipes"] entry arrives. Both
//     call sites route through this single function so migrating the
//     wire shape later never means maintaining two implementations of
//     "what an org-provisioned recipe overlay does."
func ApplyProvisionedMCP(cat *MergedCatalog, entries []ProvisionedMCPEntry) []error {
	if cat == nil {
		return []error{fmt.Errorf("recipes: ApplyProvisionedMCP: nil catalog")}
	}
	var errs []error
	out := make([]Recipe, 0, len(entries))
	for _, e := range entries {
		if e.RecipeID == "" {
			errs = append(errs, fmt.Errorf("recipes: provisioned_mcp entry with empty recipe_id"))
			continue
		}
		// Look up the pre-org merged view so an org entry can override
		// just the fields it names while preserving everything else the
		// base recipe already declares (DisplayName, Description,
		// Category, EnvKeys, Capabilities, ...). Get() reads cat.Recipes(),
		// which at this point still reflects the PREVIOUS org overlay (not
		// yet swapped in) — safe because we only overwrite cat.org at the
		// end via SetOrgRecipes, after every entry has been computed
		// against a stable base.
		base, _ := cat.Get(e.RecipeID)
		r := base
		r.ID = e.RecipeID
		if r.DisplayName == "" {
			r.DisplayName = e.RecipeID
		}
		if e.Transport != "" {
			r.Transport = e.Transport
		}
		if e.URL != "" {
			r.URL = e.URL
		}
		if e.PrimaryAuth != "" {
			r.PrimaryAuth = e.PrimaryAuth
		}
		if e.OAuthClientID != "" {
			// Org client_id always wins: this is spec §3.3's resolution
			// order step 1 ("fleet-provisioned"), realized by construction
			// — the org Recipe (with this Auth) is the one every OAuth
			// sign-in call site resolves via MergedCatalog.Get, so no
			// separate client_id-resolution code is needed (see
			// core/rpc/views/tools/oauth.go:404's recipe.Auth.ClientID
			// read, which is exactly this field).
			r.Auth = &RecipeAuth{
				Kind:     AuthKindMCPOAuth,
				ClientID: e.OAuthClientID,
				Scopes:   e.OAuthScopes,
			}
			if r.PrimaryAuth == "" {
				r.PrimaryAuth = PrimaryAuthOAuth
			}
		}
		r.Source = SourceOrg
		out = append(out, r)
	}
	cat.SetOrgRecipes(out)
	return errs
}
