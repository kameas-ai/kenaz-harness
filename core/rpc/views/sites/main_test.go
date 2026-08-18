package sites_test

import (
	"os"
	"testing"

	"github.com/zalando/go-keyring"
)

// TestMain installs go-keyring's in-memory mock for this package's test
// binary, the same way core/fleet/main_test.go and
// core/mcp/builtin/sites/main_test.go already do.
//
// WHY IT WAS NEEDED HERE (upgrade-path-coverage-01PMUG01 WP05 review,
// 2026-08-18). TestSitesLogs_TailLinesClampedTo2000 calls
// corefleet.SaveTokens, which on darwin talks to the real Keychain. That
// is fine under a developer's own HOME — the entry already exists and the
// call returns in milliseconds. Under a REDIRECTED HOME it is not: the
// Keychain prompt has nowhere to go and SaveTokens blocks until the test
// binary's -timeout fires.
//
// scripts/ci/check-tests-are-hermetic.sh (FR-4b) runs exactly that way —
// it points HOME / XDG_CONFIG_HOME / AppData at a sentinel directory to
// detect tests writing outside t.TempDir(). Without this mock the gate
// does not report a hermeticity violation; it hangs this package for its
// full -timeout=180s and then fails on a panic, on every run, on every
// macOS machine. The test's own `t.Skipf("OS keychain unavailable")`
// escape hatch only fires on headless Linux, where SaveTokens returns an
// error instead of blocking — which is why this was invisible until the
// gate was run locally.
//
// The mock is global to this test binary only; production still uses the
// OS keychain.
func TestMain(m *testing.M) {
	keyring.MockInit()
	os.Exit(m.Run())
}
