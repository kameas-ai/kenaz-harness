package mlsidecar

// v0.86.0 unwired sweep (2026-10-04): kenaz-ml reads lease/shutdown.token
// on every /v1/admin/shutdown and NEVER creates it ("written
// user-read-only by the spawning client"), but WriteLocalToken had zero
// non-test callers — every test that exercised Update/Uninstall wrote the
// token by hand (CLAUDE.md blind spot #2: a fixture doing the production
// layer's job). In production Update never stopped the old engine and
// Uninstall deleted the root out from under a running one. These tests
// write NO token themselves.

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/bundle/channels/localpath"
	"github.com/kameas-ai/kenaz-harness/core/bundle/integrity"
	"github.com/kameas-ai/kenaz-harness/core/bundle/manifest"
	"github.com/kameas-ai/kenaz-harness/core/secrets"
)

func TestManager_SpawnWritesShutdownTokenBeforeTheEngineExists(t *testing.T) {
	l := NewLayout(t.TempDir())
	setupVerifiedVersion(t, l, "1.0.0", []byte("bytes"))
	stub := newStubSidecar()
	defer stub.Close()
	stub.setHealth(healthyStubPayload("1.0.0"))
	client := NewClient(unreachableBaseURL, nil)
	var tokenAtSpawn string
	spawner := &fakeSpawner{onSpawn: func() {
		tok, ok, err := ReadLocalToken(l)
		if err == nil && ok {
			tokenAtSpawn = tok
		}
		client.BaseURL = stub.URL()
	}}
	m := NewManager(l, client, spawner, "harness", "0.86.0")
	if got := m.Reconcile(context.Background()); got.State != StateHealthy {
		t.Fatalf("State = %q (%s)", got.State, got.Detail)
	}
	if tokenAtSpawn == "" {
		t.Fatal("no lease/shutdown.token existed when the engine was spawned — nothing could ever stop it")
	}
	info, err := os.Stat(l.TokenFile())
	if err != nil {
		t.Fatalf("stat token: %v", err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("token mode = %v, want user-only", info.Mode().Perm())
	}
}

func TestManager_Uninstall_NoTokenOnDisk_StillStopsTheEngine(t *testing.T) {
	l := NewLayout(t.TempDir())
	setupVerifiedVersion(t, l, "1.0.0", []byte("bytes"))
	if err := AcquireOrRenewLease(l, "harness", os.Getpid(), "0.86.0"); err != nil {
		t.Fatalf("AcquireOrRenewLease: %v", err)
	}
	// An engine spawned by a pre-fix harness: running, no token on disk.
	if _, ok, _ := ReadLocalToken(l); ok {
		t.Fatal("precondition: no token on disk")
	}
	stub := newStubSidecar()
	defer stub.Close()
	m := NewManager(l, NewClient(stub.URL(), nil), nil, "harness", "0.86.0")
	if err := m.Uninstall(context.Background()); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	stub.mu.Lock()
	calls := append([]string(nil), stub.shutdownCalls...)
	stub.mu.Unlock()
	if len(calls) != 1 || strings.TrimSpace(strings.TrimPrefix(calls[0], "Bearer ")) == "" {
		t.Fatalf("shutdown requests = %q, want exactly one carrying a bearer token", calls)
	}
}

func TestEnsureLocalToken_NeverRotatesAnExistingToken(t *testing.T) {
	l := NewLayout(t.TempDir())
	if err := os.MkdirAll(l.LeaseDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	first, err := ensureLocalToken(l)
	if err != nil || first == "" {
		t.Fatalf("ensureLocalToken (create) = %q, %v", first, err)
	}
	again, err := ensureLocalToken(l)
	if err != nil || again != first {
		t.Fatalf("ensureLocalToken rotated a token another client may hold: %q -> %q (%v)", first, again, err)
	}
}

// TestManager_Update_NoTokenOnDisk_StillRequestsShutdown is the Update
// consumer's half (review L7): the swap-flow test hand-writes the token,
// which is exactly the fixture shape that hid this defect. Here nothing
// writes it but Update itself.
func TestManager_Update_NoTokenOnDisk_StillRequestsShutdown(t *testing.T) {
	l := NewLayout(t.TempDir())
	setupVerifiedVersion(t, l, "1.0.0", []byte("v1 bytes"))
	if _, ok, _ := ReadLocalToken(l); ok {
		t.Fatal("precondition: no token on disk")
	}

	channelRoot := t.TempDir()
	_, sha := buildTestEngineZip(t, channelRoot, "engine-v2.zip", []byte("v2 bytes"))
	if err := os.WriteFile(channelRoot+"/engine-v2.zip.sig", []byte("sig-bytes"), 0o600); err != nil {
		t.Fatalf("write sig: %v", err)
	}
	stub := newStubSidecar()
	defer stub.Close()
	stub.setHealth(HealthPayload{
		SidecarVersion:    "2.0.0",
		ExePath:           l.VersionDir("2.0.0") + "/kameas-ml/" + EngineExecutableName(""),
		ContractVersions:  map[string][]string{"branch_now": {"0123456789abcdef"}},
		LifecycleProtocol: 1,
	})
	m := &Manager{
		Layout:   l,
		Client:   NewClient(stub.URL(), nil),
		ClientID: "harness",
		Version:  "0.86.0",
		Registry: testChannelRegistryForUpdate(),
		Creds:    secrets.NoopResolver{},
		Verifier: Verifier{TrustVerifier: fakeTrustVerifier{ok: true, wantBytes: []byte("sig-bytes")}, Policy: integrity.SigningRequired},
	}
	result, _ := m.UpdateAndActivate(context.Background(), InstallRequest{
		ChannelKind:    localpath.Kind,
		ChannelPath:    channelRoot,
		Version:        "2.0.0",
		ArtifactPath:   "engine-v2.zip",
		ExpectedSHA256: sha,
		Signature:      &manifest.SignatureRef{Kind: "ed25519_detached", Locator: "engine-v2.zip.sig"},
		Source:         "local_path:" + channelRoot,
	})
	if !result.Flipped {
		t.Fatalf("install/flip half failed: %+v", result)
	}
	if !result.ShutdownRequested || result.ShutdownErr != nil {
		t.Fatalf("Update did not ask the old engine to stop (ShutdownErr=%v) — with no token on disk this is the pre-fix production behaviour", result.ShutdownErr)
	}
	tok, ok, err := ReadLocalToken(l)
	if err != nil || !ok || tok == "" {
		t.Fatalf("Update did not leave a shutdown token on disk: ok=%v err=%v", ok, err)
	}
	stub.mu.Lock()
	calls := append([]string(nil), stub.shutdownCalls...)
	stub.mu.Unlock()
	if len(calls) != 1 || calls[0] != "Bearer "+tok {
		t.Fatalf("shutdown requests = %q, want one bearing the on-disk token", calls)
	}
}
