package mlsidecar

import (
	"context"
	"os"
	"strconv"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/bundle/channels"
	"github.com/kameas-ai/kenaz-harness/core/bundle/channels/localpath"
	"github.com/kameas-ai/kenaz-harness/core/bundle/integrity"
	"github.com/kameas-ai/kenaz-harness/core/bundle/manifest"
	"github.com/kameas-ai/kenaz-harness/core/secrets"
)

// unreachableBaseURL points nowhere real — a Client built on it always
// fails to connect, simulating "no process is answering :7774" without
// ever touching the network (loopback connection refused is immediate
// and local). NO real sidecar, no Python — per the WP12 brief.
const unreachableBaseURL = "http://127.0.0.1:1"

// fakeSpawner is the WP12 brief's required test double for Spawner: it
// never execs anything. onSpawn lets a test simulate "spawning made the
// engine start answering health" by mutating shared state (e.g. flipping
// the Client's BaseURL to a running stub) at the exact moment a real
// spawn would have started the process.
type fakeSpawner struct {
	called  bool
	exePath string
	pid     int
	err     error
	onSpawn func()
}

func (f *fakeSpawner) Spawn(_ context.Context, exePath string) (int, error) {
	f.called = true
	f.exePath = exePath
	if f.onSpawn != nil {
		f.onSpawn()
	}
	if f.err != nil {
		return 0, f.err
	}
	return f.pid, nil
}

// TestManager_EveryStateDistinctlyReachable drives Manager.Reconcile
// through a real code path for each of the honestly-surfaced states
// tasks.md WP12 requires, plus StateLegacyUnverified (added by the
// 2026-09-29 security-review amendment to design F5).
func TestManager_EveryStateDistinctlyReachable(t *testing.T) {
	t.Run(string(StateNotInstalled), func(t *testing.T) {
		l := NewLayout(t.TempDir())
		m := NewManager(l, NewClient(unreachableBaseURL, nil), nil, "harness", "0.84.0")
		got := m.Reconcile(context.Background())
		if got.State != StateNotInstalled {
			t.Fatalf("State = %q, want %q (%s)", got.State, StateNotInstalled, got.Detail)
		}
	})

	t.Run(string(StateInstalling), func(t *testing.T) {
		l := NewLayout(t.TempDir())
		setupVerifiedVersion(t, l, "1.0.0", []byte("bytes"))
		// Seed a LIVE spawn lock (our own test-process pid is always
		// alive) so this Manager's own spawn attempt finds the lock held.
		if err := os.MkdirAll(l.LeaseDir(), 0o755); err != nil {
			t.Fatalf("mkdir lease dir: %v", err)
		}
		if err := os.WriteFile(l.SpawnLockFile(), []byte(strconv.Itoa(os.Getpid())), 0o644); err != nil {
			t.Fatalf("seed spawn lock: %v", err)
		}
		m := NewManager(l, NewClient(unreachableBaseURL, nil), nil, "harness", "0.84.0")
		got := m.Reconcile(context.Background())
		if got.State != StateInstalling {
			t.Fatalf("State = %q, want %q (%s)", got.State, StateInstalling, got.Detail)
		}
	})

	t.Run(string(StateHealthy)+" via spawn", func(t *testing.T) {
		l := NewLayout(t.TempDir())
		setupVerifiedVersion(t, l, "1.0.0", []byte("bytes"))
		stub := newStubSidecar()
		defer stub.Close()
		stub.setHealth(HealthPayload{
			SidecarVersion:    "1.0.0",
			ContractVersions:  map[string]int{"api": SupportedContractMajor},
			LifecycleProtocol: 1,
		})

		client := NewClient(unreachableBaseURL, nil)
		spawner := &fakeSpawner{onSpawn: func() { client.BaseURL = stub.URL() }}
		m := NewManager(l, client, spawner, "harness", "0.84.0")
		got := m.Reconcile(context.Background())
		if got.State != StateHealthy {
			t.Fatalf("State = %q, want %q (%s)", got.State, StateHealthy, got.Detail)
		}
		if !spawner.called {
			t.Error("expected the spawner to have been called")
		}
	})

	t.Run(string(StateInstalledUnhealthy)+" via port conflict", func(t *testing.T) {
		l := NewLayout(t.TempDir()) // no install at all
		stub := newStubSidecar()
		defer stub.Close()
		stub.setHealth(HealthPayload{
			SidecarVersion:    "9.9.9",
			ExePath:           "/foreign/kameas-ml",
			ContractVersions:  map[string]int{"api": SupportedContractMajor},
			LifecycleProtocol: 1,
		})
		m := NewManager(l, NewClient(stub.URL(), nil), nil, "harness", "0.84.0")
		got := m.Reconcile(context.Background())
		if got.State != StateInstalledUnhealthy {
			t.Fatalf("State = %q, want %q (%s)", got.State, StateInstalledUnhealthy, got.Detail)
		}
		if got.Reason != ReasonPortConflict {
			t.Errorf("Reason = %q, want %q", got.Reason, ReasonPortConflict)
		}
	})

	t.Run(string(StateUnverified), func(t *testing.T) {
		l := NewLayout(t.TempDir())
		if err := l.EnsureDirs(); err != nil {
			t.Fatalf("EnsureDirs: %v", err)
		}
		dir := l.VersionDir("1.0.0") + "/kameas-ml"
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		exePath := dir + "/" + EngineExecutableName("")
		if err := os.WriteFile(exePath, []byte("bytes"), 0o755); err != nil {
			t.Fatalf("write: %v", err)
		}
		if err := l.SetCurrent("1.0.0"); err != nil {
			t.Fatalf("SetCurrent: %v", err)
		}
		// No install.json — never verified by this client.
		stub := newStubSidecar()
		defer stub.Close()
		stub.setHealth(HealthPayload{
			SidecarVersion:    "1.0.0",
			ExePath:           exePath,
			ContractVersions:  map[string]int{"api": SupportedContractMajor},
			LifecycleProtocol: 1,
		})
		m := NewManager(l, NewClient(stub.URL(), nil), nil, "harness", "0.84.0")
		got := m.Reconcile(context.Background())
		if got.State != StateUnverified {
			t.Fatalf("State = %q, want %q (%s)", got.State, StateUnverified, got.Detail)
		}
	})

	t.Run(string(StateContractUnsupported), func(t *testing.T) {
		l := NewLayout(t.TempDir())
		setupVerifiedVersion(t, l, "1.0.0", []byte("bytes"))
		stub := newStubSidecar()
		defer stub.Close()
		stub.setHealth(HealthPayload{
			SidecarVersion:    "1.0.0",
			ContractVersions:  map[string]int{"api": SupportedContractMajor + 50},
			LifecycleProtocol: 1,
		})
		m := NewManager(l, NewClient(stub.URL(), nil), nil, "harness", "0.84.0")
		got := m.Reconcile(context.Background())
		if got.State != StateContractUnsupported {
			t.Fatalf("State = %q, want %q (%s)", got.State, StateContractUnsupported, got.Detail)
		}
	})

	t.Run(string(StateLegacyUnverified), func(t *testing.T) {
		l := NewLayout(t.TempDir())
		stub := newStubSidecar()
		defer stub.Close()
		stub.setHealth(HealthPayload{
			SidecarVersion:    "0.9.0",
			ContractVersions:  map[string]int{"api": SupportedContractMajor},
			LifecycleProtocol: 0,
		})
		m := NewManager(l, NewClient(stub.URL(), nil), nil, "harness", "0.84.0")
		got := m.Reconcile(context.Background())
		if got.State != StateLegacyUnverified {
			t.Fatalf("State = %q, want %q (%s)", got.State, StateLegacyUnverified, got.Detail)
		}
	})
}

// TestManager_SkewWindow_LegacyEngine_NeverHealthyNeverSpawnsOrDoubles is
// the "skew adopt-only" proof (design F5/§3.7 R4), AS AMENDED by the
// 2026-09-29 security-review ruling: a pre-lease legacy engine is left
// running untouched (never terminated, never double-spawned — the
// spawner must never be called), but it is NEVER reported healthy and
// NEVER usable via the advisor ladder (SidecarProbe.Healthy()==false).
// The pre-amendment version of this test (formerly
// TestManager_SkewWindow_AdoptOnly) asserted the opposite — StateHealthy
// — which was the security-review's critical finding.
func TestManager_SkewWindow_LegacyEngine_NeverHealthyNeverSpawnsOrDoubles(t *testing.T) {
	l := NewLayout(t.TempDir())
	stub := newStubSidecar()
	defer stub.Close()
	stub.setHealth(HealthPayload{
		SidecarVersion:    "0.9.0",
		ContractVersions:  map[string]int{"api": SupportedContractMajor},
		LifecycleProtocol: 0, // legacy: no lease support
	})
	spawner := &fakeSpawner{}
	m := NewManager(l, NewClient(stub.URL(), nil), spawner, "harness", "0.84.0")
	got := m.Reconcile(context.Background())
	if got.State != StateLegacyUnverified {
		t.Fatalf("State = %q, want %q (%s)", got.State, StateLegacyUnverified, got.Detail)
	}
	if got.State == StateHealthy {
		t.Fatal("a legacy engine must never be reported healthy")
	}
	if got.Reason != ReasonLegacyEngine {
		t.Errorf("Reason = %q, want %q", got.Reason, ReasonLegacyEngine)
	}
	if spawner.called {
		t.Error("a legacy engine must be adopt-only: the spawner must never be called")
	}
	if m.Healthy() {
		t.Fatal("SidecarProbe.Healthy() must be false for a legacy-unverified engine — the advisor ladder must fall through")
	}
}

// TestManager_BareEmptyHealth_NeverReachesStateHealthy is the
// security-review's planted-style regression pin at the full Manager/
// stub-integration level: a process answering GET /health with the
// literal raw bytes `{}` — the reviewer's exact exploit shape — must
// never resolve to StateHealthy, and SidecarProbe.Healthy() must report
// false afterward (closing the ladder path the finding was about).
func TestManager_BareEmptyHealth_NeverReachesStateHealthy(t *testing.T) {
	l := NewLayout(t.TempDir())
	setupVerifiedVersion(t, l, "1.0.0", []byte("engine binary bytes"))

	stub := newStubSidecar()
	defer stub.Close()
	stub.setHealthRaw([]byte("{}"))

	m := NewManager(l, NewClient(stub.URL(), nil), &fakeSpawner{}, "harness", "0.84.0")
	got := m.Reconcile(context.Background())
	if got.State == StateHealthy {
		t.Fatalf("a bare {} /health response must never resolve to StateHealthy (got Detail=%q)", got.Detail)
	}
	if got.State != StateLegacyUnverified {
		t.Errorf("State = %q, want %q", got.State, StateLegacyUnverified)
	}
	if m.Healthy() {
		t.Fatal("SidecarProbe.Healthy() must be false after a bare {} /health response")
	}
}

// TestManager_ReconcileRenewsLease is the "lease heartbeat" half of the
// design §3.7 R4 bridge: a successful reconcile writes/refreshes this
// client's own lease file.
func TestManager_ReconcileRenewsLease(t *testing.T) {
	l := NewLayout(t.TempDir())
	exePath, sha := setupVerifiedVersion(t, l, "1.0.0", []byte("bytes"))
	stub := newStubSidecar()
	defer stub.Close()
	stub.setHealth(HealthPayload{
		SidecarVersion:    "1.0.0",
		ExePath:           exePath,
		EngineSHA256:      sha,
		ContractVersions:  map[string]int{"api": SupportedContractMajor},
		LifecycleProtocol: 1,
	})
	m := NewManager(l, NewClient(stub.URL(), nil), nil, "harness", "0.84.0")
	if got := m.Reconcile(context.Background()); got.State != StateHealthy {
		t.Fatalf("State = %q, want %q (%s)", got.State, StateHealthy, got.Detail)
	}
	if _, ok, err := ReadLease(l, "harness"); err != nil || !ok {
		t.Fatalf("expected a lease file to exist after a healthy reconcile: ok=%v err=%v", ok, err)
	}
}

// TestManager_Status_NoIOBeforeReconcile proves "lazy start on first
// advisor demand, not app boot" at the Manager level: constructing a
// Manager and reading Status() does not itself contact anything.
func TestManager_Status_NoIOBeforeReconcile(t *testing.T) {
	l := NewLayout(t.TempDir())
	m := NewManager(l, NewClient(unreachableBaseURL, nil), nil, "harness", "0.84.0")
	got := m.Status()
	if got.State != StateNotInstalled {
		t.Fatalf("State = %q, want %q", got.State, StateNotInstalled)
	}
}

func testChannelRegistryForUpdate() channels.Registry {
	r := channels.NewRegistry()
	_ = r.Register(localpath.Kind, localpath.Factory)
	return r
}

// TestManager_UpdateAndActivate_SwapFlow is the "update swap flow" proof
// (design §3.5/§3.7 R6): stage + verify the new version, flip `current`,
// then request a token-authorized graceful shutdown of whatever was
// running.
func TestManager_UpdateAndActivate_SwapFlow(t *testing.T) {
	l := NewLayout(t.TempDir())
	// Seed v1 as already installed and current.
	setupVerifiedVersion(t, l, "1.0.0", []byte("v1 bytes"))
	token, err := WriteLocalToken(l)
	if err != nil {
		t.Fatalf("WriteLocalToken: %v", err)
	}

	channelRoot := t.TempDir()
	_, sha := buildTestEngineZip(t, channelRoot, "engine-v2.zip", []byte("v2 bytes"))
	if err := os.WriteFile(channelRoot+"/engine-v2.zip.sig", []byte("sig-bytes"), 0o600); err != nil {
		t.Fatalf("write sig: %v", err)
	}

	// The post-update reconcile re-adopts the just-flipped v2 install, so
	// the stub's health must report an exe_path/digest this client can
	// verify against its OWN install.json record for v2 — deterministic
	// because Install always unpacks the "kameas-ml/kameas-ml" zip entry
	// to this exact path under the version directory.
	v2ExePath := l.VersionDir("2.0.0") + "/kameas-ml/" + EngineExecutableName("")

	// health.EngineSHA256 is deliberately left empty: the cross-check
	// only fires when the self-report sets it (design: reports are
	// cross-checks, never trust roots — but no report is not a lie).
	// `sha` here is the ZIP's digest, not the unpacked executable's
	// (install.go records the latter in install.json — see
	// TestInstall_HappyPath_VerifiesUnpacksAndFlipsCurrent), so it would
	// be the wrong value to assert here anyway.
	stub := newStubSidecar()
	defer stub.Close()
	stub.setShutdownRequireToken(token)
	stub.setHealth(HealthPayload{
		SidecarVersion:    "2.0.0",
		ExePath:           v2ExePath,
		ContractVersions:  map[string]int{"api": SupportedContractMajor},
		LifecycleProtocol: 1,
	})

	client := NewClient(stub.URL(), nil)
	m := &Manager{
		Layout:   l,
		Client:   client,
		ClientID: "harness",
		Version:  "0.84.0",
		Registry: testChannelRegistryForUpdate(),
		Creds:    secrets.NoopResolver{},
		Verifier: Verifier{TrustVerifier: fakeTrustVerifier{ok: true, wantBytes: []byte("sig-bytes")}, Policy: integrity.SigningRequired},
	}

	req := InstallRequest{
		ChannelKind:    localpath.Kind,
		ChannelPath:    channelRoot,
		Version:        "2.0.0",
		ArtifactPath:   "engine-v2.zip",
		ExpectedSHA256: sha,
		Signature:      &manifest.SignatureRef{Kind: "ed25519_detached", Locator: "engine-v2.zip.sig"},
		Source:         "local_path:" + channelRoot,
	}
	result, status := m.UpdateAndActivate(context.Background(), req)
	if !result.Flipped {
		t.Fatalf("expected the install/flip half to succeed: %+v", result)
	}
	if !result.ShutdownRequested {
		t.Fatalf("expected a shutdown request to have been sent: %+v", result)
	}
	if stub.shutdownCallCount() != 1 {
		t.Errorf("shutdownCallCount = %d, want 1", stub.shutdownCallCount())
	}
	current, err := l.CurrentVersionDir()
	if err != nil {
		t.Fatalf("CurrentVersionDir: %v", err)
	}
	if got := current[len(current)-5:]; got != "2.0.0" {
		t.Errorf("current version dir = %s, want to end in 2.0.0", current)
	}
	if status.State != StateHealthy {
		t.Errorf("final status.State = %q, want %q (%s)", status.State, StateHealthy, status.Detail)
	}
}

// TestManager_Uninstall_LeavesNoProcessWeightsOrConfig is the WP12
// uninstall checklist proof (design §6.2 step 5).
func TestManager_Uninstall_LeavesNoProcessWeightsOrConfig(t *testing.T) {
	l := NewLayout(t.TempDir())
	setupVerifiedVersion(t, l, "1.0.0", []byte("bytes"))
	token, err := WriteLocalToken(l)
	if err != nil {
		t.Fatalf("WriteLocalToken: %v", err)
	}
	if err := AcquireOrRenewLease(l, "harness", os.Getpid(), "0.84.0"); err != nil {
		t.Fatalf("AcquireOrRenewLease: %v", err)
	}

	stub := newStubSidecar()
	defer stub.Close()
	stub.setShutdownRequireToken(token)

	m := NewManager(l, NewClient(stub.URL(), nil), nil, "harness", "0.84.0")
	if err := m.Uninstall(context.Background()); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}

	if _, err := os.Stat(l.VersionsDir()); !os.IsNotExist(err) {
		t.Errorf("expected versions/ to be removed, stat err=%v", err)
	}
	if _, err := os.Lstat(l.CurrentLink()); !os.IsNotExist(err) {
		t.Errorf("expected `current` to be removed, stat err=%v", err)
	}
	if _, ok, _ := ReadInstallJSON(l); ok {
		t.Error("expected install.json to be removed")
	}
	if _, ok, _ := ReadLease(l, "harness"); ok {
		t.Error("expected this client's own lease to be released")
	}
	if stub.shutdownCallCount() != 1 {
		t.Errorf("expected a sole-leaseholder shutdown request, shutdownCallCount=%d", stub.shutdownCallCount())
	}
	if m.Status().State != StateNotInstalled {
		t.Errorf("Status().State = %q, want %q", m.Status().State, StateNotInstalled)
	}
}
