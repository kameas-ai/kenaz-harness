package contexts_test

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
// Found during fleet-enforcement-truth-01PMZ505 verification (2026-09-15,
// unrelated to that mission's own WPs — this package is untouched by any
// of them; found only because scripts/ci/check-tests-are-hermetic.sh was
// run as part of that mission's release-ritual verification pass).
// context_publish_org_fallback_test.go's setupPublishTest calls
// corefleet.SaveTokens, which on darwin talks to the real Keychain. Under
// the sentinel HOME/XDG_CONFIG_HOME/AppData that gate redirects to (to
// catch tests writing outside t.TempDir()), the Keychain prompt has
// nowhere to go and SaveTokens blocks until the test binary's default
// -timeout=180s fires, failing the whole package on every macOS run of
// that gate. git log confirms this test file predates this session
// (v0.80.0, commit ed55cfd1) — the gap is pre-existing, not introduced
// here.
//
// The mock is global to this test binary only; production still uses the
// OS keychain.
func TestMain(m *testing.M) {
	keyring.MockInit()
	os.Exit(m.Run())
}
