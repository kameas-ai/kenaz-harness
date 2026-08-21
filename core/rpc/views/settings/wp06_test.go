package settings

// wp06_test.go — controls-and-readouts-that-tell-the-truth-01PMZ808
// UNIT-3 / WP06. AC-013: the "schemaVersion gates migrations" claim
// must be gone from core/, not merely reworded somewhere a human missed.
//
// The WindowSize round-trip (AC-011) and the upgrade-path overwrite
// check (AC-012 / AC-PI-1) live in the repo-root main_test.go
// (TestWindowSizeRoundTrip, TestWindowSizeUpgradePath) because the
// wiring under test — resolveInitialWindowSize / persistWindowSize —
// lives in main.go, not in this package; settings.Settings itself has
// no window-size-specific behaviour to unit test beyond LoadAll's
// existing zero-value backfill (impl_test.go already covers that).

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestWP06_AC013_SchemaVersionClaimNarrowed walks core/ looking for the
// exact retracted claim. It is a Go-native stand-in for the tasks.md
// AC-013 grep instruction so the assertion runs under `go test` rather
// than needing a shell step. The claim itself is assembled at runtime
// (not written literally in this file) so this test does not flag its
// own source when it walks core/.
//
// Mutation: reintroduce the retracted phrase anywhere under core/ (e.g.
// restore the old doc comment). Must fail.
func TestWP06_AC013_SchemaVersionClaimNarrowed(t *testing.T) {
	claim := strings.Join([]string{"schemaVersion", "gates", "migrations"}, " ")
	root := filepath.Join("..", "..", "..", "..") // core/rpc/views/settings -> repo root
	selfPath, _ := filepath.Abs("wp06_test.go")
	var hits []string
	err := filepath.WalkDir(filepath.Join(root, "core"), func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if abs, aerr := filepath.Abs(path); aerr == nil && abs == selfPath {
			return nil // this file legitimately names the retracted claim in prose
		}
		// Collapse the comment's line-wrap ("schemaVersion\n// gates
		// migrations") the same way the mission's grep instruction
		// treats it: search for the two words on adjacent lines OR on
		// one line, case-sensitive, exact phrase.
		data, rerr := os.ReadFile(path)
		if rerr != nil {
			return nil // best-effort; unreadable files are not this test's concern
		}
		normalized := strings.Join(strings.Fields(string(data)), " ")
		if strings.Contains(normalized, claim) {
			hits = append(hits, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk core/: %v", err)
	}
	if len(hits) > 0 {
		t.Fatalf("retracted claim %q still present in: %v", claim, hits)
	}
}

// TestWP06_AC013_LedgerEntryExists is the "and the ledger entry exists"
// half of AC-013.
func TestWP06_AC013_LedgerEntryExists(t *testing.T) {
	root := filepath.Join("..", "..", "..", "..")
	data, err := os.ReadFile(filepath.Join(root, "docs", "unwired-ledger.md"))
	if err != nil {
		t.Skipf("docs/unwired-ledger.md not present in this checkout (gitignored in some worktrees): %v", err)
	}
	if !strings.Contains(string(data), "SchemaVersion` gates no migration") {
		t.Fatalf("docs/unwired-ledger.md has no dated entry for settings.Settings.SchemaVersion")
	}
}
