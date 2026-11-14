package settings

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/fleet"
)

// Sign-out removes the per-account local records (share ledger, own-node
// location index, step-up refusal) so the next account never inherits them.
func TestFleetSignOut_RemovesAccountScopedFiles(t *testing.T) {
	r := newSessionRig(t)
	files := fleet.AccountScopedFiles(r.dataDir)
	if len(files) != 3 {
		t.Fatalf("account files = %v", files)
	}
	for _, p := range files {
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	keep := filepath.Join(r.dataDir, "fleet", "wire_id_salt")
	if err := os.WriteFile(keep, []byte("salt"), 0o600); err != nil {
		t.Fatal(err)
	}
	_ = r.api.FleetSignOut(context.Background())
	for _, p := range files {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("%s survived sign-out (err=%v)", filepath.Base(p), err)
		}
	}
	if _, err := os.Stat(keep); err != nil {
		t.Errorf("sign-out removed an unrelated file: %v", err)
	}
}
