package catalog

import (
	"os"
	"testing"

	"github.com/zalando/go-keyring"
)

// TestMain installs go-keyring's in-memory mock for this package's test
// binary, the same way core/rpc/views/sites/main_test.go,
// core/fleet/main_test.go and core/mcp/builtin/sites/main_test.go already
// do (upgrade-path-coverage-01PMUG01 WP05 review, 2026-08-18).
//
// Found during fleet-enforcement-truth-01PMZ505 WP11 verification
// (2026-09-15): impl_test.go's setupUnpublishTest calls
// corefleet.SaveTokens, which on darwin talks to the real Keychain. That
// is fine under a developer's own HOME (the entry already exists and the
// call returns in milliseconds), but scripts/ci/check-tests-are-hermetic.sh
// points HOME/XDG_CONFIG_HOME/AppData at a sentinel directory to catch
// tests that write outside t.TempDir() — under that redirected HOME, the
// Keychain prompt has nowhere to go and SaveTokens blocks until the test
// binary's default -timeout=180s fires. Without this mock,
// TestAPI_CatalogUnpublish_EmitsAuditOnSuccess and
// TestAPI_CatalogUnpublish_NoAuditOnFailure hang the whole package for
// 180s and then fail, on every macOS run of that gate — this is
// independent of and predates the WP11 frontend work in this same
// session; impl_test.go itself was unmodified (git log: 1ba6a209, v0.79.0).
//
// The mock is global to this test binary only; production still uses the
// OS keychain.
func TestMain(m *testing.M) {
	keyring.MockInit()
	os.Exit(m.Run())
}
