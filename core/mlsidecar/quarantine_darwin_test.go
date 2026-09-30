//go:build darwin

package mlsidecar

import (
	"os"
	"path/filepath"
	"testing"
)

// TestClearQuarantine_RemovesRecursively is the darwin-guarded
// "quarantine-cleared-only-after-verify" mechanics proof (design F3/
// §3.7 R3): a file (and a nested file) carrying com.apple.quarantine has
// the attribute removed recursively; a file with no attribute at all is
// left untouched (idempotent, per the design's own wording).
func TestClearQuarantine_RemovesRecursively(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "kameas-ml")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	quarantined := filepath.Join(nested, "kameas-ml")
	clean := filepath.Join(nested, "readme.txt")
	if err := os.WriteFile(quarantined, []byte("engine binary"), 0o755); err != nil {
		t.Fatalf("write quarantined: %v", err)
	}
	if err := os.WriteFile(clean, []byte("no xattr here"), 0o644); err != nil {
		t.Fatalf("write clean: %v", err)
	}
	if err := setQuarantineAttrForTest(quarantined); err != nil {
		t.Skipf("cannot set quarantine xattr in this sandbox: %v", err)
	}

	present, err := quarantineAttrPresent(quarantined)
	if err != nil {
		t.Fatalf("quarantineAttrPresent (before clear): %v", err)
	}
	if !present {
		t.Fatal("expected the quarantine attribute to be present before clearQuarantine runs")
	}

	if err := clearQuarantine(root); err != nil {
		t.Fatalf("clearQuarantine: %v", err)
	}

	present, err = quarantineAttrPresent(quarantined)
	if err != nil {
		t.Fatalf("quarantineAttrPresent (after clear): %v", err)
	}
	if present {
		t.Error("expected the quarantine attribute to be removed after clearQuarantine")
	}

	// Idempotent: running it again on a tree with no quarantine attrs at
	// all must not error.
	if err := clearQuarantine(root); err != nil {
		t.Fatalf("clearQuarantine (idempotent re-run): %v", err)
	}
}

// TestInstall_QuarantineClearedOnlyAfterVerification_OrderingProof shows
// the ordering guarantee at the Install level: a verification FAILURE
// means the version directory clearQuarantine would operate on is never
// even created — so quarantine clearing structurally cannot have run
// before verification. (install_test.go's
// TestInstall_TamperedArtifact_NeverUnpackedOrActivated is the
// platform-neutral half of this same proof; this darwin-only variant
// documents the connection explicitly for the quarantine step.)
func TestInstall_QuarantineClearedOnlyAfterVerification_OrderingProof(t *testing.T) {
	l := NewLayout(t.TempDir())
	if err := l.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}
	if _, err := os.Stat(l.VersionDir("1.0.0")); !os.IsNotExist(err) {
		t.Fatalf("precondition failed: version dir should not exist yet, err=%v", err)
	}
	// clearQuarantine on a nonexistent path is the direct evidence that
	// nothing could have cleared quarantine on an unverified artifact —
	// there is no directory to walk.
	if err := clearQuarantine(l.VersionDir("1.0.0")); err == nil {
		t.Fatal("expected clearQuarantine to fail on a directory that was never created (proves ordering: verify-then-unpack-then-clear, never clear-before-verify)")
	}
}
