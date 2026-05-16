// Package fleet provides Zitadel PKCE authentication, identity caching,
// and the fleet-server integration for the kenaz harness. It is the
// foundation for all fleet-backed features (role-gated capabilities,
// org/team identity, tier-based access control).
//
// # OSS-first contract
//
// The harness is a fully-functional local-only product without this package.
// Fleet features are invisible until the user signs in. No non-fleet feature
// requires authentication. Setting HARNESS_FLEET_DISABLED=1 makes the harness
// behave identically to a fork that removed core/fleet/ entirely.
//
// # Fork recipe
//
// Forking operators who do not want fleet integration can remove this package
// cleanly by deleting core/fleet/ and severing four wire-up points:
//
//  1. main.go — the fleet.NewClient call and fleet kill-switch check at startup.
//  2. core/rpc/api.go — the *fleet.Client field on API and its initialisation
//     in New(), plus the fleet.Disabled() guard.
//  3. core/rpc/views/settings/api.go — the five Fleet* methods on SettingsAPI
//     and their implementations in impl.go, plus the FleetProfileInfo type.
//  4. frontend/src/views/settings/AccountPanel.vue — delete the file entirely;
//     remove the Account tab from SettingsView.vue.
//
// After removing those four sites, `go build ./...` and the frontend build
// must both succeed with no fleet-related errors.
//
// # Architecture
//
// The package is intentionally isolated. Only core/rpc/views/settings/ is
// permitted to import core/fleet/. All other RPC views must remain fleet-free.
// scripts/ci/check-no-fleet-imports.sh enforces this at PR gate.
package fleet
