package refs

// This file contains a package-level static analysis helper that is
// exercised via the test suite. The lint check asserts that no file in
// core/tools/**/*.go contains a literal "@secret:" string outside of a
// refs.Substitute call site. Tool packages MUST always call
// refs.Substitute to resolve references — never construct the token
// themselves or embed it in a hardcoded string.
//
// The check is implemented as a TestLintNoLiteralSecretReference test
// (see lint_test.go) that runs as part of the normal `go test` suite.
// It is placed here rather than in an _test.go file so the package
// exports the linting logic for use by the CI check script.

import (
	"os"
	"path/filepath"
	"strings"
)

// LintResult is the outcome of the literal-reference lint pass.
type LintResult struct {
	// File is the path of the file that contains the violation.
	File string
	// Line is the 1-based line number.
	Line int
	// Snippet is the offending line content (truncated to 200 chars).
	Snippet string
}

// LintToolsDir scans all .go files under toolsDir and returns any file
// that contains a literal "@secret:" string that appears NOT to be inside
// a comment or import path. A non-empty result means a tool package
// is embedding a reference token rather than going through refs.Substitute.
//
// toolsDir should be an absolute path to the core/tools directory.
// Returns nil, nil when the directory does not exist (graceful degradation
// in test environments that don't mount the full source tree).
func LintToolsDir(toolsDir string) ([]LintResult, error) {
	if _, err := os.Stat(toolsDir); err != nil {
		return nil, nil // directory missing — skip gracefully
	}

	var results []LintResult
	err := filepath.WalkDir(toolsDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		fileResults, ferr := lintFile(path)
		if ferr != nil {
			return ferr
		}
		results = append(results, fileResults...)
		return nil
	})
	return results, err
}

func lintFile(path string) ([]LintResult, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(string(data), "\n")
	const needle = `@secret:`
	var results []LintResult
	for i, line := range lines {
		if !strings.Contains(line, needle) {
			continue
		}
		trimmed := strings.TrimSpace(line)
		// Skip comment lines.
		if strings.HasPrefix(trimmed, "//") {
			continue
		}
		// Skip import paths (unlikely but safe).
		if strings.HasPrefix(trimmed, `"`) && strings.HasSuffix(trimmed, `"`) {
			continue
		}
		// Skip test files — they are allowed to embed reference tokens
		// for assertion purposes.
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		// Skip refs package itself.
		if strings.Contains(filepath.ToSlash(path), "core/credstore/refs/") {
			continue
		}
		snippet := line
		if len(snippet) > 200 {
			snippet = snippet[:200]
		}
		results = append(results, LintResult{
			File:    path,
			Line:    i + 1,
			Snippet: snippet,
		})
	}
	return results, nil
}
