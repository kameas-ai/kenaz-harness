package contexts_test

import (
	"os"
	"testing"

	"github.com/zalando/go-keyring"
)

// TestMain installs the in-memory mock for go-keyring so tests work on
// hosts without a system keychain — and, just as importantly, without
// hanging for the real macOS Keychain-access prompt / D-Bus Secret
// Service timeout on hosts that DO have one.
// TestContextPublish_TeamWithNoTeamID_FallsBackToOrg drives
// fleet.SaveTokens (via setupPublishTest) to establish signed-in fleet
// state, which hits the real OS keychain and hung for the full
// 3-minute test timeout before this file existed (CLAUDE.md: "Any test
// that establishes signed-in fleet state needs keyring.MockInit in a
// TestMain"). The mock is global to this package's test binary;
// production code still uses the real OS keychain.
func TestMain(m *testing.M) {
	keyring.MockInit()
	os.Exit(m.Run())
}
