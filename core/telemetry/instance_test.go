package telemetry

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEnsureInstanceID_RoundTrip(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	first, err := EnsureInstanceID(dir)
	if err != nil {
		t.Fatalf("EnsureInstanceID #1: %v", err)
	}
	if !ulidPattern.MatchString(first) {
		t.Errorf("first ULID %q does not match pattern", first)
	}
	second, err := EnsureInstanceID(dir)
	if err != nil {
		t.Fatalf("EnsureInstanceID #2: %v", err)
	}
	if first != second {
		t.Errorf("ULID changed across calls: %q -> %q", first, second)
	}

	// File lands at <dir>/telemetry/instance.id with the tight perm.
	path := filepath.Join(dir, instanceDirName, instanceFileName)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.IsDir() {
		t.Errorf("expected file, got dir")
	}
}

func TestEnsureInstanceID_CreatesParentDir(t *testing.T) {
	t.Parallel()
	// dataDir exists but the telemetry subdir does not — first call
	// must create it.
	dir := t.TempDir()
	telDir := filepath.Join(dir, instanceDirName)
	if _, err := os.Stat(telDir); !os.IsNotExist(err) {
		t.Fatalf("precondition: %s should not exist", telDir)
	}
	if _, err := EnsureInstanceID(dir); err != nil {
		t.Fatalf("EnsureInstanceID: %v", err)
	}
	if info, err := os.Stat(telDir); err != nil || !info.IsDir() {
		t.Errorf("expected %s to be a dir, got err=%v info=%v", telDir, err, info)
	}
}

func TestEnsureInstanceID_RegeneratesCorruptFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	telDir := filepath.Join(dir, instanceDirName)
	if err := os.MkdirAll(telDir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	corruptPath := filepath.Join(telDir, instanceFileName)
	if err := os.WriteFile(corruptPath, []byte("not-a-ulid"), 0o600); err != nil {
		t.Fatalf("write corrupt: %v", err)
	}

	got, err := EnsureInstanceID(dir)
	if err != nil {
		t.Fatalf("EnsureInstanceID: %v", err)
	}
	if !ulidPattern.MatchString(got) {
		t.Errorf("regenerated ULID %q does not match pattern", got)
	}
	if got == "not-a-ulid" {
		t.Errorf("did not regenerate corrupt content")
	}

	// Persisted across calls.
	got2, err := EnsureInstanceID(dir)
	if err != nil {
		t.Fatalf("EnsureInstanceID #2: %v", err)
	}
	if got != got2 {
		t.Errorf("ULID changed after corruption fix: %q -> %q", got, got2)
	}
}

func TestEnsureInstanceID_RejectsEmptyDir(t *testing.T) {
	t.Parallel()
	if _, err := EnsureInstanceID(""); err == nil {
		t.Errorf("expected error on empty dataDir")
	}
}

func TestNewULID_Monotonic(t *testing.T) {
	t.Parallel()
	prev, err := newULID()
	if err != nil {
		t.Fatalf("newULID: %v", err)
	}
	for i := 0; i < 50; i++ {
		next, err := newULID()
		if err != nil {
			t.Fatalf("newULID #%d: %v", i, err)
		}
		if next <= prev {
			t.Errorf("ULID not monotonic: %q -> %q", prev, next)
		}
		prev = next
	}
}
