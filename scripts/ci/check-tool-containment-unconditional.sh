#!/usr/bin/env bash
# check-tool-containment-unconditional.sh — AC-015 gate
# (harness-self-attach-01PMHS01 UNIT-4): the merged permission resolver
# — static arm + the Cedar-backed session-kind arm, THE containment for
# model-initiated unattended tool execution (owner rulings B-2/B-3) —
# must be constructed UNCONDITIONALLY in core/rpc/api.go's newLLMStack.
#
# THE DEFECT CLASS
# -----------------
# Before UNIT-4, `perms` was built only inside
# `if c != nil && c.DataDir() != "" { ... }`. An empty DataDir, or a
# NewStaticResolverFromDataDir error, left `perms` nil — and nil is not
# a restrictive resolver, it is NO resolver: discoverer_adapter.go's
# `if d.perms != nil` guard lists everything, and chatPermsAdapter
# returns "auto_allow" when its inner resolver is nil. Every existing
# and proposed LISTING test passed in that state (tasks.md UNIT-4).
#
# A naive `grep -q 'toolloop.NewMergedResolver('` check would pass even
# if a future edit moved the call BACK inside a DataDir-guarded
# conditional — the call site still exists textually, just reachable on
# fewer boot paths. This is "the conditional-assignment shape" tasks.md
# names explicitly: a gate that only checks the call's EXISTENCE cannot
# see it.
#
# WHAT THIS CHECKS
# ----------------
# The `toolloop.NewMergedResolver(` call in core/rpc/api.go must be a
# TOP-LEVEL statement inside its enclosing function — indented with
# EXACTLY ONE TAB, gofmt's marker for "not nested inside any if/for/
# closure block". Re-wrapping the call in a conditional adds at least
# one more tab of indentation, which this check catches directly. This
# is a narrow, deliberately mechanical proxy for "unconditional" — it
# does not parse Go, it trusts gofmt's indentation convention (this repo
# gofmt's on save; check-codegen.sh / CI enforce it elsewhere). Verified
# against a planted conditional-wrapper mutation in
# scripts/ci/gates_can_fail_test.go.
#
# Usage: bash scripts/ci/check-tool-containment-unconditional.sh

set -euo pipefail

source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib/ci-gate.sh"

GATE="[tool-containment-unconditional]"
API_FILE="core/rpc/api.go"

ci_require_file "$API_FILE" "$GATE"

# Any occurrence at all, any indentation — existence check, the "naive
# grep" this gate deliberately does more than.
any=$(grep -n 'toolloop\.NewMergedResolver(' "$API_FILE" || true)
if [ -z "$any" ]; then
  echo "${GATE} FAIL: toolloop.NewMergedResolver( does not appear in ${API_FILE} at all." >&2
  echo "${GATE} harness-self-attach-01PMHS01 UNIT-4 wires the merged permission" >&2
  echo "${GATE} resolver (static arm + Cedar session-kind arm) here. If the call" >&2
  echo "${GATE} moved, update this gate in the same commit; if the wire was" >&2
  echo "${GATE} reverted, that is exactly the regression AC-015 exists to catch." >&2
  exit 1
fi

# The load-bearing check: a one-tab-indented assignment. Two spaces of
# leading tab literal below (bash $'...' ANSI-C quoting for the TAB).
onetab=$(grep -nE $'^\t[A-Za-z_][A-Za-z0-9_]* := toolloop\\.NewMergedResolver\\(' "$API_FILE" || true)

if [ -z "$onetab" ]; then
  echo "${GATE} FAIL: toolloop.NewMergedResolver( exists in ${API_FILE} but is not a" >&2
  echo "${GATE} top-level (one-tab-indented) statement — it appears to be nested" >&2
  echo "${GATE} inside a conditional, loop, or closure. This is the exact" >&2
  echo "${GATE} 'conditional-assignment shape' regression AC-015" >&2
  echo "${GATE} (harness-self-attach-01PMHS01 UNIT-4) exists to prevent: a merged" >&2
  echo "${GATE} resolver built only on SOME boot paths degrades to nil (== no" >&2
  echo "${GATE} resolver == unrestricted) on the others." >&2
  echo "${GATE} occurrence(s) found:" >&2
  echo "$any" >&2
  exit 1
fi

echo "${GATE} clean — toolloop.NewMergedResolver( is a top-level (unconditional) statement:"
echo "$onetab"
