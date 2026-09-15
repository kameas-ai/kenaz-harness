package contexts_test

import (
	"os"
	"testing"

	"github.com/zalando/go-keyring"
)

// TestMain installs the in-memory go-keyring mock, mirroring the six
// sibling packages that carry it. Added after
// TestContextPublish_TeamWithNoTeamID_FallsBackToOrg was caught blocked
// inside go-keyring's macOSXKeychain.Set (a real /usr/bin/security write)
// under check-tests-are-hermetic.sh's sandboxed HOME — the security agent
// cannot find the login keychain there, so the exec'd process hangs
// rather than erroring (2026-09-15, finding #108).
//
// Method note for the next audit: grepping test files for direct
// SaveTokens/LoadTokens calls is NOT sufficient to find packages needing
// this guard — this package reaches the keyring through test setup that
// signs in via the fleet token store. The class-level fix is a test-mode
// refusal at the token-store seam itself (finding #81); until that
// exists, any test that establishes a signed-in fleet state needs this.
func TestMain(m *testing.M) {
	keyring.MockInit()
	os.Exit(m.Run())
}
