//go:build darwin

package mlsidecar

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/bundle/integrity"
	"github.com/kameas-ai/kenaz-harness/core/secrets"
)

// TestInstallDMG_RealHdiutil drives the REAL hdiutil mounter end to end
// against a DMG this test builds itself (hdiutil create -srcfolder, volume
// "kenaz-ml", a kameas-ml onedir at the root — the real artifact's shape).
// It skips, loudly, when hdiutil is unavailable or refuses to create an
// image in this environment (sandboxed CI), so the fake-mounter tests in
// install_dmg_test.go remain the always-on proof.
func TestInstallDMG_RealHdiutil(t *testing.T) {
	if _, err := exec.LookPath("hdiutil"); err != nil {
		t.Skip("hdiutil not available")
	}
	src := t.TempDir()
	vol := filepath.Join(src, EngineOnedirName)
	if err := os.MkdirAll(filepath.Join(vol, "_internal"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(vol, "kameas-ml"), []byte("real-dmg-launcher"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(vol, "_internal", "x.bin"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("x.bin", filepath.Join(vol, "_internal", "y.bin")); err != nil {
		t.Fatal(err)
	}

	channelRoot := t.TempDir()
	dmgPath := filepath.Join(channelRoot, "kenaz-ml.dmg")
	if out, err := exec.Command("hdiutil", "create", "-volname", EngineVolumeName, "-srcfolder", src, "-ov", "-format", "UDZO", dmgPath).CombinedOutput(); err != nil {
		t.Skipf("hdiutil create unavailable here: %v: %s", err, out)
	}
	sha, err := HashFileSHA256(dmgPath)
	if err != nil {
		t.Fatal(err)
	}

	l := NewLayout(t.TempDir())
	v := Verifier{Policy: integrity.SigningOptional}
	req := dmgRequest(channelRoot, "kenaz-ml.dmg", sha, DefaultDMGMounter())
	req.Signature = nil
	res, err := Install(context.Background(), l, testChannelRegistry(), secrets.NoopResolver{}, v, req)
	if err != nil {
		t.Skipf("hdiutil attach unavailable here (sandbox?): %v", err)
	}
	launcher := filepath.Join(res.VersionDir, "kameas-ml", "kameas-ml")
	if got, _ := os.ReadFile(launcher); string(got) != "real-dmg-launcher" {
		t.Errorf("launcher = %q", got)
	}
	if info, _ := os.Stat(launcher); info.Mode().Perm()&0o100 == 0 {
		t.Errorf("launcher lost its exec bit through a real dmg: %v", info.Mode())
	}
	if target, err := os.Readlink(filepath.Join(res.VersionDir, "kameas-ml", "_internal", "y.bin")); err != nil || target != "x.bin" {
		t.Errorf("symlink through real dmg: %q %v", target, err)
	}
	// A tampered real dmg never reaches hdiutil attach: flip a byte and
	// re-install; verification must refuse.
	b, _ := os.ReadFile(dmgPath)
	b[len(b)/2] ^= 0xff
	if err := os.WriteFile(dmgPath, b, 0o644); err != nil {
		t.Fatal(err)
	}
	l2 := NewLayout(t.TempDir())
	if _, err := Install(context.Background(), l2, testChannelRegistry(), secrets.NoopResolver{}, v, req); err == nil {
		t.Fatal("tampered real dmg must be refused")
	}
	if _, err := os.Stat(l2.VersionDir("1.2.0")); !os.IsNotExist(err) {
		t.Errorf("tampered real dmg left a version dir: %v", err)
	}
}
