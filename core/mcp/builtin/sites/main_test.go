package sites_test

import (
	"os"
	"testing"

	"github.com/zalando/go-keyring"
)

// TestMain installs the in-memory keyring mock so tests work on CI hosts
// without a system keychain (Linux without D-Bus / GNOME keyring). The mock
// is global to this package's test binary; production code still uses the
// real OS keychain.
func TestMain(m *testing.M) {
	keyring.MockInit()
	os.Exit(m.Run())
}
