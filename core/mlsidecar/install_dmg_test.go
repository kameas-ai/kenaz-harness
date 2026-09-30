package mlsidecar

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/bundle/channels/localpath"
	"github.com/kameas-ai/kenaz-harness/core/bundle/integrity"
	"github.com/kameas-ai/kenaz-harness/core/bundle/manifest"
	"github.com/kameas-ai/kenaz-harness/core/secrets"
)

// recordingMounter is the DMGMounter test double: it "mounts" by handing
// back a prepared volume directory, and records — race-safely — every
// Attach/Detach. It never runs hdiutil.
type recordingMounter struct {
	mu        sync.Mutex
	volume    string // directory standing in for the mounted volume
	attaches  []string
	detaches  int
	attachErr error
	onAttach  func()
}

func (r *recordingMounter) Attach(_ context.Context, dmgPath string) (string, func() error, error) {
	r.mu.Lock()
	if r.attachErr != nil {
		r.mu.Unlock()
		return "", nil, r.attachErr
	}
	r.attaches = append(r.attaches, dmgPath)
	hook := r.onAttach
	r.mu.Unlock()
	if hook != nil {
		hook()
	}
	return r.volume, func() error {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.detaches++
		return nil
	}, nil
}

func (r *recordingMounter) snapshot() (attaches []string, detaches int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.attaches...), r.detaches
}

// writeFixtureDMG writes dmg-shaped bytes (NOT a real disk image — the
// recording mounter never parses them; the real-hdiutil proof lives in
// dmg_darwin_test.go) into channelRoot/kenaz-ml.dmg and returns the A-1
// digest over exactly those bytes.
func writeFixtureDMG(t *testing.T, channelRoot string) (name, sha string) {
	t.Helper()
	name = "kenaz-ml.dmg"
	_, sha = writeArtifact(t, channelRoot, name, []byte("dmg-shaped-fixture-bytes: UDIF stand-in"))
	return name, sha
}

// writeFixtureVolume lays out what the real kenaz-ml DMG contains: a
// "kameas-ml" onedir whose launcher is kameas-ml/kameas-ml, with an
// _internal tree including a relative symlink (as PyInstaller macOS
// onedirs carry).
func writeFixtureVolume(t *testing.T, launcher []byte) string {
	t.Helper()
	vol := t.TempDir()
	onedir := filepath.Join(vol, EngineOnedirName)
	if err := os.MkdirAll(filepath.Join(onedir, "_internal", "lib"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(onedir, "kameas-ml"), launcher, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(onedir, "_internal", "lib", "libreal.dylib"), []byte("dylib"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("libreal.dylib", filepath.Join(onedir, "_internal", "lib", "liblink.dylib")); err != nil {
		t.Fatal(err)
	}
	return vol
}

func dmgRequest(channelRoot, name, sha string, m DMGMounter) InstallRequest {
	return InstallRequest{
		ChannelKind:    localpath.Kind,
		ChannelPath:    channelRoot,
		Version:        "1.2.0",
		ArtifactPath:   name,
		ExpectedSHA256: sha,
		Signature:      &manifest.SignatureRef{Kind: "ed25519_detached", Locator: name + ".sig", Algorithm: "ed25519", KeyID: "k1"},
		Source:         "local_path:" + channelRoot,
		Mounter:        m,
	}
}

func TestInstallDMG_HappyPath_VerifiesThenMountsCopiesDetaches(t *testing.T) {
	channelRoot := t.TempDir()
	name, sha := writeFixtureDMG(t, channelRoot)
	if err := os.WriteFile(filepath.Join(channelRoot, name+".sig"), []byte("sig-bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	mounter := &recordingMounter{volume: writeFixtureVolume(t, []byte("launcher-bytes"))}
	l := NewLayout(t.TempDir())
	v := Verifier{TrustVerifier: fakeTrustVerifier{ok: true, wantBytes: []byte("sig-bytes")}, Policy: integrity.SigningRequired}

	var phases []string
	req := dmgRequest(channelRoot, name, sha, mounter)
	req.Phase = func(p string) { phases = append(phases, p) }

	res, err := Install(context.Background(), l, testChannelRegistry(), secrets.NoopResolver{}, v, req)
	if err != nil {
		t.Fatalf("Install(dmg): %v", err)
	}
	attaches, detaches := mounter.snapshot()
	if len(attaches) != 1 || detaches != 1 {
		t.Fatalf("attach/detach = %d/%d, want 1/1 (image must always be detached)", len(attaches), detaches)
	}
	if filepath.Base(attaches[0]) != "1.2.0.dmg" {
		t.Errorf("attached staged file %q, want the staged <version>.dmg", attaches[0])
	}

	launcher := filepath.Join(res.VersionDir, "kameas-ml", "kameas-ml")
	if got, _ := os.ReadFile(launcher); string(got) != "launcher-bytes" {
		t.Errorf("launcher = %q", got)
	}
	if info, _ := os.Stat(launcher); info.Mode().Perm()&0o100 == 0 {
		t.Errorf("launcher lost its exec bit: %v", info.Mode())
	}
	link := filepath.Join(res.VersionDir, "kameas-ml", "_internal", "lib", "liblink.dylib")
	if target, err := os.Readlink(link); err != nil || target != "libreal.dylib" {
		t.Errorf("symlink not preserved: target=%q err=%v", target, err)
	}
	// Staged dmg is gone; install.json holds the LAUNCHER digest (what
	// adoption re-hashes), not the dmg's.
	if _, err := os.Stat(filepath.Join(l.Root, ".staging", "1.2.0.dmg")); !os.IsNotExist(err) {
		t.Errorf("staged dmg should be removed after install, stat err=%v", err)
	}
	wantExe, _ := HashFileSHA256(launcher)
	rec, ok, _ := ReadInstallJSON(l)
	if !ok || rec.EngineSHA256 != wantExe || rec.EngineSHA256 == sha || !rec.Verified {
		t.Errorf("install.json = %+v (ok=%v), want launcher digest %s, verified", rec, ok, wantExe)
	}
	want := []string{"downloading", "verifying", "unpacking"}
	if len(phases) != len(want) {
		t.Fatalf("phases = %v, want %v", phases, want)
	}
	for i := range want {
		if phases[i] != want[i] {
			t.Fatalf("phases = %v, want %v", phases, want)
		}
	}
}

// TestInstallDMG_TamperedBytes_NeverMounted is the dmg verify-order
// planted proof: the A-1 manifest wraps the DMG BYTES, so a DMG whose
// bytes do not match must be refused BEFORE hdiutil ever sees it. The
// recording mounter's attach count is the witness — 0 means the image
// was never mounted, never copied, never given a version dir.
func TestInstallDMG_TamperedBytes_NeverMounted(t *testing.T) {
	channelRoot := t.TempDir()
	name, _ := writeFixtureDMG(t, channelRoot)
	mounter := &recordingMounter{volume: writeFixtureVolume(t, []byte("launcher"))}
	l := NewLayout(t.TempDir())
	v := Verifier{TrustVerifier: fakeTrustVerifier{ok: true}, Policy: integrity.SigningOptional}

	req := dmgRequest(channelRoot, name, "sha256:"+zeros64, mounter)
	req.Signature = nil
	_, err := Install(context.Background(), l, testChannelRegistry(), secrets.NoopResolver{}, v, req)
	if err == nil {
		t.Fatal("expected a tampered dmg to be refused")
	}
	if !errors.Is(err, ErrVerificationFailed) || !errors.Is(err, ErrDigestMismatch) {
		t.Errorf("err = %v, want ErrVerificationFailed wrapping ErrDigestMismatch", err)
	}
	if attaches, _ := mounter.snapshot(); len(attaches) != 0 {
		t.Fatalf("tampered dmg was mounted %d time(s): verification must run BEFORE mount", len(attaches))
	}
	assertNothingInstalled(t, l)
}

// TestInstallDMG_UnsignedRefusedBeforeMount: same ordering proof for the
// signature half — SigningRequired + no signature must refuse pre-mount.
func TestInstallDMG_UnsignedRefusedBeforeMount(t *testing.T) {
	channelRoot := t.TempDir()
	name, sha := writeFixtureDMG(t, channelRoot)
	mounter := &recordingMounter{volume: writeFixtureVolume(t, []byte("launcher"))}
	l := NewLayout(t.TempDir())
	v := DefaultEngineVerifier(fakeTrustVerifier{ok: true}, nil)

	req := dmgRequest(channelRoot, name, sha, mounter)
	req.Signature = nil
	if _, err := Install(context.Background(), l, testChannelRegistry(), secrets.NoopResolver{}, v, req); err == nil {
		t.Fatal("expected an unsigned dmg to be refused under SigningRequired")
	}
	if attaches, _ := mounter.snapshot(); len(attaches) != 0 {
		t.Fatalf("unsigned dmg was mounted %d time(s)", len(attaches))
	}
	assertNothingInstalled(t, l)
}

func TestInstallDMG_BadSignature_RefusedBeforeMount(t *testing.T) {
	channelRoot := t.TempDir()
	name, sha := writeFixtureDMG(t, channelRoot)
	if err := os.WriteFile(filepath.Join(channelRoot, name+".sig"), []byte("sig-bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	mounter := &recordingMounter{volume: writeFixtureVolume(t, []byte("launcher"))}
	l := NewLayout(t.TempDir())
	v := Verifier{TrustVerifier: fakeTrustVerifier{ok: false}, Policy: integrity.SigningRequired}
	if _, err := Install(context.Background(), l, testChannelRegistry(), secrets.NoopResolver{}, v, dmgRequest(channelRoot, name, sha, mounter)); err == nil {
		t.Fatal("expected an invalidly-signed dmg to be refused")
	}
	if attaches, _ := mounter.snapshot(); len(attaches) != 0 {
		t.Fatalf("badly-signed dmg was mounted %d time(s)", len(attaches))
	}
	assertNothingInstalled(t, l)
}

func TestInstallDMG_VolumeMissingOnedir_RefusesAndDetaches(t *testing.T) {
	channelRoot := t.TempDir()
	name, sha := writeFixtureDMG(t, channelRoot)
	mounter := &recordingMounter{volume: t.TempDir()} // empty volume
	l := NewLayout(t.TempDir())
	v := Verifier{Policy: integrity.SigningOptional}
	req := dmgRequest(channelRoot, name, sha, mounter)
	req.Signature = nil
	if _, err := Install(context.Background(), l, testChannelRegistry(), secrets.NoopResolver{}, v, req); err == nil {
		t.Fatal("expected a volume without the kameas-ml onedir to be refused")
	}
	if _, detaches := mounter.snapshot(); detaches != 1 {
		t.Errorf("detaches = %d, want 1 even on failure", detaches)
	}
	assertNothingInstalled(t, l)
}

func TestInstallDMG_AttachFailure_RefusesCleanly(t *testing.T) {
	channelRoot := t.TempDir()
	name, sha := writeFixtureDMG(t, channelRoot)
	mounter := &recordingMounter{attachErr: errors.New("hdiutil: resource busy")}
	l := NewLayout(t.TempDir())
	v := Verifier{Policy: integrity.SigningOptional}
	req := dmgRequest(channelRoot, name, sha, mounter)
	req.Signature = nil
	if _, err := Install(context.Background(), l, testChannelRegistry(), secrets.NoopResolver{}, v, req); err == nil {
		t.Fatal("expected an attach failure to fail the install")
	}
	assertNothingInstalled(t, l)
}

func TestCopyTree_RefusesAbsoluteAndEscapingSymlinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks")
	}
	for name, link := range map[string]string{"absolute": "/etc/passwd", "escaping": "../../outside"} {
		t.Run(name, func(t *testing.T) {
			src := filepath.Join(t.TempDir(), EngineOnedirName)
			if err := os.MkdirAll(filepath.Join(src, "a"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(link, filepath.Join(src, "a", "evil")); err != nil {
				t.Fatal(err)
			}
			dst := filepath.Join(t.TempDir(), EngineOnedirName)
			if err := copyTree(src, dst); err == nil {
				t.Fatalf("copyTree accepted a %s symlink", name)
			}
		})
	}
}

func TestIsDMGArtifact(t *testing.T) {
	for p, want := range map[string]bool{"a/kenaz-ml.dmg": true, "KENAZ-ML.DMG": true, "engine.zip": false, "dmg": false} {
		if got := isDMGArtifact(p); got != want {
			t.Errorf("isDMGArtifact(%q) = %v, want %v", p, got, want)
		}
	}
}

const zeros64 = "0000000000000000000000000000000000000000000000000000000000000000"

func assertNothingInstalled(t *testing.T, l Layout) {
	t.Helper()
	if _, err := os.Stat(l.VersionDir("1.2.0")); !os.IsNotExist(err) {
		t.Errorf("version dir must not exist after a refused install, stat err=%v", err)
	}
	if _, err := os.Lstat(l.CurrentLink()); !os.IsNotExist(err) {
		t.Errorf("`current` must not exist after a refused install, err=%v", err)
	}
	if _, ok, _ := ReadInstallJSON(l); ok {
		t.Error("install.json must not exist after a refused install")
	}
}
