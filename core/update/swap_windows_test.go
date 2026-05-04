//go:build windows

package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeHelperSpawner records every Spawn call without launching a real
// process. Returns spawnErr if set; otherwise nil. Used to drive the
// swap_windows.go flow without touching the OS process tree.
type fakeHelperSpawner struct {
	calls    []fakeSpawnCall
	spawnErr error
}

type fakeSpawnCall struct {
	helperPath string
	args       []string
}

func (f *fakeHelperSpawner) Spawn(helperPath string, args []string) error {
	f.calls = append(f.calls, fakeSpawnCall{helperPath: helperPath, args: append([]string(nil), args...)})
	return f.spawnErr
}

// withSpawner swaps the package-private windowsSpawner for the
// duration of a test. Returns a restore func the caller defers.
func withSpawner(s helperSpawner) func() {
	prev := windowsSpawner
	windowsSpawner = s
	return func() { windowsSpawner = prev }
}

// stageBlob writes a file with the given body and returns (path,
// hex-sha256). Used by the helper-spawn tests to satisfy the
// pre-swap re-verify call without depending on the parent harness.
func stageBlob(t *testing.T, dir, name, body string) (string, string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(body))
	return path, hex.EncodeToString(sum[:])
}

// TestSwap_HelperSpawnHappyPath asserts that when kenaz-updater.exe
// exists next to the running binary, Swap dispatches to the helper
// and does NOT write the deferred-pending marker. This is the
// production-default path on a fresh v0.4.0 install.
func TestSwap_HelperSpawnHappyPath(t *testing.T) {
	dir := t.TempDir()
	dataDir := filepath.Join(dir, "data")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Both the running binary AND the helper must exist next to each
	// other in the install dir.
	binDir := filepath.Join(dir, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	runningBin := filepath.Join(binDir, "harness.exe")
	if err := os.WriteFile(runningBin, []byte("running"), 0o644); err != nil {
		t.Fatal(err)
	}
	helperPath := filepath.Join(binDir, helperBinaryName)
	if err := os.WriteFile(helperPath, []byte("helper"), 0o644); err != nil {
		t.Fatal(err)
	}

	stagedPath, digest := stageBlob(t, dir, "staged.exe", "new contents")
	staged := StagedUpdate{
		Path:          stagedPath,
		TargetVersion: "v0.4.1",
		Sha256:        digest,
		Platform:      "windows/amd64",
	}

	spawner := &fakeHelperSpawner{}
	defer withSpawner(spawner)()

	if err := (realSwapper{}).Swap(context.Background(), staged, runningBin, dataDir); err != nil {
		t.Fatalf("Swap: %v", err)
	}
	if len(spawner.calls) != 1 {
		t.Fatalf("expected 1 spawn, got %d", len(spawner.calls))
	}
	c := spawner.calls[0]
	if c.helperPath != helperPath {
		t.Errorf("helperPath = %s want %s", c.helperPath, helperPath)
	}
	joined := strings.Join(c.args, " ")
	for _, want := range []string{"--parent-pid", "--staged", stagedPath, "--target", runningBin, "--sha256", digest} {
		if !strings.Contains(joined, want) {
			t.Errorf("args missing %q: %s", want, joined)
		}
	}
	// Helper was invoked → no pending marker should have been written.
	if _, err := os.Stat(filepath.Join(dataDir, "update", "pending.json")); !os.IsNotExist(err) {
		t.Errorf("pending.json should NOT exist on helper-spawn path: err=%v", err)
	}
}

// TestSwap_HelperMissingFallsBack covers the "kenaz-updater.exe not
// bundled" case. The realSwapper is supposed to fall back to the
// legacy deferred-pending-marker path so the bootswap shim can pick
// the swap up at next launch — never just bail with an error.
func TestSwap_HelperMissingFallsBack(t *testing.T) {
	dir := t.TempDir()
	dataDir := filepath.Join(dir, "data")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatal(err)
	}

	binDir := filepath.Join(dir, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	runningBin := filepath.Join(binDir, "harness.exe")
	if err := os.WriteFile(runningBin, []byte("running"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Note: kenaz-updater.exe is intentionally NOT created here.

	stagedPath, digest := stageBlob(t, dir, "staged.exe", "new contents")
	staged := StagedUpdate{
		Path:          stagedPath,
		TargetVersion: "v0.4.1",
		Sha256:        digest,
		Platform:      "windows/amd64",
	}

	spawner := &fakeHelperSpawner{}
	defer withSpawner(spawner)()

	if err := (realSwapper{}).Swap(context.Background(), staged, runningBin, dataDir); err != nil {
		t.Fatalf("Swap: %v", err)
	}
	if len(spawner.calls) != 0 {
		t.Errorf("spawner should NOT be called when helper is missing: %d calls", len(spawner.calls))
	}
	// Fallback DOES write the pending marker.
	markerPath := filepath.Join(dataDir, "update", "pending.json")
	if _, err := os.Stat(markerPath); err != nil {
		t.Errorf("pending.json should exist on fallback path: %v", err)
	}
	m, ok, err := readPendingMarker(markerPath)
	if err != nil || !ok {
		t.Fatalf("readPendingMarker: ok=%v err=%v", ok, err)
	}
	if m.Sha256 != digest {
		t.Errorf("marker sha256 = %s want %s", m.Sha256, digest)
	}
}

// TestSwap_HelperSpawnFailsFallsBack covers the runtime-spawn-error
// branch: kenaz-updater.exe IS bundled but exec.Cmd.Start fails (AV
// quarantined the file, disk error, etc.). Same fallback as the
// missing-helper case — write the marker, let the bootswap shim
// recover on next launch.
func TestSwap_HelperSpawnFailsFallsBack(t *testing.T) {
	dir := t.TempDir()
	dataDir := filepath.Join(dir, "data")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatal(err)
	}

	binDir := filepath.Join(dir, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	runningBin := filepath.Join(binDir, "harness.exe")
	if err := os.WriteFile(runningBin, []byte("running"), 0o644); err != nil {
		t.Fatal(err)
	}
	helperPath := filepath.Join(binDir, helperBinaryName)
	if err := os.WriteFile(helperPath, []byte("helper"), 0o644); err != nil {
		t.Fatal(err)
	}

	stagedPath, digest := stageBlob(t, dir, "staged.exe", "new contents")
	staged := StagedUpdate{
		Path:          stagedPath,
		TargetVersion: "v0.4.1",
		Sha256:        digest,
		Platform:      "windows/amd64",
	}

	spawner := &fakeHelperSpawner{spawnErr: errors.New("AV quarantined helper")}
	defer withSpawner(spawner)()

	if err := (realSwapper{}).Swap(context.Background(), staged, runningBin, dataDir); err != nil {
		t.Fatalf("Swap should swallow spawn error and fall back: %v", err)
	}
	if len(spawner.calls) != 1 {
		t.Fatalf("expected 1 attempted spawn, got %d", len(spawner.calls))
	}
	if _, err := os.Stat(filepath.Join(dataDir, "update", "pending.json")); err != nil {
		t.Errorf("pending.json should exist on spawn-fail fallback: %v", err)
	}
}

// TestSwap_BadShaMismatchPropagates asserts the pre-spawn re-verify
// is real: a corrupted staged file (digest doesn't match the manifest
// claim) does NOT silently fall through to the fallback — it surfaces
// the error so ApplyAndRestart can emit the audit-failure event.
func TestSwap_BadShaMismatchPropagates(t *testing.T) {
	dir := t.TempDir()
	dataDir := filepath.Join(dir, "data")
	binDir := filepath.Join(dir, "bin")
	_ = os.MkdirAll(dataDir, 0o755)
	_ = os.MkdirAll(binDir, 0o755)
	runningBin := filepath.Join(binDir, "harness.exe")
	_ = os.WriteFile(runningBin, []byte("running"), 0o644)

	stagedPath, _ := stageBlob(t, dir, "staged.exe", "real bytes")
	staged := StagedUpdate{
		Path:          stagedPath,
		TargetVersion: "v0.4.1",
		Sha256:        strings.Repeat("0", 64), // wrong digest
		Platform:      "windows/amd64",
	}

	spawner := &fakeHelperSpawner{}
	defer withSpawner(spawner)()

	err := (realSwapper{}).Swap(context.Background(), staged, runningBin, dataDir)
	if err == nil {
		t.Fatalf("expected sha-mismatch error")
	}
	if !strings.Contains(err.Error(), "sha256") {
		t.Errorf("err = %q; want sha256 mismatch", err.Error())
	}
	if len(spawner.calls) != 0 {
		t.Errorf("spawner must not run on sha mismatch: %d calls", len(spawner.calls))
	}
}

// TestBuildHelperArgs_ForwardsLaunchArgs confirms os.Args[1:] is
// threaded into the helper as repeated --launch-args occurrences.
// Without this, the relaunched binary would lose its CLI state
// (e.g. --debug, --headless) across the auto-update.
func TestBuildHelperArgs_ForwardsLaunchArgs(t *testing.T) {
	prev := os.Args
	defer func() { os.Args = prev }()
	os.Args = []string{"harness.exe", "--debug", "--headless"}

	staged := StagedUpdate{
		Path:          "C:\\staged\\new.exe",
		TargetVersion: "v0.4.1",
		Sha256:        strings.Repeat("a", 64),
		Platform:      "windows/amd64",
	}
	args := buildHelperArgs(staged, "C:\\Program Files\\Kenaz\\harness.exe", "C:\\Users\\x\\AppData\\Local\\Kenaz")
	count := 0
	for i, a := range args {
		if a == "--launch-args" && i+1 < len(args) {
			count++
		}
	}
	if count != 2 {
		t.Errorf("expected 2 --launch-args repetitions, got %d (args=%v)", count, args)
	}
}
