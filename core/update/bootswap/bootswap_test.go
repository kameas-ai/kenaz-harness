package bootswap

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// fakeRelauncher captures the relaunch call without exiting.
type fakeRelauncher struct {
	mu     sync.Mutex
	target string
	args   []string
	err    error
	calls  int
}

func (f *fakeRelauncher) Relaunch(target string, args []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.target = target
	f.args = append([]string{}, args...)
	return f.err
}

func writeMarker(t *testing.T, dataDir string, m marker) string {
	t.Helper()
	dir := filepath.Join(dataDir, "update")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "pending.json")
	body, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func writeStaged(t *testing.T, path string, body []byte) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0o755); err != nil {
		t.Fatal(err)
	}
	h := sha256.Sum256(body)
	return hex.EncodeToString(h[:])
}

func TestMaybeSwapAndRelaunch_NoMarker_NoOp(t *testing.T) {
	dir := t.TempDir()
	r := &fakeRelauncher{}
	res, err := MaybeSwapAndRelaunch(Config{
		DataDir:    dir,
		Relauncher: r,
	})
	if err != nil {
		t.Fatalf("expected nil err: %v", err)
	}
	if res.Swapped {
		t.Fatal("expected Swapped=false")
	}
	if r.calls != 0 {
		t.Fatal("relauncher should not be called")
	}
}

func TestMaybeSwapAndRelaunch_HappyPath(t *testing.T) {
	dir := t.TempDir()
	stagedPath := filepath.Join(dir, "update", "staging", "kenaz.exe.new")
	targetPath := filepath.Join(dir, "kenaz.exe")
	body := []byte("new binary")
	sha := writeStaged(t, stagedPath, body)
	if err := os.WriteFile(targetPath, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	markerPath := writeMarker(t, dir, marker{
		TargetPath:    targetPath,
		StagedPath:    stagedPath,
		Sha256:        sha,
		TargetVersion: "v0.4.0",
		Platform:      "windows/amd64",
	})

	r := &fakeRelauncher{}
	res, err := MaybeSwapAndRelaunch(Config{
		DataDir:    dir,
		Relauncher: r,
		Args:       []string{"--flag"},
	})
	if err != nil {
		t.Fatalf("MaybeSwapAndRelaunch: %v", err)
	}
	if !res.Swapped {
		t.Fatal("expected Swapped=true")
	}
	if res.TargetVersion != "v0.4.0" {
		t.Fatalf("res = %+v", res)
	}

	// Marker should be deleted.
	if _, err := os.Stat(markerPath); !os.IsNotExist(err) {
		t.Fatalf("marker should be deleted, stat: %v", err)
	}

	// Target should now hold the staged contents.
	got, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(body) {
		t.Fatalf("target contents = %q, want %q", got, body)
	}

	// Relauncher should have been called with the target path.
	if r.target != targetPath {
		t.Fatalf("relauncher target = %q want %q", r.target, targetPath)
	}
	if len(r.args) != 1 || r.args[0] != "--flag" {
		t.Fatalf("relauncher args = %v", r.args)
	}
}

func TestMaybeSwapAndRelaunch_ShaMismatch_DeletesEverything(t *testing.T) {
	dir := t.TempDir()
	stagedPath := filepath.Join(dir, "update", "staging", "kenaz.exe.new")
	targetPath := filepath.Join(dir, "kenaz.exe")
	writeStaged(t, stagedPath, []byte("payload"))
	if err := os.WriteFile(targetPath, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	markerPath := writeMarker(t, dir, marker{
		TargetPath:    targetPath,
		StagedPath:    stagedPath,
		Sha256:        "deadbeef" + "0000000000000000000000000000000000000000000000000000000000",
		TargetVersion: "v0.4.0",
	})

	r := &fakeRelauncher{}
	res, err := MaybeSwapAndRelaunch(Config{
		DataDir:    dir,
		Relauncher: r,
	})
	if err == nil {
		t.Fatal("expected sha-mismatch error")
	}
	if res.Swapped {
		t.Fatal("Swapped should be false on sha mismatch")
	}
	if r.calls != 0 {
		t.Fatal("relauncher should not be called")
	}
	if _, err := os.Stat(markerPath); !os.IsNotExist(err) {
		t.Fatal("marker should be deleted on sha mismatch")
	}
	if _, err := os.Stat(stagedPath); !os.IsNotExist(err) {
		t.Fatal("staged file should be deleted on sha mismatch")
	}
	// Target untouched.
	got, _ := os.ReadFile(targetPath)
	if string(got) != "old" {
		t.Fatalf("target should be untouched, got %q", got)
	}
}

func TestMaybeSwapAndRelaunch_CorruptMarker(t *testing.T) {
	dir := t.TempDir()
	dirUpdate := filepath.Join(dir, "update")
	_ = os.MkdirAll(dirUpdate, 0o755)
	markerPath := filepath.Join(dirUpdate, "pending.json")
	if err := os.WriteFile(markerPath, []byte("not valid json"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := &fakeRelauncher{}
	_, err := MaybeSwapAndRelaunch(Config{
		DataDir:    dir,
		Relauncher: r,
	})
	if err == nil {
		t.Fatal("expected error for corrupt marker")
	}
	if _, err := os.Stat(markerPath); !os.IsNotExist(err) {
		t.Fatal("corrupt marker should be deleted")
	}
}

func TestMaybeSwapAndRelaunch_MissingFields(t *testing.T) {
	dir := t.TempDir()
	writeMarker(t, dir, marker{TargetVersion: "v0.4.0"}) // missing required
	_, err := MaybeSwapAndRelaunch(Config{
		DataDir:    dir,
		Relauncher: &fakeRelauncher{},
	})
	if err == nil {
		t.Fatal("expected error for missing fields")
	}
}

func TestMaybeSwapAndRelaunch_RequiresDataDir(t *testing.T) {
	if _, err := MaybeSwapAndRelaunch(Config{}); err == nil {
		t.Fatal("expected error for empty DataDir")
	}
}

func TestMaybeSwapAndRelaunch_StagedPathMissing(t *testing.T) {
	dir := t.TempDir()
	writeMarker(t, dir, marker{
		TargetPath: filepath.Join(dir, "kenaz.exe"),
		StagedPath: filepath.Join(dir, "missing.exe"),
		Sha256:     "abc",
	})
	_, err := MaybeSwapAndRelaunch(Config{
		DataDir:    dir,
		Relauncher: &fakeRelauncher{},
	})
	if err == nil {
		t.Fatal("expected error for missing staged path")
	}
}
