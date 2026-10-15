package rpc

// context_sync_seed_retention_test.go — provider-credentials-client-01PCCLI01
// A3 at the wiring seam: ApplyRecoveryCode (the production import path)
// must keep this device's previous seed for decryption. Real core/keyring
// seam (mock backend under `go test`), no fleet client (import stays local).

import (
	"bytes"
	"strconv"
	"testing"

	corefleet "github.com/kameas-ai/kenaz-harness/core/fleet"
	"github.com/kameas-ai/kenaz-harness/core/keyring"
	"github.com/kameas-ai/kenaz-harness/core/paths"
)

func TestRecoveryImport_RetainsPreviousSeedForDecrypt(t *testing.T) {
	account := "fleet:" + paths.KeychainNamespace() + ":context_seed"
	wipe := func() {
		svc := paths.FleetKeychainService()
		_ = keyring.Delete(svc, account)
		for i := 1; i <= 6; i++ {
			_ = keyring.Delete(svc, account+"_prev."+strconv.Itoa(i))
		}
	}
	wipe()
	t.Cleanup(wipe)

	local, imported := make([]byte, 32), make([]byte, 32)
	for i := range local {
		local[i], imported[i] = byte(i+1), byte(250-i)
	}
	if err := corefleet.StoreContextSeed(local); err != nil {
		t.Fatalf("seed this device: %v", err)
	}
	code, err := corefleet.MintRecoveryCode(imported)
	if err != nil {
		t.Fatal(err)
	}
	if err := (&recoveryBackendAdapter{}).ApplyRecoveryCode(code); err != nil {
		t.Fatalf("ApplyRecoveryCode: %v", err)
	}
	seeds, err := corefleet.ContextSeedsForDecrypt()
	if err != nil {
		t.Fatalf("ContextSeedsForDecrypt: %v", err)
	}
	if len(seeds) != 2 || !bytes.Equal(seeds[0], imported) || !bytes.Equal(seeds[1], local) {
		t.Fatalf("decrypt seeds after import = %d entries, want [imported, previous local]", len(seeds))
	}
}
