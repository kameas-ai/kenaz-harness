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
)

func TestMain(m *testing.M) {
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
