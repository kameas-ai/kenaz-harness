package paths

import (
	"os"
	"path/filepath"
	"testing"
)

// writeFile is a tiny helper that creates parent dirs and writes a file.
func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func TestMigrateLegacy_MovesWhenSafe(t *testing.T) {
	root := t.TempDir()
	legacy := filepath.Join(root, ".harness")
	target := filepath.Join(root, ".kenaz", "harness", "prod")
	writeFile(t, filepath.Join(legacy, "data.db"), "DATA")
	writeFile(t, filepath.Join(legacy, "contexts", "x"), "ctx")

	res, err := MigrateLegacy(legacy, target)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.Migrated {
		t.Fatalf("expected Migrated=true, got skipped=%q", res.Skipped)
	}
	if !exists(filepath.Join(target, "data.db")) {
		t.Errorf("target data.db missing after migrate")
	}
	if !exists(filepath.Join(target, "contexts", "x")) {
		t.Errorf("target did not get nested contents")
	}
	if exists(legacy) {
		t.Errorf("legacy should be gone after atomic rename")
	}
}

func TestMigrateLegacy_IdempotentWhenTargetPopulated(t *testing.T) {
	root := t.TempDir()
	legacy := filepath.Join(root, ".harness")
	target := filepath.Join(root, ".kenaz", "harness", "prod")
	writeFile(t, filepath.Join(legacy, "data.db"), "LEGACY")
	writeFile(t, filepath.Join(target, "data.db"), "EXISTING")

	res, err := MigrateLegacy(legacy, target)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Migrated {
		t.Fatalf("must not migrate over a populated target")
	}
	if b, _ := os.ReadFile(filepath.Join(target, "data.db")); string(b) != "EXISTING" {
		t.Errorf("target data.db was overwritten: %q", string(b))
	}
	if !exists(legacy) {
		t.Errorf("legacy must be left intact when target wins")
	}
}

func TestMigrateLegacy_NoLegacy(t *testing.T) {
	root := t.TempDir()
	res, err := MigrateLegacy(filepath.Join(root, ".harness"), filepath.Join(root, "t"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Migrated || res.Skipped != "no legacy data dir" {
		t.Fatalf("got migrated=%v skipped=%q", res.Migrated, res.Skipped)
	}
}

func TestMigrateLegacy_LegacyHasNoDataDB(t *testing.T) {
	root := t.TempDir()
	legacy := filepath.Join(root, ".harness")
	writeFile(t, filepath.Join(legacy, "random"), "x")
	res, err := MigrateLegacy(legacy, filepath.Join(root, "t"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Migrated || res.Skipped != "legacy has no data.db" {
		t.Fatalf("got migrated=%v skipped=%q", res.Migrated, res.Skipped)
	}
}

func TestMigrateLegacy_SkipsWhenLocked(t *testing.T) {
	root := t.TempDir()
	legacy := filepath.Join(root, ".harness")
	target := filepath.Join(root, ".kenaz", "harness", "prod")
	writeFile(t, filepath.Join(legacy, "data.db"), "DATA")
	writeFile(t, filepath.Join(legacy, "data.db.harness-lock"), `{"pid":4242,"hostname":"box","started_at":"now"}`)

	// Force the liveness probe to report the holder as alive.
	orig := processAliveSameHost
	processAliveSameHost = func(int, string) bool { return true }
	defer func() { processAliveSameHost = orig }()

	res, err := MigrateLegacy(legacy, target)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Migrated {
		t.Fatalf("must not migrate a locked legacy dir")
	}
	if !exists(filepath.Join(legacy, "data.db")) {
		t.Errorf("legacy data must be untouched when locked")
	}
	if exists(filepath.Join(target, "data.db")) {
		t.Errorf("target must not be created when skipped")
	}
}

func TestMigrateLegacy_MigratesWhenHolderDead(t *testing.T) {
	root := t.TempDir()
	legacy := filepath.Join(root, ".harness")
	target := filepath.Join(root, ".kenaz", "harness", "prod")
	writeFile(t, filepath.Join(legacy, "data.db"), "DATA")
	writeFile(t, filepath.Join(legacy, "data.db.harness-lock"), `{"pid":4242,"hostname":"box","started_at":"old"}`)

	orig := processAliveSameHost
	processAliveSameHost = func(int, string) bool { return false } // stale lock
	defer func() { processAliveSameHost = orig }()

	res, err := MigrateLegacy(legacy, target)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.Migrated {
		t.Fatalf("should migrate past a stale lock; skipped=%q", res.Skipped)
	}
	if !exists(filepath.Join(target, "data.db")) {
		t.Errorf("target data.db missing after migrate")
	}
}
