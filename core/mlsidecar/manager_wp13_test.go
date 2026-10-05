package mlsidecar

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/bundle/integrity"
	"github.com/kameas-ai/kenaz-harness/core/secrets"
)

func healthyStubPayload(version string) HealthPayload {
	return HealthPayload{
		SidecarVersion:    version,
		LifecycleProtocol: 1,
	}
}

func TestLayout_TokenFileIsTheRealEnginesName(t *testing.T) {
	l := NewLayout(t.TempDir())
	if got := filepath.Base(l.TokenFile()); got != "shutdown.token" {
		t.Fatalf("TokenFile base = %q, want shutdown.token (the real engine's frozen name)", got)
	}
	if filepath.Dir(l.TokenFile()) != l.LeaseDir() {
		t.Fatalf("token must live in lease/: %s", l.TokenFile())
	}
}

// TestManager_SpawnCreatesLeaseDirBeforeSpawn pins the kenaz-ml interop
// invariant: the CLIENT creates lease/ before the engine process exists
// (an engine that finds no lease/ under KENAZ_ML_INSTALL_ROOT could read
// "no leases" as "no clients" and self-terminate).
func TestManager_SpawnCreatesLeaseDirBeforeSpawn(t *testing.T) {
	l := NewLayout(t.TempDir())
	setupVerifiedVersion(t, l, "1.0.0", []byte("bytes"))
	if _, err := os.Stat(l.LeaseDir()); !os.IsNotExist(err) {
		t.Fatalf("precondition: lease dir must not exist yet (%v)", err)
	}
	stub := newStubSidecar()
	defer stub.Close()
	stub.setHealth(healthyStubPayload("1.0.0"))
	client := NewClient(unreachableBaseURL, nil)
	leaseDirExistedAtSpawn := false
	spawner := &fakeSpawner{onSpawn: func() {
		info, err := os.Stat(l.LeaseDir())
		leaseDirExistedAtSpawn = err == nil && info.IsDir()
		client.BaseURL = stub.URL()
	}}
	m := NewManager(l, client, spawner, "harness", "0.85.0")
	if got := m.Reconcile(context.Background()); got.State != StateHealthy {
		t.Fatalf("State = %q (%s)", got.State, got.Detail)
	}
	if !leaseDirExistedAtSpawn {
		t.Fatal("lease/ did not exist when the engine was spawned")
	}
}

func TestManager_StartupWaitPollsUntilHealthy(t *testing.T) {
	l := NewLayout(t.TempDir())
	setupVerifiedVersion(t, l, "1.0.0", []byte("bytes"))
	stub := newStubSidecar()
	defer stub.Close()
	stub.setHealth(healthyStubPayload("1.0.0"))
	// Pre-spawn, nothing listens (transport error). A 503 here would be
	// a LIVE foreign/booting process and correctly classify as
	// port-occupied instead of spawning (reconcileLocked's
	// ErrUnusableResponse branch) — found at the WP13 merge.
	stub.setDown(true)
	spawner := &fakeSpawner{onSpawn: func() {
		stub.setDown(false)
		stub.setHealthStatus(503) // booting: live but not ready
		go func() {
			time.Sleep(80 * time.Millisecond)
			stub.setHealthStatus(200)
		}()
	}}
	m := NewManager(l, NewClient(stub.URL(), nil), spawner, "harness", "0.85.0")
	m.StartupWait = 5 * time.Second
	m.StartupPoll = 10 * time.Millisecond
	got := m.Reconcile(context.Background())
	if got.State != StateHealthy {
		t.Fatalf("State = %q (%s), want healthy after the engine finished starting", got.State, got.Detail)
	}
}

func TestManager_StartupWaitExpires_InstalledUnhealthy(t *testing.T) {
	l := NewLayout(t.TempDir())
	setupVerifiedVersion(t, l, "1.0.0", []byte("bytes"))
	stub := newStubSidecar()
	defer stub.Close()
	stub.setDown(true)
	spawner := &fakeSpawner{onSpawn: func() {
		stub.setDown(false)
		stub.setHealthStatus(503) // boots but never becomes ready
	}}
	m := NewManager(l, NewClient(stub.URL(), nil), spawner, "harness", "0.85.0")
	m.StartupWait = 60 * time.Millisecond
	m.StartupPoll = 10 * time.Millisecond
	got := m.Reconcile(context.Background())
	if got.State != StateInstalledUnhealthy || got.Reason != ReasonCrash {
		t.Fatalf("got %+v, want installed_unhealthy/crash", got)
	}
}

// TestManager_StatusNeverBlocksBehindSlowSpawn: Status() is what the
// Settings panel and the advisor probe poll; a slow spawn-wait must not
// hold it up.
func TestManager_StatusNeverBlocksBehindSlowSpawn(t *testing.T) {
	l := NewLayout(t.TempDir())
	setupVerifiedVersion(t, l, "1.0.0", []byte("bytes"))
	release := make(chan struct{})
	entered := make(chan struct{})
	spawner := &fakeSpawner{onSpawn: func() {
		close(entered)
		<-release
	}}
	m := NewManager(l, NewClient(unreachableBaseURL, nil), spawner, "harness", "0.85.0")
	done := make(chan struct{})
	go func() {
		m.Reconcile(context.Background())
		close(done)
	}()
	<-entered
	got := make(chan Status, 1)
	go func() { got <- m.Status() }()
	select {
	case <-got:
	case <-time.After(2 * time.Second):
		t.Fatal("Status() blocked behind an in-flight Reconcile")
	}
	close(release)
	<-done
}

type installFixture struct {
	channelRoot string
	name, sha   string
	mounter     *recordingMounter
}

func newInstallFixture(t *testing.T) installFixture {
	t.Helper()
	channelRoot := t.TempDir()
	name, sha := writeFixtureDMG(t, channelRoot)
	if err := os.WriteFile(filepath.Join(channelRoot, name+".sig"), []byte("sig-bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	return installFixture{channelRoot: channelRoot, name: name, sha: sha, mounter: &recordingMounter{volume: writeFixtureVolume(t, []byte("launcher-bytes"))}}
}

func newInstallManager(t *testing.T, l Layout, fx installFixture, client *Client, spawner Spawner) *Manager {
	t.Helper()
	m := NewManager(l, client, spawner, "harness", "0.85.0")
	m.Registry = testChannelRegistry()
	m.Creds = secrets.NoopResolver{}
	m.Verifier = Verifier{TrustVerifier: fakeTrustVerifier{ok: true, wantBytes: []byte("sig-bytes")}, Policy: integrity.SigningRequired}
	m.Mounter = fx.mounter
	return m
}

// TestManager_InstallAndActivate_DMGToHealthy is the install flow end to
// end against the local fixture: download -> verify -> mount -> copy ->
// start -> healthy, with each phase visible in Status while it runs.
func TestManager_InstallAndActivate_DMGToHealthy(t *testing.T) {
	fx := newInstallFixture(t)
	l := NewLayout(t.TempDir())
	stub := newStubSidecar()
	defer stub.Close()
	stub.setHealth(healthyStubPayload("1.2.0"))
	client := NewClient(unreachableBaseURL, nil)
	spawner := &fakeSpawner{onSpawn: func() { client.BaseURL = stub.URL() }}
	m := newInstallManager(t, l, fx, client, spawner)

	var duringUnpack Status
	fx.mounter.onAttach = func() { duringUnpack = m.Status() }

	got := m.InstallAndActivate(context.Background(), dmgRequest(fx.channelRoot, fx.name, fx.sha, nil))
	if got.State != StateHealthy {
		t.Fatalf("State = %q (%s), want healthy", got.State, got.Detail)
	}
	if duringUnpack.State != StateInstalling || !strings.HasPrefix(duringUnpack.Detail, "unpacking") {
		t.Errorf("status while unpacking = %+v, want installing/unpacking", duringUnpack)
	}
	if !spawner.called || !strings.HasSuffix(spawner.exePath, filepath.Join("1.2.0", "kameas-ml", "kameas-ml")) {
		t.Errorf("spawner exe = %q, want versions/1.2.0/kameas-ml/kameas-ml", spawner.exePath)
	}
	if rec, ok := m.Installed(); !ok || rec.Version != "1.2.0" {
		t.Errorf("Installed() = %+v, %v", rec, ok)
	}
}

func TestManager_InstallAndActivate_TamperedIsDigestMismatchAndNothingInstalled(t *testing.T) {
	fx := newInstallFixture(t)
	l := NewLayout(t.TempDir())
	m := newInstallManager(t, l, fx, NewClient(unreachableBaseURL, nil), &fakeSpawner{})
	got := m.InstallAndActivate(context.Background(), dmgRequest(fx.channelRoot, fx.name, "sha256:"+zeros64, nil))
	if got.State != StateNotInstalled || got.Reason != ReasonDigestMismatch || !strings.Contains(got.Detail, "install failed") {
		t.Fatalf("got %+v, want not_installed/digest_mismatch with an install-failed detail", got)
	}
	if attaches, _ := fx.mounter.snapshot(); len(attaches) != 0 {
		t.Fatalf("tampered artifact was mounted %d time(s)", len(attaches))
	}
	if _, ok := m.Installed(); ok {
		t.Fatal("tampered artifact must not leave an install record")
	}
}

func TestManager_InstallAndActivate_FailedReinstallKeepsExistingInstall(t *testing.T) {
	fx := newInstallFixture(t)
	l := NewLayout(t.TempDir())
	setupVerifiedVersion(t, l, "1.0.0", []byte("old"))
	m := newInstallManager(t, l, fx, NewClient(unreachableBaseURL, nil), &fakeSpawner{})
	got := m.InstallAndActivate(context.Background(), dmgRequest(fx.channelRoot, fx.name, "sha256:"+zeros64, nil))
	if got.State != StateInstalledUnhealthy || got.Reason != ReasonDigestMismatch {
		t.Fatalf("got %+v, want installed_unhealthy/digest_mismatch (old install still there)", got)
	}
	if rec, ok := m.Installed(); !ok || rec.Version != "1.0.0" {
		t.Fatalf("old install record lost: %+v %v", rec, ok)
	}
}

func TestManager_InstallAndActivate_VerifierFuncErrorRefusesWithoutMounting(t *testing.T) {
	fx := newInstallFixture(t)
	l := NewLayout(t.TempDir())
	m := newInstallManager(t, l, fx, NewClient(unreachableBaseURL, nil), &fakeSpawner{})
	m.VerifierFunc = func(context.Context) (Verifier, error) { return Verifier{}, errors.New("anchor store unavailable") }
	got := m.InstallAndActivate(context.Background(), dmgRequest(fx.channelRoot, fx.name, fx.sha, nil))
	if got.State != StateNotInstalled || got.Reason != ReasonDigestMismatch || !strings.Contains(got.Detail, "anchor store unavailable") {
		t.Fatalf("got %+v", got)
	}
	if attaches, _ := fx.mounter.snapshot(); len(attaches) != 0 {
		t.Fatal("an unresolvable verifier must never fall back to an unverified install")
	}
}

func TestManager_InstallAndActivate_SecondCallWhileInFlightReturnsInstalling(t *testing.T) {
	fx := newInstallFixture(t)
	l := NewLayout(t.TempDir())
	stub := newStubSidecar()
	defer stub.Close()
	stub.setHealth(healthyStubPayload("1.2.0"))
	client := NewClient(unreachableBaseURL, nil)
	m := newInstallManager(t, l, fx, client, &fakeSpawner{onSpawn: func() { client.BaseURL = stub.URL() }})
	var second Status
	fx.mounter.onAttach = func() {
		second = m.InstallAndActivate(context.Background(), dmgRequest(fx.channelRoot, fx.name, fx.sha, nil))
	}
	m.InstallAndActivate(context.Background(), dmgRequest(fx.channelRoot, fx.name, fx.sha, nil))
	if second.State != StateInstalling {
		t.Fatalf("re-entrant install returned %+v, want the in-flight installing status", second)
	}
	if attaches, _ := fx.mounter.snapshot(); len(attaches) != 1 {
		t.Fatalf("attaches = %d, want exactly 1 (no second install raced)", len(attaches))
	}
}

// TestManager_Uninstall_LeavesNothing is the clean-uninstall proof: no
// process asked to stay (token-authorized shutdown sent, Bearer, token
// trimmed), no weights, no config, no engine-written state, no root.
func TestManager_Uninstall_LeavesNothing(t *testing.T) {
	root := filepath.Join(t.TempDir(), "ml") // the wiring's harness-owned root
	l := NewLayout(root)
	setupVerifiedVersion(t, l, "1.0.0", []byte("bytes"))
	mustWrite(t, filepath.Join(root, "models", "weights.bin"), "weights")
	mustWrite(t, filepath.Join(root, "engine.log"), "log")
	mustWrite(t, filepath.Join(root, "checkpoints", "ab", "pack.bin"), "pack")
	mustWrite(t, filepath.Join(root, "lease", "shutdown.token"), "tok-123\n") // trailing newline, as a real engine may write
	stub := newStubSidecar()
	defer stub.Close()
	stub.setShutdownRequireToken("tok-123")
	m := NewManager(l, NewClient(stub.URL(), nil), nil, "harness", "0.85.0")

	if err := m.Uninstall(context.Background()); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	if stub.shutdownCallCount() != 1 {
		t.Fatalf("shutdown requests = %d, want 1 (sole leaseholder stops the engine)", stub.shutdownCallCount())
	}
	stub.mu.Lock()
	auth := stub.shutdownCalls[0]
	stub.mu.Unlock()
	if auth != "Bearer tok-123" {
		t.Errorf("Authorization = %q, want a trimmed Bearer token", auth)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("install root must be gone entirely (process+weights+config): stat err=%v", err)
	}
	if got := m.Status(); got.State != StateNotInstalled {
		t.Errorf("status after uninstall = %+v", got)
	}
	if _, ok := m.Installed(); ok {
		t.Error("Installed() must be false after uninstall")
	}
}

// TestManager_Uninstall_MispointedRootKeepsForeignFiles: a Manager whose
// root is not the harness-owned "ml" dir removes only the layout it knows
// — it must never RemoveAll a directory it does not own.
func TestManager_Uninstall_MispointedRootKeepsForeignFiles(t *testing.T) {
	root := t.TempDir()
	l := NewLayout(root)
	setupVerifiedVersion(t, l, "1.0.0", []byte("bytes"))
	mustWrite(t, filepath.Join(root, "foreign.txt"), "not ours")
	m := NewManager(l, NewClient(unreachableBaseURL, nil), nil, "harness", "0.85.0")
	if err := m.Uninstall(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "foreign.txt")); err != nil {
		t.Fatalf("foreign file was deleted: %v", err)
	}
	if _, err := os.Stat(l.VersionsDir()); !os.IsNotExist(err) {
		t.Errorf("versions dir should be gone: %v", err)
	}
	if _, ok := m.Installed(); ok {
		t.Error("install record should be gone")
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestEngineRelease_SizeMBRoundsUp(t *testing.T) {
	for in, want := range map[int64]int{0: 0, -1: 0, 1: 1, 1 << 20: 1, (1 << 20) + 1: 2, 200 << 20: 200} {
		if got := (EngineRelease{SizeBytes: in}).SizeMB(); got != want {
			t.Errorf("SizeMB(%d) = %d, want %d", in, got, want)
		}
	}
}

func TestPinnedEngineRelease_IsHonestlyUnavailableUntilPublished(t *testing.T) {
	// Pin the ZERO-value semantics explicitly, independent of whatever
	// pinned_release_gen.go a release build checks in.
	defer SetPinnedReleaseForTesting(EngineRelease{})()
	if _, err := PinnedEngineRelease(context.Background()); !errors.Is(err, ErrNoPublishedRelease) {
		t.Fatalf("PinnedEngineRelease err = %v, want ErrNoPublishedRelease", err)
	}
}

func TestPlatformSupport(t *testing.T) {
	if ok, _ := PlatformSupport("darwin", "arm64"); !ok {
		t.Error("darwin/arm64 must be supported")
	}
	for _, p := range [][2]string{{"darwin", "amd64"}, {"linux", "amd64"}, {"windows", "amd64"}} {
		if ok, why := PlatformSupport(p[0], p[1]); ok || why == "" {
			t.Errorf("%v: ok=%v reason=%q, want unsupported with a user-facing reason", p, ok, why)
		}
	}
}

// TestManager_KenazSeededInstall_IsInstalledAndProbeStarts is the
// A5(4) cross-client acceptance proof at the Manager level: an install
// record written by KENAZ (provenance kenaz-bundle-digest,
// Verified=false — Kenaz never sets the bit) counts as Installed, and
// Reconcile adopts the running engine as healthy when the tree
// matches. Before the WP13-merge fix, Installed() keyed on
// rec.Verified: the panel said "Not installed", the DemandProbe never
// started, and Enable would have clobbered the shared root.
func TestManager_KenazSeededInstall_IsInstalledAndProbeStarts(t *testing.T) {
	l := NewLayout(t.TempDir())
	exe, sha := setupSeededVersion(t, l, "1.0.0", []byte("kenaz-seeded"), ProvenanceKenazBundle)
	stub := newStubSidecar()
	defer stub.Close()
	h := healthyStubPayload("1.0.0")
	h.ExePath = exe
	h.EngineSHA256 = sha
	stub.setHealth(h)
	m := NewManager(l, NewClient(stub.URL(), nil), &fakeSpawner{}, "harness", "0.85.0")
	if _, ok := m.Installed(); !ok {
		t.Fatal("Installed() = false for a kenaz-bundle-digest record — the A5(4) cross-client rule is broken")
	}
	got := m.Reconcile(context.Background())
	if got.State != StateHealthy {
		t.Fatalf("Reconcile = %q (%s), want healthy adoption of the Kenaz-seeded engine", got.State, got.Detail)
	}
	p := &DemandProbe{M: m}
	defer p.Close()
	deadline := time.After(3 * time.Second)
	for !p.Healthy() { // Healthy() demands; the loop drives Ensure
		select {
		case <-deadline:
			t.Fatal("DemandProbe never became healthy on a Kenaz-seeded install")
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// TestManager_Uninstall_MlShapedRootWithoutRecordKeepsForeignFiles pins
// the WP13-review hardening (review F1): the ratified path SHAPE alone
// ("…/ml/<env>") never authorizes whole-root RemoveAll — a known install
// record must exist at entry. Deleting the hadKnownInstall guard in
// Uninstall makes this fail.
func TestManager_Uninstall_MlShapedRootWithoutRecordKeepsForeignFiles(t *testing.T) {
	root := filepath.Join(t.TempDir(), ".kenaz", "ml", "prod")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	foreign := filepath.Join(root, "somebody-elses-notes.txt")
	if err := os.WriteFile(foreign, []byte("not ours"), 0o600); err != nil {
		t.Fatal(err)
	}
	m := NewManager(NewLayout(root), nil, nil, "harness", "v")
	if err := m.Uninstall(context.Background()); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	if _, err := os.Stat(foreign); err != nil {
		t.Fatalf("a record-less ml-shaped root was RemoveAll'd (foreign file stat: %v) — path shape alone must never authorize whole-root removal", err)
	}
}
