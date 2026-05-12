package refs_test

import (
	"path/filepath"
	"runtime"
	"testing"

	"github.com/sigil-tech/kaneaz-harness/core/credstore/refs"
)

// TestLintNoLiteralSecretReference asserts that no non-test Go file in
// core/tools/** contains a bare "@secret:" string outside of a comment.
// Tool packages must always resolve references via refs.Substitute, never
// by constructing or hardcoding the reference token.
func TestLintNoLiteralSecretReference(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping lint check in short mode")
	}
	// Locate the root relative to this file's package.
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller(0) failed")
	}
	// Walk up from core/credstore/refs to repo root (3 levels).
	repoRoot := filepath.Dir(filepath.Dir(filepath.Dir(thisFile)))
	toolsDir := filepath.Join(repoRoot, "core", "tools")

	results, err := refs.LintToolsDir(toolsDir)
	if err != nil {
		t.Fatalf("LintToolsDir: %v", err)
	}
	for _, r := range results {
		t.Errorf("literal @secret: in tool source at %s:%d: %q", r.File, r.Line, r.Snippet)
	}
}
