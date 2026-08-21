package logging

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestInitLogger_UnderGoTest_DoesNotWriteToHome is the regression for the
// finding that every `go test` opening a real sqlite database appended to
// the developer's live ~/.kenaz/harness.log. The file had reached 669 MB.
//
// The assertion is on the RESOLVED path rather than on the absence of
// bytes in the real home file, because a test that writes to the real
// file to prove it must not is self-defeating.
func TestInitLogger_UnderGoTest_DoesNotWriteToHome(t *testing.T) {
	// L() resolves the path through initLogger's once.Do, so read the
	// live handle rather than re-deriving.
	_ = L()
	if logFile == nil {
		t.Skip("logger fell back to stderr; no path to assert on")
	}
	got := logFile.Name()

	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no home dir on this machine: %v", err)
	}
	forbidden := filepath.Join(home, defaultDir, defaultFile)
	if got == forbidden {
		t.Fatalf("logger opened the developer's real log under `go test`: %s", got)
	}
	if strings.HasPrefix(got, filepath.Join(home, defaultDir)+string(os.PathSeparator)) {
		t.Fatalf("logger opened a path inside the real ~/%s under `go test`: %s", defaultDir, got)
	}
}

// TestRotateIfLarge covers the size cap that did not exist until
// 2026-08-21: over the cap rotates to .1 and discards the previous
// backup; under the cap leaves the file alone.
func TestRotateIfLarge(t *testing.T) {
	t.Run("over the cap rotates and replaces the old backup", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "harness.log")
		if err := os.WriteFile(path, make([]byte, maxLogBytes+1), 0o600); err != nil {
			t.Fatalf("seed: %v", err)
		}
		if err := os.WriteFile(path+".1", []byte("stale backup"), 0o600); err != nil {
			t.Fatalf("seed backup: %v", err)
		}

		rotateIfLarge(path)

		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("live log still present after rotation (err=%v)", err)
		}
		bk, err := os.Stat(path + ".1")
		if err != nil {
			t.Fatalf("backup missing after rotation: %v", err)
		}
		if bk.Size() != int64(maxLogBytes)+1 {
			t.Fatalf("backup size = %d, want the rotated live log (%d) — the stale backup was not replaced", bk.Size(), maxLogBytes+1)
		}
	})

	t.Run("under the cap leaves the file alone", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "harness.log")
		if err := os.WriteFile(path, []byte("small"), 0o600); err != nil {
			t.Fatalf("seed: %v", err)
		}

		rotateIfLarge(path)

		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("live log gone: %v", err)
		}
		if string(got) != "small" {
			t.Fatalf("live log content = %q, want it untouched", got)
		}
		if _, err := os.Stat(path + ".1"); !os.IsNotExist(err) {
			t.Fatal("rotated a file that was under the cap")
		}
	})

	t.Run("missing file is a no-op", func(t *testing.T) {
		rotateIfLarge(filepath.Join(t.TempDir(), "absent.log"))
	})
}
