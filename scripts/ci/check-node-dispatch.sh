#!/usr/bin/env bash
# check-node-dispatch.sh — manifest dispatch: <-> newExecutorRegistry()
# cross-check, both directions (wiring-integrity-01PMAG04 WP04, spec
# §3.2 item 1).
#
# The self-hosted CI image (kameas-ci-medium / ci-fast, see CLAUDE.md
# "Self-hosted runner caveats") has no apt-get and no extra tooling
# beyond the Go toolchain, so this is pure shell + `go test` — no
# separate `go run` helper binary to build/ship. The actual checks
# already live as two ordinary Go tests (kept there, not duplicated
# here, so there is exactly one place the dispatch-registry logic can
# drift):
#
#   1. core/agentgraph/nodes.TestBundledCatalog_DispatchDeclarations
#      — every shipped manifest's dispatch: value is well-formed
#      (graph|builtin_tool, tool_name paired correctly). Manifest-only;
#      cannot see the Go executor registry (DIRECTIVE_001: the nodes
#      package imports nothing from agentgraph).
#   2. core/agentgraph.TestDispatchRegistry_MatchesManifests — the
#      registry-side half: every dispatch: graph manifest has a
#      registered Executor, and every registered Executor's kind declares
#      dispatch: graph (not builtin_tool, not undeclared).
#
#      NOTE (01PMGX01 WP17, 2026-08-13): this used to add "and
#      sleep/subagent_dispatch are pinned as the two shipped
#      builtin_tool cases". They are not, any more —
#      agentgraph-total-convergence-01PMGX01 Phase 2 gave both real
#      archetype-derived executors and flipped them to dispatch: graph,
#      and the test was updated with them. `builtin_tool` now has ZERO
#      shipped instances; see DispatchMode's doc comment in
#      core/agentgraph/nodes/manifest.go for why the mode is kept
#      regardless, and what deleting it would have to include.
#
# This script exists so CI has one named step covering both directions,
# and so a developer can run the identical check locally without
# remembering two `-run` patterns across two packages.
#
# Exit codes:
#   0 — both directions clean
#   1 — go test itself failed to run (build error, etc.)
#   2 — a dispatch mismatch was found (see go test output above)
#
set -euo pipefail

WORKTREE_ROOT="$(git rev-parse --show-toplevel 2>/dev/null || pwd)"
cd "$WORKTREE_ROOT"

echo "[node-dispatch] manifest-side: dispatch: declarations well-formed..."
if ! go test ./core/agentgraph/nodes/... -run '^TestBundledCatalog_DispatchDeclarations$' -count=1 -v 2>&1; then
  echo "" >&2
  echo "[node-dispatch] FAIL: a shipped manifest's dispatch: declaration is missing or malformed." >&2
  echo "  Every kind manifest under core/agentgraph/nodes/manifests/ (except the _archetype.* files) must declare" >&2
  echo "  dispatch: graph  (a registered Executor exists), or" >&2
  echo "  dispatch: builtin_tool  + tool_name: <name>  (no Executor; reached via a builtin tool call)." >&2
  exit 2
fi

echo ""
echo "[node-dispatch] registry-side: dispatch: graph <-> newExecutorRegistry(), both directions..."
if ! go test ./core/agentgraph/... -run '^TestDispatchRegistry_MatchesManifests$' -count=1 -v 2>&1; then
  echo "" >&2
  echo "[node-dispatch] FAIL: manifest dispatch: declarations and newExecutorRegistry() disagree." >&2
  echo "  Either a dispatch: graph kind has no registered Executor (add one, or the manifest is lying), or" >&2
  echo "  a registered Executor's kind isn't declared dispatch: graph (fix the manifest, or the registration is stray)." >&2
  echo "  See docs/wiring-audit.md's WP01 section for the full contract." >&2
  exit 2
fi

echo ""
echo "[node-dispatch] clean — manifest dispatch: declarations and the Go executor registry agree in both directions."
exit 0
