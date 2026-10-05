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
