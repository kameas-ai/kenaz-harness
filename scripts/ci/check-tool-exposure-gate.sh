#!/usr/bin/env bash
# check-tool-exposure-gate.sh — FR-E1 / acceptance criterion 6 of
# tool-context-budget-01TCBUD01 (WP08): no tool schema reaches
# llm.GenerationRequest.Tools without going through the exposure
# partition.
#
# THE DEFECT CLASS
# ----------------
# Before the mission, every model call carried all 143 installed tools'
# full schemas (~220k prompt tokens on a one-word turn; a 131k-window
# model could not run at all) because the request builder copied the
# whole discovered catalog into GenerationRequest.Tools. The mission put
# a partition in between: toolexposure.ResolvedCatalog (tiers: org pin →
# session → project → user → org default → harness default) →
# chat.exposureTurn.selectTools (hot / pinned / activated, fitted to the
# schema budget) → GenerationRequest.SetTools. A summary-tier tool is
# sent only after kenaz__load_tools (or an auto-activation) activates it.
#
# The regression is a new request builder — or a refactor of the old one
# — that writes Tools from the catalog directly. Every partition test
# keeps passing in that state, because every one of them drives the
# partition.
#
# WHAT THIS CHECKS
# ----------------
# (a) STRUCTURAL (scripts/ci/cmd/checktoolexposure): every non-test writer
#     of GenerationRequest.Tools under core/ — a `Tools:` key in a
#     GenerationRequest literal, a SetTools call, or `x.Tools = …` on a
#     visibly GenerationRequest-typed x — must be listed in
#     scripts/ci/allowlists/tool-exposure-writers.txt: either it IS the
#     partition path, or it is a dated, owned gap. Unlisted → fail;
#     stale entry → fail. Discovery floor: finding no GenerationRequest
#     literal or no SetTools call fails rather than passing vacuously.
# (b) RUNTIME: the partition path itself still refuses summary tools —
#       core/rpc/views/agentgraph/chat
#         TestRequestBuilder_NeverSendsSummaryToolUnlessActivated  (the FR-E1 seed)
#       core/rpc
#         TestToolExposureWiring_FirstTurnToolTokens  (newLLMStack over a
#         real core wires the load core, so production never takes the
#         nil-exposure "send everything" branch; first turn sends only
#         the hot set)
#     Each must report `--- PASS: <name>`; "no tests to run" (a renamed
#     test) fails.
#
# Planted-violation proof: scripts/ci/gates_can_fail_test.go
# TestToolExposureGate_PlantedDirectToolsWriteFires plants a direct
# `gen.Tools = …` write in the chat package and asserts this gate fails.
#
# TOOL_EXPOSURE_GATE_STRUCTURAL_ONLY=1 skips (b) — used only by the
# planted-violation meta-tests, which exercise (a). CI runs both halves.
#
# Usage: bash scripts/ci/check-tool-exposure-gate.sh (from anywhere).

set -euo pipefail

source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib/ci-gate.sh"

GATE="[tool-exposure-gate]"
ALLOW_FILE="scripts/ci/allowlists/tool-exposure-writers.txt"
CHAT_PKG="core/rpc/views/agentgraph/chat"

ci_require_dir core "$GATE"
ci_require_dir "$CHAT_PKG" "$GATE"
ci_require_file "$ALLOW_FILE" "$GATE"

# ---- (a) structural ----
BIN_DIR="$(mktemp -d)"
trap 'rm -rf "$BIN_DIR"' EXIT
if ! go build -o "$BIN_DIR/checktoolexposure" ./scripts/ci/cmd/checktoolexposure/; then
  echo "${GATE} FAIL: checktoolexposure failed to build." >&2
  exit 1
fi
set +e
"$BIN_DIR/checktoolexposure"
rc=$?
set -e
if [[ $rc -ne 0 ]]; then
  echo "${GATE} FAIL — structural check (exit ${rc})." >&2
  exit 1
fi

if [[ "${TOOL_EXPOSURE_GATE_STRUCTURAL_ONLY:-0}" == "1" ]]; then
  echo "${GATE} structural half clean; runtime half skipped (TOOL_EXPOSURE_GATE_STRUCTURAL_ONLY=1)."
  exit 0
fi

# ---- (b) runtime ----
run_named_test() {
  local pkg="$1" name="$2" out
  set +e
  out=$(CGO_ENABLED="${CGO_ENABLED:-1}" go test -count=1 -short -run "^${name}\$" -v "./${pkg}/" 2>&1)
  local code=$?
  set -e
  if [[ $code -ne 0 ]]; then
    printf '%s\n' "$out" | tail -40 >&2
    echo "${GATE} FAIL: ${pkg} ${name} failed (exit ${code})." >&2
    exit 1
  fi
  if ! grep -q -- "--- PASS: ${name} " <<< "$out"; then
    printf '%s\n' "$out" | tail -20 >&2
    echo "${GATE} FAIL: ${pkg} ${name} did not run (renamed or deleted?). Update this gate in the same commit." >&2
    exit 1
  fi
  echo "${GATE} ${pkg} ${name}: PASS"
}

run_named_test "$CHAT_PKG" TestRequestBuilder_NeverSendsSummaryToolUnlessActivated
run_named_test core/rpc TestToolExposureWiring_FirstTurnToolTokens

echo "${GATE} clean — every GenerationRequest.Tools writer is the exposure path or a dated gap in ${ALLOW_FILE}, and the partition refuses un-activated summary tools."
