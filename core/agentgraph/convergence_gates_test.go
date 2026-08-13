package agentgraph

// Go-test half of the WP01 convergence gates
// (kitty-specs/agentgraph-total-convergence-01PMGX01/spec.md §6, WP01:
// `fix(ci): WP01 — convergence invariant gates`). The bash-based gates
// live under scripts/ci/check-agentgraph-convergence.sh (I3/I6/I7),
// scripts/ci/check-no-unwired-gates.sh (I10), and
// scripts/ci/check-no-forbidden-compaction-symbols.sh (I4). This file
// covers I1 and I2, which need access to unexported package internals
// (newExecutorRegistry, the ExecutorRegistry query surface) that a bash
// script cannot reach.
//
// Both tests share the same allow-list contract as the bash gates: a
// plain-text file under scripts/ci/allowlists/ with one entry per line,
// `#` comments and blank lines ignored. Each test FAILS on an unlisted
// violation AND on a stale allow-list entry (a listed kind that no
// longer violates the invariant) — see spec §4.1: allow-lists must
// shrink monotonically, never silently accumulate.

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// loadGateAllowlist reads a plain-text allow-list file (as shared with
// the scripts/ci/*.sh gates: `#` comments and blank lines ignored) and
// returns the entries as a set.
func loadGateAllowlist(t *testing.T, relPath string) map[string]bool {
	t.Helper()
	data, err := os.ReadFile(filepath.FromSlash(relPath))
	if err != nil {
		t.Fatalf("reading allow-list %s: %v", relPath, err)
	}
	out := map[string]bool{}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out[line] = true
	}
	return out
}

// pascalCaseKind converts a manifest kind id (snake_case, e.g.
// "read_bash_output") to the PascalCase form the codebase's
// `executor:` naming convention uses (e.g. "ReadBashOutput").
func pascalCaseKind(id string) string {
	parts := strings.Split(id, "_")
	var b strings.Builder
	for _, p := range parts {
		if p == "" {
			continue
		}
		b.WriteString(strings.ToUpper(p[:1]))
		b.WriteString(p[1:])
	}
	return b.String()
}

// TestConvergenceI1_EveryKindHasRegisteredExecutor pins spec §6 I1: every
// NodeKind in AllNodeKinds() must have a registered executor, except the
// kinds named in scripts/ci/allowlists/i1-missing-executor.txt. Today
// that is exactly `sleep` and `subagent_dispatch` (verified empirically:
// neither appears in newExecutorRegistry() — executor.go:580 — so a graph
// containing either passes validation and fails at run time with
// `agentgraph: no executor for kind "…"`). Both are palette-reachable
// (nodes/catalog_test.go's wantCallable counts them) — this is a
// user-reachable crash, not a hypothetical.
//
// This test FAILS if:
//   - a NEW kind appears with no registered executor and no allow-list
//     entry (regression), or
//   - an allow-listed kind now HAS a registered executor (stale entry —
//     delete the line; spec §6.1 requires every WP that fixes a kind to
//     shrink an allow-list).
func TestConvergenceI1_EveryKindHasRegisteredExecutor(t *testing.T) {
	t.Parallel()

	allow := loadGateAllowlist(t, "../../scripts/ci/allowlists/i1-missing-executor.txt")
	reg := newExecutorRegistry()

	kinds := AllNodeKinds()
	seen := map[string]bool{}
	for _, k := range kinds {
		id := string(k)
		seen[id] = true
		hasExecutor := reg.Has(k)
		isAllowed := allow[id]

		switch {
		case !hasExecutor && !isAllowed:
			t.Errorf("I1 VIOLATION: kind %q has no registered executor in newExecutorRegistry() and is not in scripts/ci/allowlists/i1-missing-executor.txt. Register an executor for it, or add the allow-list entry with justification.", id)
		case hasExecutor && isAllowed:
			t.Errorf("I1 STALE ALLOWLIST: kind %q now has a registered executor but is still listed in scripts/ci/allowlists/i1-missing-executor.txt — delete the line.", id)
		}
	}

	for id := range allow {
		if !seen[id] {
			t.Errorf("I1 STALE ALLOWLIST: entry %q no longer appears in AllNodeKinds() — delete the line from scripts/ci/allowlists/i1-missing-executor.txt.", id)
		}
	}

	// Sanity: once the allow-list is empty this becomes exactly spec §6's
	// stated invariant (`len(AllNodeKinds()) == len(registry.byKind)`).
	if len(allow) == 0 && len(kinds) != reg.Len() {
		t.Errorf("I1: allow-list is empty but len(AllNodeKinds())=%d != len(registry.byKind)=%d — an executor is registered for a kind that no longer exists, or vice versa.", len(kinds), reg.Len())
	}
}

// TestConvergenceI2_ManifestExecutorNamesRegisteredSymbol pins spec §6 I2:
// every manifest's `executor:` field must name the symbol the codebase's
// own naming convention predicts for that kind — `agentgraph.Exec` +
// PascalCase(kind id), e.g. `corpus_read` -> `agentgraph.ExecCorpusRead`
// (verified against all 34 manifests: every kind except sleep and
// subagent_dispatch follows this convention exactly).
//
// IMPORTANT CAVEAT (documented here, not just in the allow-list, because
// it changes what "passing" means): NONE of the `executor:` string values
// in core/agentgraph/nodes/manifests/*.yaml are real compiled Go symbols
// today — `grep -rn 'func ExecModel\|func ExecActivity\|...' core/` finds
// zero hits for any of them. Dispatch is the hand-maintained table in
// core/agentgraph/executor.go:580 (unexported *Executor structs, e.g.
// modelExecutor), NOT the `executor:` field (spec §1.1: "The manifest
// executor: field is documentation only — it is not code-generated into
// dispatch"). A literal AST-based "does this Go symbol exist and is it
// registered" check would therefore fail on all 34 manifests, which is
// not what this WP's brief asks for (it names sleep.yaml's
// agentgraph.ExecBuiltinTool as the one exception, implying the other 33
// are fine). This test instead checks two things that ARE mechanically
// meaningful and, empirically, isolate exactly the two broken manifests:
//   1. the executor: string matches the derived naming convention
//      (self-consistency — sleep/subagent_dispatch both declare the
//      shared placeholder "agentgraph.ExecBuiltinTool" instead of a
//      kind-specific name), AND
//   2. the kind actually has a registered executor (cross-checked
//      against the same registry I1 uses).
//
// FAILS on an unlisted violation or a stale allow-list entry, same
// contract as I1.
func TestConvergenceI2_ManifestExecutorNamesRegisteredSymbol(t *testing.T) {
	t.Parallel()

	allow := loadGateAllowlist(t, "../../scripts/ci/allowlists/i2-manifest-executor-symbol.txt")
	reg := newExecutorRegistry()

	ids := make([]string, 0, len(ResolvedManifests))
	for kind := range ResolvedManifests {
		ids = append(ids, string(kind))
	}
	sort.Strings(ids)

	seen := map[string]bool{}
	for _, id := range ids {
		kind := NodeKind(id)
		rm := ResolvedManifests[kind]
		seen[id] = true

		expected := "agentgraph.Exec" + pascalCaseKind(id)
		hasExecutor := reg.Has(kind)
		conventionMatches := rm.Manifest.Executor == expected
		violation := !conventionMatches || !hasExecutor
		isAllowed := allow[id]

		switch {
		case violation && !isAllowed:
			t.Errorf("I2 VIOLATION: manifest %q declares executor %q (want %q by convention), registered=%v, and is not in scripts/ci/allowlists/i2-manifest-executor-symbol.txt.", id, rm.Manifest.Executor, expected, hasExecutor)
		case !violation && isAllowed:
			t.Errorf("I2 STALE ALLOWLIST: manifest %q now matches the naming convention and has a registered executor but is still listed in scripts/ci/allowlists/i2-manifest-executor-symbol.txt — delete the line.", id)
		}
	}

	for id := range allow {
		if !seen[id] {
			t.Errorf("I2 STALE ALLOWLIST: entry %q no longer appears in ResolvedManifests — delete the line from scripts/ci/allowlists/i2-manifest-executor-symbol.txt.", id)
		}
	}
}
