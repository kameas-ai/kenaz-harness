package fleet

import (
	"os"
	"testing"

	"github.com/zalando/go-keyring"
)

// TestMain installs the in-memory mock for go-keyring so tests work on
// hosts without a system keychain (CI Linux runners without D-Bus / GNOME
// keyring fall into this category; their keyring.Set returns an error
// that token_store.go ignores, which manifests downstream as
// "fleet: not signed in" in HTTP tests). The mock is global to the
// package's test binary; production code still uses the real OS keychain.
func TestMain(m *testing.M) {
	keyring.MockInit()
	os.Exit(m.Run())
}
