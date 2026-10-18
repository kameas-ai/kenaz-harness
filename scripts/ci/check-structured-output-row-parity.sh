#!/usr/bin/env bash
# check-structured-output-row-parity.sh — a capability row cannot
# silently desynchronize from its own wire-encoding
# (structured-output-is-reachable-01PMZE14, UNIT-6/WP09, gate G-3;
# CLAUDE.md "Release ritual: unwired sweep" — "a toggle that reports it
# is on").
#
# RENAMED (PR #323 review) from this WP's original framing — "a
# capability row cannot advertise a structured-output mode its adapter
# cannot serve" — which overclaims. See the SCOPE BOUNDARY below.
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
# SCOPE BOUNDARY — read before trusting a green run here as "vendor
# support is correct": six of eight registered kinds (anthropic,
# openai, openrouter, azure-openai, custom-openai, ollama) route
# through encoders with NO internal capability awareness —
# ApplyResponseFormat / openaiwire.BuildRequestBody emit
# response_format for ANY model whenever asked, regardless of what the
# row says. For those six this gate can only catch an encoder that
# REGRESSES while its row still claims support; it CANNOT catch a row
# that was wrong about real vendor support from the day it was
# written — that needs a live probe or a maintained ground-truth
# table, neither of which this gate is. Only gemini (schema-keyword
# translation can fail) and bedrock (checked white-box against its
# real, capability-relevant Converse-API encoder) have any
# independent proof beyond "the row and the mechanical encoder agree
# with each other by construction."
#
# The mechanism is a Go test, not static analysis — see
# core/llm/registry/wp09_g3_capability_row_parity_test.go (the cross-
# adapter cases; TestG3_CapabilityRowMatchesAdapterBehaviour — see its
# package-level comment for the full per-kind boundary) and its
# bedrock-package companion
# core/llm/bedrock/wp09_g3_row_parity_test.go
# (TestG3_Bedrock_TrueRowMatchesConverseEncoder — bedrock's exported
# StructuredOutputAdapter method is a documented no-op, so its real
# encoder can only be driven white-box). Mirrors
# check-knob-coverage.sh's shape: this script doesn't re-implement the
# check, it runs the test(s) and reports.
#
# WP09_G3_OVERLAY (optional env var): a go build/test -overlay= JSON
# file path. When set, passed straight through to the nested `go test`
# invocation below. This exists so gates_can_fail_test.go's planted-
# violation proof can substitute a mutated core/llm/gemini/wire.go
# WITHOUT EVER WRITING TO THE REAL FILE — see
# TestStructuredOutputRowParityGate_PlantedEncoderDropFires. A bare
# read-mutate-restore (write the real file, defer a restore) can leave
# the working tree permanently mutated if the process is killed
# mid-test (SIGKILL, OOM, a CI -timeout firing) — a defer cannot run
# after a hard kill. The overlay makes that hazard class structurally
# impossible: the real file is never opened for writing, so there is
# nothing for an interrupted run to leave behind.
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

overlay_flag=""
if [[ -n "${WP09_G3_OVERLAY:-}" ]]; then
  echo "[structured-output-row-parity] using overlay: ${WP09_G3_OVERLAY}"
  overlay_flag="-overlay=${WP09_G3_OVERLAY}"
fi

echo "[structured-output-row-parity] running every TestG3_* test under ./core/llm/..."
# shellcheck disable=SC2086 -- overlay_flag is a single, controlled
# -overlay=<path> token (or empty); intentionally unquoted so the
# empty case contributes zero words to the command line instead of one.
if ! go test ./core/llm/... $overlay_flag -run '^TestG3_' -count=1 -v 2>&1; then
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
