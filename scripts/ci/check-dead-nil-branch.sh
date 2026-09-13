#!/usr/bin/env bash
# check-dead-nil-branch.sh — the statically-dead-nil-branch gate.
#
# THE DEFECT CLASS
# -----------------
# `var x T` (a zero-value declaration, no initializer) immediately
# followed, in the SAME lexical block, by `if x != nil { ... }` with no
# assignment to x anywhere in between — so x is provably nil at the
# check and the if-block can never execute. Shipped in production as
# core/rpc/builtins_wiring.go's
#
#   var subagentSeam agentgraph.BranchSeam // nil — no child-run spawner yet
#   if subagentSeam != nil { registerSubagentDispatchTool(...) }
#
# which hid the ENTIRE kenaz__subagent_dispatch registration block from
# every gate that already existed: check-builtin-tool-registration.sh
# only checks for an *import*, which was present (needed to construct
# the tool inside the dead branch), so the unreachable tool never
# landed on an allowlist. UNIT-6 of subagent-control-and-background-
# tasks-01PMZB11 fixed the one reported instance; this gate exists so
# the CLASS — not just that instance — cannot recur silently.
#
# See scripts/ci/cmd/checkdeadnilbranch/main.go's header for the exact
# check (a Go/AST + go/types tool, not a grep — "is this the same
# object, is there an assignment anywhere on the intervening subtree"
# are type-checker questions, same reasoning as check-seam-implementers.sh
# and check-nil-optional-deps.sh being Go tools rather than shell greps).
#
# Violations must appear in scripts/ci/allowlists/i19-dead-nil-branch.txt
# with a DATED justification naming the blocker and owner — same
# contract as every other gate in this directory. Allowlists shrink
# monotonically.
#
# Exit codes:
#   0 — every candidate has an intervening assignment or is allowlisted
#   1 — the checker itself failed to build, load packages, or read the
#       allowlist
#   2 — at least one statically-dead nil-check is neither disproven nor
#       allowlisted, or the allowlist is stale
#
# Usage: bash scripts/ci/check-dead-nil-branch.sh (from anywhere).

set -euo pipefail

# Resolve the repo root from THIS FILE's own on-disk location
# (BASH_SOURCE[0]), not `git rev-parse --show-toplevel || pwd` — that
# fallback silently uses the CALLER's cwd (not the repo root) when
# invoked from outside any git repository, which is precisely the
# vacuous-gate shape CLAUDE.md's check-knob-coverage.sh AC-063 note
# documents (proven live from a mktemp'd /tmp directory: exit 3 there,
# exit 0 from the repo root, before that fix). Confirmed the same way
# here during development: this gate silently reported a `go.mod not
# found` build failure from /tmp before switching to lib/ci-gate.sh.
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib/ci-gate.sh"

BIN="$(mktemp -d)/checkdeadnilbranch"
trap 'rm -rf "$(dirname "$BIN")"' EXIT

echo "[dead-nil-branch] building checker..."
if ! go build -o "$BIN" ./scripts/ci/cmd/checkdeadnilbranch/; then
  echo "[dead-nil-branch] FAIL: checkdeadnilbranch failed to build." >&2
  exit 1
fi

echo "[dead-nil-branch] scanning core/+cmd/ for a zero-value var declaration checked != nil with no intervening assignment..."
"$BIN"
