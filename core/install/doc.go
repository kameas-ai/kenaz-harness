// Package install is the one install framework (install-framework-01DOGF0B,
// FR-3): every way a capability reaches this device — an MCP recipe, a
// skill, a workflow, a bundle, an agent pack — is a Provider registered
// with one Framework, and every install goes through Framework.Install.
//
// What the framework guarantees, for every provider, so no provider has to
// remember it:
//
//   - Verify runs before Install. A provider reports what verification
//     applies to the item (builtin / local / signature); for a signed fleet
//     payload the framework runs its single SignatureVerifier — the one hook
//     register C-2's per-device key lookup lands in, once, for every kind.
//   - No install reports success unless the consumer sees it (FR-1, by
//     construction). After Provider.Install returns, the framework asks
//     Provider.InstalledState — which every provider computes from its
//     runtime consumer (the MCP supervisor's enabled list, the slash
//     registry's skill store, the workflows store), never from a side
//     directory — and fails the install with ErrNotConsumed when the
//     consumer does not list it. This is what ends the badge-only install
//     (docs/unwired-ledger.md, 2026-10-04) for every future kind.
//   - One event per transition, for every provider: TopicCapabilityInstalled
//     after an install the consumer confirms, TopicCapabilityUninstalled
//     after an uninstall the consumer confirms.
//
// The package is deliberately fleet-free (scripts/ci/check-no-fleet-imports.sh):
// fleet-backed providers inject their fetch and verification seams from
// core/rpc, the only chassis package allowed to hold a *fleet.Client.
//
// Names (research/decision-record.md in the mission directory): the Go
// contract is install.Provider, the orchestrator install.Framework, the
// Wails binding family Capability_*, the topics capability:installed /
// capability:uninstalled, and the frontend surface "Add capability".
//
// Rail (decision record §3, Phase 4 executed early by owner ruling
// 2026-10-05, ahead of Phase 3): ONE left-rail entry, "Capabilities", at
// route /tools, replaces Tools + Marketplace; the page's primary action is
// "Add capability", and /marketplace redirects to /tools. bundle and
// agent_pack are still provider-less (Phase 3) and stay disabled-with-reason
// in the surface's fleet-catalog rows.
package install
