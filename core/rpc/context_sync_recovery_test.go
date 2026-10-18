package rpc

// context_sync_recovery_test.go — device-keys-handoff-01DEVKH01 WP02
// (spec §4 consequence (a), AC-9): importing a recovery code changes the
// derived handoff key, so the adapter re-registers it with PUT
// /api/v1/me/nodes/{node_id}/keys; a 403 node_removed there takes the same
// terminal sign-out as enroll. Real files (node_id.txt, device key) under a
// temp data dir; a fake fleet answering fleet's real envelopes.

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	corefleet "github.com/kameas-ai/kenaz-harness/core/fleet"
)

func TestRecoveryImport_ReRegistersDeviceKeys(t *testing.T) {
	if corefleet.Disabled() {
		t.Skip("HARNESS_FLEET_DISABLED=1")
	}
	var (
		mu      sync.Mutex
		puts    []map[string]any
		paths   []string
		removed atomic.Bool
	)
	var srv *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("/config.json", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"api_base_url": srv.URL})
	})
	mux.HandleFunc("PUT /api/v1/me/nodes/{id}/keys", func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		mu.Lock()
		puts = append(puts, body)
		paths = append(paths, r.PathValue("id"))
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if removed.Load() {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"code":"node_removed","message":"this node was removed by an administrator; enroll under a new node_id after signing in again"}`))
			return
		}
		_, _ = w.Write([]byte(`{"keys":[{"purpose":"handoff","fingerprint":"sha256:00","created_at":"2026-10-07T00:00:00Z"}]}`))
	})
	srv = httptest.NewServer(mux)
	defer srv.Close()
	corefleet.SetExternalTokenSource(func() string { return "tok-recovery" })
	t.Cleanup(func() { corefleet.SetExternalTokenSource(nil) })

	dataDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dataDir, "fleet"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "fleet", "node_id.txt"), []byte("NODE-B\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var signedOut atomic.Int32
	adapter := &recoveryBackendAdapter{
		client:        corefleet.NewClientForTestingWithDataDir(srv.URL, dataDir),
		dataDir:       dataDir,
		onNodeRemoved: func() { signedOut.Add(1) },
	}

	// Device A's seed, exported as a recovery code, imported on device B.
	seedA := make([]byte, 32)
	for i := range seedA {
		seedA[i] = byte(200 - i)
	}
	code, err := corefleet.MintRecoveryCode(seedA)
	if err != nil {
		t.Fatal(err)
	}
	if err := adapter.ApplyRecoveryCode(code); err != nil {
		t.Fatalf("ApplyRecoveryCode: %v", err)
	}
	mu.Lock()
	gotPuts, gotPaths := append([]map[string]any(nil), puts...), append([]string(nil), paths...)
	mu.Unlock()
	if len(gotPuts) != 1 || gotPaths[0] != "NODE-B" {
		t.Fatalf("PUT keys calls = %v paths=%v, want one for NODE-B", len(gotPuts), gotPaths)
	}
	// The registered key is device B's: seed A + node B — NOT device A's
	// key (same seed, other node id) — by design (spec §4 (a)).
	privB, _ := corefleet.DeriveHandoffPrivKey(seedA, "NODE-B")
	privA, _ := corefleet.DeriveHandoffPrivKey(seedA, "NODE-A")
	sent := gotPuts[0]["handoff_public_key"]
	if sent != base64.StdEncoding.EncodeToString(privB.PublicKey().Bytes()) {
		t.Fatalf("PUT handoff key = %v, want seedA+NODE-B derivation", sent)
	}
	if sent == base64.StdEncoding.EncodeToString(privA.PublicKey().Bytes()) {
		t.Fatal("recovery import must not reproduce device A's handoff key")
	}
	if signedOut.Load() != 0 {
		t.Fatal("a 200 must not sign out")
	}

	// node_removed on PUT → the same terminal sign-out hook as enroll.
	removed.Store(true)
	if err := adapter.ApplyRecoveryCode(code); err != nil {
		t.Fatalf("the import itself still succeeds locally: %v", err)
	}
	if signedOut.Load() != 1 {
		t.Fatalf("node_removed on PUT keys: sign-out hook calls = %d, want 1", signedOut.Load())
	}
}
