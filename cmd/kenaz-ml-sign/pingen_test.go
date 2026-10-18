package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/mlsidecar"
)

const (
	pgURL  = "https://dev.downloads.kameas.ai/kenaz-ml/1.2.0"
	pgDMG  = "kenaz-ml-1.2.0-darwin-arm64.dmg"
	pgSHA  = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	pgKey  = "fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210"
	pgSize = "132120576"
)

// TestPinGen_WritesAPinThatCompiles drives pin-gen with the exact flags
// the release.yml engine-pin step passes, then proves the generated
// file is (a) what mlsidecar renders for the canonical pin and (b) a
// valid replacement for the checked-in pinned_release_gen.go — the
// package BUILDS with it (go build -overlay), so a release never
// discovers a broken pin at wails-build time.
func TestPinGen_WritesAPinThatCompiles(t *testing.T) {
	out := filepath.Join(t.TempDir(), "pinned_release_gen.go")
	code, stdout, stderr := runCmd(t, noEnv, "pin-gen", "--version", "1.2.0", "--channel-url", pgURL,
		"--artifact-path", pgDMG, "--sha256", pgSHA, "--size", pgSize, "--key-id", pgKey, "--out", out)
	if code != exitOK {
		t.Fatalf("pin-gen exit %d: %s", code, stderr)
	}
	if !strings.Contains(stdout, "1.2.0") {
		t.Fatalf("stdout = %q", stdout)
	}
	got, _ := os.ReadFile(out)
	rel, err := mlsidecar.NewHTTPMirrorPin("1.2.0", pgURL, pgDMG, pgSHA, 132120576, pgKey)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := mlsidecar.RenderPinnedReleaseSource(rel)
	if !bytes.Equal(got, want) {
		t.Fatalf("pin-gen output differs from the canonical render:\n%s", got)
	}

	repoRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	overlay, _ := json.Marshal(map[string]map[string]string{"Replace": {
		filepath.Join(repoRoot, mlsidecar.PinnedReleaseGenFile): out,
	}})
	overlayPath := filepath.Join(t.TempDir(), "overlay.json")
	if err := os.WriteFile(overlayPath, overlay, 0o644); err != nil {
		t.Fatal(err)
	}
	build := exec.Command("go", "build", "-overlay", overlayPath, "./core/mlsidecar/")
	build.Dir = repoRoot
	if b, err := build.CombinedOutput(); err != nil {
		t.Fatalf("core/mlsidecar does not build with the generated pin: %v\n%s", err, b)
	}
}

func TestPinGen_ZeroMatchesCheckedInDefaultAndBadInputsFail(t *testing.T) {
	out := filepath.Join(t.TempDir(), "zero.go")
	if code, _, e := runCmd(t, noEnv, "pin-gen", "--zero", "--out", out); code != exitOK {
		t.Fatalf("pin-gen --zero: %d %s", code, e)
	}
	got, _ := os.ReadFile(out)
	want, _ := mlsidecar.RenderPinnedReleaseSource(mlsidecar.EngineRelease{})
	if !bytes.Equal(got, want) {
		t.Fatal("--zero output is not the zero-value render")
	}
	bad := [][]string{
		{"pin-gen", "--zero", "--version", "1.2.0", "--out", out},
		{"pin-gen", "--version", "1.2.0", "--channel-url", "http://insecure/x", "--artifact-path", pgDMG, "--sha256", pgSHA, "--size", pgSize, "--key-id", pgKey, "--out", out},
		{"pin-gen", "--version", "1.2.0", "--channel-url", pgURL, "--artifact-path", pgDMG, "--sha256", pgSHA, "--size", "0", "--key-id", pgKey, "--out", out},
		{"pin-gen", "--out", out},
	}
	for _, args := range bad {
		if code, _, _ := runCmd(t, noEnv, args...); code != exitUsage {
			t.Errorf("%v: exit %d, want %d", args, code, exitUsage)
		}
	}
	if after, _ := os.ReadFile(out); !bytes.Equal(after, want) {
		t.Fatal("a refused pin-gen overwrote the output file")
	}
}
