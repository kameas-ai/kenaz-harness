package contexts_test

import (
	"os"
	"testing"

	"github.com/zalando/go-keyring"
)

// TestMain installs go-keyring's in-memory mock for this package's test
// binary, the same way core/fleet/main_test.go, core/rpc/views/sites/
// main_test.go, and core/rpc/views/settings' fleet tests already do.
//
// context_publish_org_fallback_test.go's setupPublishTest calls
// fleet.SaveTokens, which on darwin talks to the real Keychain. That is
// fine under a developer's own HOME — the entry already exists and the
// call returns in milliseconds. Under a REDIRECTED HOME it is not: the
// Keychain prompt has nowhere to go and SaveTokens blocks until the test
// binary's -timeout fires.
//
// scripts/ci/check-tests-are-hermetic.sh (FR-4b) runs exactly that way —
// it points HOME/XDG_CONFIG_HOME/AppData at a sentinel directory. Without
// this mock the gate does not report a hermeticity violation; it hangs
// this package for its full -timeout=180s and then fails on a panic
// (goroutine dump from the timed-out `go test`), on every machine with no
// interactive Keychain/Secret-Service session — found running the gate
// locally, 2026-09-15.
//
// The mock is global to this test binary only; production still uses the
// OS keychain.
func TestMain(m *testing.M) {
	keyring.MockInit()
	os.Exit(m.Run())
}
