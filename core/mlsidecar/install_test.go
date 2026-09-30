package mlsidecar

import (
	"archive/zip"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/bundle/channels"
	"github.com/kameas-ai/kenaz-harness/core/bundle/channels/localpath"
	"github.com/kameas-ai/kenaz-harness/core/bundle/integrity"
	"github.com/kameas-ai/kenaz-harness/core/bundle/manifest"
	"github.com/kameas-ai/kenaz-harness/core/secrets"
)

// buildTestEngineZip writes a minimal zip (one file,
// kameas-ml/kameas-ml, with the given content) at dir/name and returns
// its sha256 digest. This is the "engine artifact" every install.go test
// downloads through a local_path channel — no real network, no real
// onedir, per the WP12 brief's hard constraints.
func buildTestEngineZip(t *testing.T, dir, name string, content []byte) (path, sha string) {
	t.Helper()
	path = filepath.Join(dir, name)
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create zip: %v", err)
	}
	zw := zip.NewWriter(f)
	w, err := zw.Create("kameas-ml/kameas-ml")
	if err != nil {
		t.Fatalf("zip create entry: %v", err)
	}
	if _, err := w.Write(content); err != nil {
		t.Fatalf("zip write: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close zip file: %v", err)
	}
	rf, err := os.Open(path)
	if err != nil {
		t.Fatalf("reopen zip: %v", err)
	}
	defer rf.Close()
	sha, _, err = integrity.HashSHA256(rf)
	if err != nil {
		t.Fatalf("hash zip: %v", err)
	}
	return path, sha
}

func testChannelRegistry() channels.Registry {
	r := channels.NewRegistry()
	_ = r.Register(localpath.Kind, localpath.Factory)
	return r
}

// TestInstall_HappyPath_VerifiesUnpacksAndFlipsCurrent covers the full
// design §6.2 install sequence against a local_path channel: fetch,
// verify (signature + sha256), unpack, (darwin-guarded) clear
// quarantine, flip `current`, write install.json.
func TestInstall_HappyPath_VerifiesUnpacksAndFlipsCurrent(t *testing.T) {
	channelRoot := t.TempDir()
	zipPath, sha := buildTestEngineZip(t, channelRoot, "engine.zip", []byte("the real engine bytes"))
	_ = zipPath
	if err := os.WriteFile(filepath.Join(channelRoot, "engine.zip.sig"), []byte("sig-bytes"), 0o600); err != nil {
		t.Fatalf("write sig: %v", err)
	}

	l := NewLayout(t.TempDir())
	req := InstallRequest{
		ChannelKind:    localpath.Kind,
		ChannelPath:    channelRoot,
		Version:        "1.0.0",
		ArtifactPath:   "engine.zip",
		ExpectedSHA256: sha,
		Signature:      &manifest.SignatureRef{Kind: "ed25519_detached", Locator: "engine.zip.sig", Algorithm: "ed25519", KeyID: "k1"},
		Source:         "local_path:" + channelRoot,
	}
	v := Verifier{TrustVerifier: fakeTrustVerifier{ok: true, wantBytes: []byte("sig-bytes")}, Policy: integrity.SigningRequired}

	res, err := Install(context.Background(), l, testChannelRegistry(), secrets.NoopResolver{}, v, req)
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if !res.Record.Verified {
		t.Error("expected Record.Verified=true")
	}
	unpacked := filepath.Join(res.VersionDir, "kameas-ml", "kameas-ml")
	got, err := os.ReadFile(unpacked)
	if err != nil {
		t.Fatalf("read unpacked engine file: %v", err)
	}
	if string(got) != "the real engine bytes" {
		t.Errorf("unpacked content = %q", got)
	}

	current, err := l.CurrentVersionDir()
	if err != nil {
		t.Fatalf("CurrentVersionDir: %v", err)
	}
	if filepath.Base(current) != "1.0.0" {
		t.Errorf("current = %s, want a path ending in 1.0.0", current)
	}

	rec, ok, err := ReadInstallJSON(l)
	if err != nil || !ok {
		t.Fatalf("ReadInstallJSON: ok=%v err=%v", ok, err)
	}
	// install.json's EngineSHA256 is the digest of the UNPACKED
	// EXECUTABLE (what a later adoption re-hashes), not the zip's own
	// digest (sha) — the zip is deleted after unpacking.
	wantExeDigest, err := HashFileSHA256(unpacked)
	if err != nil {
		t.Fatalf("HashFileSHA256: %v", err)
	}
	if rec.Version != "1.0.0" || rec.EngineSHA256 != wantExeDigest {
		t.Errorf("install.json record = %+v, want EngineSHA256=%s", rec, wantExeDigest)
	}
	if rec.EngineSHA256 == sha {
		t.Error("install.json's EngineSHA256 must be the unpacked executable's digest, not the zip's")
	}
}

// TestInstall_TamperedArtifact_NeverUnpackedOrActivated is the
// tampered-artifact-never-executes planted proof at the install layer: a
// manifest with a correctly-signed but WRONG digest (simulating a
// tampered/corrupted download) must refuse before unpacking anything —
// no version directory, no `current` flip, no install.json.
func TestInstall_TamperedArtifact_NeverUnpackedOrActivated(t *testing.T) {
	channelRoot := t.TempDir()
	buildTestEngineZip(t, channelRoot, "engine.zip", []byte("the real engine bytes"))
	if err := os.WriteFile(filepath.Join(channelRoot, "engine.zip.sig"), []byte("sig-bytes"), 0o600); err != nil {
		t.Fatalf("write sig: %v", err)
	}

	l := NewLayout(t.TempDir())
	req := InstallRequest{
		ChannelKind:  localpath.Kind,
		ChannelPath:  channelRoot,
		Version:      "1.0.0",
		ArtifactPath: "engine.zip",
		// Wrong digest — as if the manifest were tampered with, or the
		// download corrupted in transit.
		ExpectedSHA256: "sha256:0000000000000000000000000000000000000000000000000000000000000000",
		Signature:      &manifest.SignatureRef{Kind: "ed25519_detached", Locator: "engine.zip.sig"},
		Source:         "local_path:" + channelRoot,
	}
	v := Verifier{TrustVerifier: fakeTrustVerifier{ok: true}, Policy: integrity.SigningRequired}

	_, err := Install(context.Background(), l, testChannelRegistry(), secrets.NoopResolver{}, v, req)
	if err == nil {
		t.Fatal("expected Install to refuse a digest-mismatched artifact")
	}
	if _, statErr := os.Stat(l.VersionDir("1.0.0")); !os.IsNotExist(statErr) {
		t.Errorf("expected NO version directory after a refused install, stat err=%v", statErr)
	}
	if _, statErr := os.Lstat(l.CurrentLink()); !os.IsNotExist(statErr) {
		t.Errorf("expected `current` to remain absent after a refused install, stat err=%v", statErr)
	}
	if _, ok, _ := ReadInstallJSON(l); ok {
		t.Error("expected no install.json record after a refused install")
	}
}

// TestInstall_UnsignedArtifact_RefusedUnderSigningRequired proves the
// default engine-download policy actually refuses an unsigned artifact —
// "verify BEFORE first run" has teeth only if this holds.
func TestInstall_UnsignedArtifact_RefusedUnderSigningRequired(t *testing.T) {
	channelRoot := t.TempDir()
	_, sha := buildTestEngineZip(t, channelRoot, "engine.zip", []byte("bytes"))

	l := NewLayout(t.TempDir())
	req := InstallRequest{
		ChannelKind:    localpath.Kind,
		ChannelPath:    channelRoot,
		Version:        "1.0.0",
		ArtifactPath:   "engine.zip",
		ExpectedSHA256: sha,
		Source:         "local_path:" + channelRoot,
	}
	v := DefaultEngineVerifier(fakeTrustVerifier{ok: true}, nil)
	if _, err := Install(context.Background(), l, testChannelRegistry(), secrets.NoopResolver{}, v, req); err == nil {
		t.Fatal("expected Install to refuse an unsigned artifact under the default SigningRequired policy")
	}
}
