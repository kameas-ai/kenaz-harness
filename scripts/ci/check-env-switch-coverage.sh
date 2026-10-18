#!/usr/bin/env bash
# check-env-switch-coverage.sh — G-6 (model-settings-reach-the-model-
# 01PMZ101 WP12/WP14, spec §7 G-6): documented env-var values a `switch`
# silently absorbs into its `default:` arm.
#
# THE DEFECT CLASS
# -----------------
# core/llm/capabilities/cache.go's DefaultCache documented
# HARNESS_LLM_CAPABILITY_CACHE as accepting "sqlite" | "memory" | "off"
# while its switch had no `case "sqlite":` arm at all — the documented
# value silently fell to `default:` (MemoryCache), leaving the
# capability probe cache's SQLite backend (migration sessions/0329,
# shipped in v0.63.0) permanently unreachable in every install that
# believed the doc comment. Fixed as part of this same WP; this gate
# exists so the class cannot recur silently, here or elsewhere.
#
# SCOPE — deliberately narrow, said so rather than pretending otherwise
# ------------------------------------------------------------------------
# This is a same-FILE, textual checker: it finds `const Env<Name> =
# "..."` declarations whose doc comment contains a `Values: "a" | "b" |
# "c"` list, and checks that a switch on os.Getenv(<Name>) IN THE SAME
# FILE has a `case` for every documented value. It does not follow the
# constant across files. See scripts/ci/cmd/checkenvswitch/main.go's
# header for the full design and its stated limitation.
#
# Violations must appear in
# scripts/ci/allowlists/i21-documented-env-switch-gaps.txt with a DATED
# justification naming the blocker and owner.
#
# Exit codes:
#   0 — every documented value has a matching case, modulo the
#       allowlist; the allowlist has no stale entries.
#   1 — the checker itself failed to build, or found zero Go files /
#       zero documented env constants — a tool defect, not a finding.
#   2 — at least one real violation, or a stale allowlist entry.
#
# Usage: bash scripts/ci/check-env-switch-coverage.sh (from anywhere).

set -euo pipefail

WORKTREE_ROOT="$(git rev-parse --show-toplevel 2>/dev/null || pwd)"
cd "${WORKTREE_ROOT}"

BIN="$(mktemp -d)/checkenvswitch"
trap 'rm -rf "$(dirname "${BIN}")"' EXIT

echo "[env-switch-coverage] building checker..."
if ! go build -o "${BIN}" ./scripts/ci/cmd/checkenvswitch/; then
  echo "[env-switch-coverage] FAIL: checkenvswitch failed to build." >&2
  exit 1
fi

echo "[env-switch-coverage] scanning core/+cmd/ for documented env values a switch might swallow..."
"${BIN}"
