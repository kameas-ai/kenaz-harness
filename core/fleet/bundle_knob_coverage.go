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
// ModelPrefs starts as RegisterDeferred here — the copy into
// fleetState.fleetModelPrefs (fleet.go:740-743 at spec-authoring time)
// has no reader yet, per WP02's applier fix in the same commit, which
// makes a bundle carrying model_prefs fail the apply rather than ack
// clean. WP04 promotes it to Register once ProviderAllowlist and
// DefaultModel each reach an observable branch.
func init() {
	knobcoverage.Register[Bundle]("BundleID", "fleet.ConfigPoller monotonic replay guard + VerifyWithKeySet (core/fleet/config_pull.go, core/fleet/bundle.go) — envelope field, not a config section")
	knobcoverage.Register[Bundle]("IssuedAt", "part of the signed bundleSigningPayload (core/fleet/bundle.go) — envelope field, not a config section")
	knobcoverage.Register[Bundle]("CedarDelta", "compositeConfigApplier.ApplyBundle -> fleet.ApplyCedarDelta -> cedarpolicy.Engine.SetTeamBundle (core/rpc/views/settings/fleet.go, fleet-enforcement-truth-01PMZ505 WP02/WP03)")
	knobcoverage.Register[Bundle]("MCPAllowlist", "compositeConfigApplier.ApplyBundle -> recipes.ApplyFleetAllowlist -> globalAllowlist (core/mcp/recipes/allowlist.go)")
	knobcoverage.RegisterDeferred[Bundle]("ModelPrefs", "fleet-enforcement-truth-01PMZ505 WP02: copied into fleetState.fleetModelPrefs with no reader; WP02 makes an unconsumed section fail the apply instead of acking clean. WP04 promotes to Register once ProviderAllowlist/DefaultModel reach an observable branch in core/rpc/views/llm.")
	knobcoverage.Register[Bundle]("KameasMLWeightURLs", "deliberately NOT applied in-process — the fleet-hosted-LLM / kameas-ml surface was removed (harness-fleet-sync-activation-01NSYNC01); field retained only so signature verification of server-signed bundles still round-trips (core/rpc/views/settings/fleet.go type doc)")
	knobcoverage.Register[Bundle]("MandatedSkills", "compositeConfigApplier.ApplyBundle -> fleet.ApplyMandatedSkills (fleet-skills-sync-01NDFSEX18 WP05)")
	knobcoverage.Register[Bundle]("Signature", "fleet.VerifyWithKeySet (core/fleet/bundle.go) — envelope field, not a config section")
}
