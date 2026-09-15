package catalog

import (
	"os"
	"testing"

	"github.com/zalando/go-keyring"
)

// TestMain installs the in-memory mock for go-keyring, mirroring the five
// sibling packages that already carry it (core/fleet, core/rpc,
// views/settings, views/sites, mcp/builtin/sites). This package was the
// ONE test package touching corefleet.SaveTokens/LoadTokens without the
// guard, which had two live consequences:
//
//  1. On macOS dev machines, every run of this package's tests (including
//     every scripts/ci gate sweep and every full-suite run) hit the REAL
//     login keychain via /usr/bin/security. Test runs carry no
//     KENAZ_HARNESS_ENV, so paths.EnvName() defaults to "prod" and the
//     lookups targeted `fleet:prod:access_token` — an item that never
//     exists on a dev-signed-in machine — producing repeated
//     cannot-be-found keychain error popups for the developer (reported
//     live 2026-09-15) and, under check-tests-are-hermetic.sh's sandboxed
//     HOME, the documented keychain-hang flake that made
//     TestAPI_CatalogUnpublish_EmitsAuditOnSuccess time out.
//  2. On headless Linux CI (no D-Bus Secret Service), keyring.Set errors
//     and manifests downstream as "fleet: not signed in" — the same class
//     core/fleet/main_test.go's own comment documents.
//
// The mock is global to this package's test binary; production code still
// uses the real OS keychain.
func TestMain(m *testing.M) {
	keyring.MockInit()
	os.Exit(m.Run())
}
