package rpc

// testmain_test.go — upgrade-path-coverage-01PMUG01 WP05 (FR-4b).
//
// rpc.New logs "rpc.boot.path_augmented" (or the warn variant) through
// the process-global logging.L() singleton on every call, before any
// per-test sandboxing runs. logging.L() lazily opens its file on FIRST
// call per process and, absent an explicit logging.Configure(dir), that
// file is ~/.kenaz/harness.log (core/logging/logger.go) — the
// developer's real home directory, entirely independent of whichever
// rpc.New call site in this package happens to run first in the test
// binary. WithSettingsStore (this WP's other half) closes the settings-
// store seam; this closes the same class of hole in the logging
// package, using the override mechanism core/logging already ships
// for exactly this purpose (main.go calls logging.Configure with the
// per-env log directory at boot).
//
// TestMain runs once per test binary, before any individual test's
// t.TempDir() exists, so the redirect target is a package-scoped temp
// dir instead.
import (
	"os"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/logging"
	"github.com/zalando/go-keyring"
)

func TestMain(m *testing.M) {
	// keyring.MockInit() installs go-keyring's in-memory backend by WRITING
	// a package-level global. Doing it per-test races every goroutine that
	// READS that global concurrently — and rpc.New starts one: the fleet
	// ConfigPoller calls fleet.LoadTokens -> keyring.Get() on a ticker.
	//
	// This surfaced repeatedly as "TestKeychainDelete_NotFoundIsSilent:
	// race detected", which reads like a keychain flake and is not one.
	// Two earlier fixes attacked the wrong half: t.Cleanup(api.Shutdown)
	// on every API construction site (v0.66.0) and making Syncer.Stop
	// idempotent. Both were correct in themselves and neither could work,
	// because cleanup runs when a test ENDS while tests run in PARALLEL —
	// one test's poller is alive precisely when another calls MockInit.
	//
	// Installing the mock once here, before m.Run and therefore before any
	// test or goroutine exists, removes the concurrent WRITE entirely.
	// Afterwards every access is a read.
	//
	// Safe for the tests that used to call it per-test: they use distinct
	// service/key pairs and plant whatever they need, so they require the
	// mock to be INSTALLED, not RESET.
	keyring.MockInit()

	dir, err := os.MkdirTemp("", "kenaz-rpc-test-logs-*")
	if err == nil {
		logging.Configure(dir)
	}
	code := m.Run()
	if dir != "" {
		_ = os.RemoveAll(dir)
	}
	os.Exit(code)
}
