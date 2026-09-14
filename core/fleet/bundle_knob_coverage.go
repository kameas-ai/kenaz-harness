package fleet

import "github.com/kameas-ai/kenaz-harness/core/wiring/knobcoverage"

// init registers every Bundle field with core/wiring/knobcoverage
// (fleet-enforcement-truth-01PMZ505 WP02, spec §7 G-3). Bundle is the
// wire shape a fleet config bundle uses to push policy, model
// governance and mandated skills onto an enrolled device
// (compositeConfigApplier.ApplyBundle, core/rpc/views/settings/fleet.go).
// Two of its sections — cedar_delta and model_prefs — shipped with no
// real consumer for a release cycle before being found by hand (spec
// §1.1, §1.2); this registration is the mechanism that makes "this
// section reaches a branch" a claim CI can check instead of a claim in
// a PR description, mirroring core/agentgraph/knob_coverage.go's
// precedent for ModelAttrs.
//
// ModelPrefs was RegisterDeferred as of WP02 (copied into
// fleetState.fleetModelPrefs with no reader). WP04 promotes it here:
// llmview.ApplyFleetModelPrefs (core/rpc/views/llm/fleet_prefs.go) is
// the real consumer — ListProviders filters on it, StartStream blocks
// an excluded profile, and profileKindAndModel seeds DefaultModel (D-3).
func init() {
	knobcoverage.Register[Bundle]("BundleID", "fleet.ConfigPoller monotonic replay guard + VerifyWithKeySet (core/fleet/config_pull.go, core/fleet/bundle.go) — envelope field, not a config section")
	knobcoverage.Register[Bundle]("IssuedAt", "part of the signed bundleSigningPayload (core/fleet/bundle.go) — envelope field, not a config section")
	knobcoverage.Register[Bundle]("CedarDelta", "compositeConfigApplier.ApplyBundle -> fleet.ApplyCedarDelta -> cedarpolicy.Engine.SetTeamBundle (core/rpc/views/settings/fleet.go, fleet-enforcement-truth-01PMZ505 WP02/WP03)")
	knobcoverage.Register[Bundle]("MCPAllowlist", "compositeConfigApplier.ApplyBundle -> recipes.ApplyFleetAllowlist -> globalAllowlist (core/mcp/recipes/allowlist.go)")
	knobcoverage.Register[Bundle]("ModelPrefs", "compositeConfigApplier.ApplyBundle -> llmview.ApplyFleetModelPrefs -> llm.API.ListProviders (filter) + llm.API.StartStream (block) + llm.API.profileKindAndModel (seed DefaultModel, D-3) — fleet-enforcement-truth-01PMZ505 WP04")
	knobcoverage.Register[Bundle]("KameasMLWeightURLs", "deliberately NOT applied in-process — the fleet-hosted-LLM / kameas-ml surface was removed (harness-fleet-sync-activation-01NSYNC01); field retained only so signature verification of server-signed bundles still round-trips (core/rpc/views/settings/fleet.go type doc)")
	knobcoverage.Register[Bundle]("MandatedSkills", "compositeConfigApplier.ApplyBundle -> fleet.ApplyMandatedSkills (fleet-skills-sync-01NDFSEX18 WP05)")
	knobcoverage.Register[Bundle]("ProvisionedMCP", "compositeConfigApplier.ApplyBundle -> recipes.ApplyProvisionedMCP -> MergedCatalog org-layer overlay (core/rpc/views/settings/fleet.go, core/mcp/recipes/org.go+merged.go, fleet-org-config-inheritance-01NORGX01 WP02) — an org-provisioned recipe wins over shipped/registry/user in every OAuth sign-in call site that resolves through MergedCatalog.Get (e.g. core/rpc/views/tools/oauth.go's recipe.Auth.ClientID read), which is also this mission's WP03 client_id-resolution requirement, satisfied by the merge precedence itself rather than separate resolution code")
	knobcoverage.RegisterDeferred[Bundle]("ProviderSetups", "wire contract only as of fleet-org-config-inheritance-01NORGX01 WP01 (core/fleet/bundle.go). Apply pipeline (LLM stack wiring, org-shared-key credstore delivery) is WP04, explicitly gated by kitty-specs/fleet-org-config-inheritance-01NORGX01/plan.md's Gates section: \"no harness WP04 merge before a fleet dev environment can exercise it\" — kenaz-fleet org endpoints AND the dedicated encrypted org-key channel it requires do not exist yet (ruled kitty-specs/fleet-org-config-inheritance-01NORGX01/spec.md 2026-08-19, blocker unchanged as of 2026-09-12). Owner: alec. Blocker: kenaz-fleet org endpoints + encrypted org-key channel. See docs/unwired-ledger.md.")
	knobcoverage.Register[Bundle]("OrgConfig", "compositeConfigApplier.ApplyBundle -> fleet.KindRegistry.Kind(id) -> SyncKind.Apply(ctx, ScopeOrg, payload) (core/rpc/views/settings/fleet.go, fleet-generic-sync-framework-01NSYNC02 WP02) — dispatches each keyed entry to its registered kind; an id with no registration is a logged skip (forward-compat), a registered kind that cannot accept ScopeOrg is a collected apply error")
	knobcoverage.Register[Bundle]("Signature", "fleet.VerifyWithKeySet (core/fleet/bundle.go) — envelope field, not a config section")
}
