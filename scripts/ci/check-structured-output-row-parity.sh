#!/usr/bin/env bash
# check-structured-output-row-parity.sh — a capability row cannot
# advertise a structured-output mode its adapter cannot serve
# (structured-output-is-reachable-01PMZE14, UNIT-6/WP09, gate G-3;
# CLAUDE.md "Release ritual: unwired sweep" — "a toggle that reports it
# is on").
#
# The class this closes: §1.8 of the mission spec found ONE of each
# direction already in the tree — gemini's rows were honest only
# because its adapter did nothing (fixed by WP04/WP05), and
# coverage_registry.yaml's bedrock row falsely claimed no coverage for
# behaviour the adapter actually has. Nothing before this gate checked
# that a capabilities/data/*.yaml row and its adapter's REAL wire
# behaviour agree, for every registered kind, driven through
# capabilities.LoadDefault() rather than by reading YAML or a struct
# field directly.
#
# The mechanism is a Go test, not static analysis — see
# core/llm/registry/wp09_g3_capability_row_parity_test.go (the cross-
# adapter cases; TestG3_CapabilityRowMatchesAdapterBehaviour) and its
# bedrock-package companion
# core/llm/bedrock/wp09_g3_row_parity_test.go
# (TestG3_Bedrock_TrueRowMatchesConverseEncoder — bedrock's exported
# StructuredOutputAdapter method is a documented no-op, so its real
# encoder can only be driven white-box). Mirrors
# check-knob-coverage.sh's shape: this script doesn't re-implement the
# check, it runs the test(s) and reports.
#
# Exit codes:
#   0 — every TestG3_* test passed
#   1 — go test itself failed to run (build error, etc.) — see output
#   2 — a TestG3_* test failed (a row disagrees with its adapter)
#   3 — no TestG3_* test exists anywhere in the tree; the gate would
#       otherwise report "clean" while asserting nothing (the vacuous-
#       pass class scripts/ci/gates_can_fail_test.go exists to catch)
#
set -euo pipefail

WORKTREE_ROOT="$(git rev-parse --show-toplevel 2>/dev/null || pwd)"
cd "$WORKTREE_ROOT"

guards=$(grep -rlE '^func TestG3_' --include='*_test.go' core/llm 2>/dev/null || true)
if [[ -z "$guards" ]]; then
  echo "[structured-output-row-parity] FAIL: no TestG3_* test exists under core/llm." >&2
  echo "  This gate would otherwise report 'clean' while asserting nothing about any" >&2
  echo "  capability row. Fix: restore core/llm/registry/wp09_g3_capability_row_parity_test.go" >&2
  echo "  (and its core/llm/bedrock companion)." >&2
  exit 3
fi
echo "[structured-output-row-parity] guard(s) found:"
printf '  %s\n' $guards

echo "[structured-output-row-parity] running every TestG3_* test under ./core/llm/..."
if ! go test ./core/llm/... -run '^TestG3_' -count=1 -v 2>&1; then
  echo "" >&2
  echo "[structured-output-row-parity] FAIL: a structured_output capability row disagrees" >&2
  echo "  with its adapter's real wire-encoding behaviour — either a 'true' row whose" >&2
  echo "  encoder does not carry the schema, or a 'false' row Gate.Check does not refuse." >&2
  echo "  See the failing subtest name for the (kind, model) pair." >&2
  exit 2
fi

echo ""
echo "[structured-output-row-parity] clean — every capability row matches its adapter's real behaviour."
exit 0
