#!/usr/bin/env bash
# check-graph-write-paths.sh — a graph reaches disk only through a
# validated, Cedar-gated path.
#
# WHAT THIS PROTECTS, AND WHY A TEST WAS NOT ENOUGH:
# model-authored-graphs-01PMGA01 UNIT-2 stopped the manager persisting
# graphs the validator rejects, and UNIT-4 made graph writes ask Cedar
# (THE SEAM: "No commit may make a graph-writing verb reachable from a
# model until AC-004 passes"). Both fixes live inside Manager.saveGraph.
#
# Neither survives a SECOND writer. Anyone adding an os.WriteFile against
# the library directory — a repair path, an importer, a migration, a
# "just this once" fixup — silently bypasses BOTH the validator and the
# consent gate, and every existing test still passes because they all
# drive saveGraph. That is the class UNIT-8 owes a gate for, and it is
# the same shape as check-single-move-writer.sh: one seam, enforced.
#
# THE RULE (two legs):
#
#   A. Within core/rpc/views/agentgraph/, production code may contain at
#      most ONE filesystem-mutating call against the user library. The
#      permitted site is Manager.saveGraph.
#   B. saveGraph must call the validator BEFORE it writes. A write that
#      precedes validation persists exactly what UNIT-2 forbade.
#
# Deletion is deliberately NOT covered: deleteGraph removes a file, it
# cannot persist an invalid graph, and it has its own bundled-id guard.
#
# Usage: bash scripts/ci/check-graph-write-paths.sh
#
# GRAPH_WRITE_PATHS_PKG overrides the scanned package. It is NOT a
# suppression knob — it cannot make a real violation pass — it only
# points the scan at a directory, so gates_can_fail_test.go can plant a
# probe without mutating the real package.
#
# Exit codes: 0 clean; 1 a second writer, or a write before validation,
# or the scan root is unreadable (never a silent pass).

set -euo pipefail

source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib/ci-gate.sh"

GATE="[graph-write-paths]"
PKG="${GRAPH_WRITE_PATHS_PKG:-core/rpc/views/agentgraph}"

ci_require_dir "$PKG" "$GATE" \
  "Both legs scan it; with no scan root this gate cannot see a second writer."

mapfile -t GO_FILES < <(find "$PKG" -maxdepth 1 -name '*.go' -type f ! -name '*_test.go' | sort)
if [[ ${#GO_FILES[@]} -eq 0 ]]; then
  echo "${GATE} FAIL: no production .go files under ${PKG}/." >&2
  echo "${GATE} Leg A would find zero writers and report clean — a vacuous pass." >&2
  exit 1
fi

# ---- Leg A: exactly one filesystem-mutating call -----------------------
# Comment lines are excluded: two comments in this package deliberately
# NAME os.WriteFile while warning about the bypass, and a gate that
# counts its own warning as a violation trains people to delete the
# warning.
MUTATORS='os\.WriteFile|os\.Create|os\.OpenFile|ioutil\.WriteFile'
# -H, not -n alone: grep omits the filename when given exactly ONE file,
# which shifts every colon-separated field and made leg B parse the line
# CONTENT as a line number. The real package has many files so it worked
# by accident; a single-file package would have broken it silently.
writes=$(grep -HnE "(${MUTATORS})\(" "${GO_FILES[@]}" 2>/dev/null \
  | grep -vE '^[^:]*:[0-9]+:[[:space:]]*//' || true)

write_count=$(printf '%s' "$writes" | grep -c . || true)

if [[ "$write_count" -gt 1 ]]; then
  echo "${GATE} FAIL: ${write_count} filesystem-mutating call(s) in ${PKG}/; exactly 1 is allowed." >&2
  printf '%s\n' "$writes" | sed 's/^/  /' >&2
  echo "${GATE}" >&2
  echo "${GATE} The permitted writer is Manager.saveGraph, which validates (UNIT-2)" >&2
  echo "${GATE} and asks Cedar (UNIT-4) first. A second writer bypasses BOTH, and" >&2
  echo "${GATE} every existing test still passes because they all drive saveGraph." >&2
  echo "${GATE} Route the new call through saveGraph instead of opening a second path." >&2
  exit 1
fi

if [[ "$write_count" -eq 0 ]]; then
  echo "${GATE} FAIL: no filesystem-mutating call found in ${PKG}/." >&2
  echo "${GATE} saveGraph is supposed to contain exactly one. Zero means the seam" >&2
  echo "${GATE} moved and this gate is now watching the wrong package — which would" >&2
  echo "${GATE} otherwise read as 'clean' forever." >&2
  exit 1
fi

# ---- Leg B: validation precedes the write ------------------------------
MANAGER="${PKG}/manager.go"
if [[ -f "$MANAGER" ]]; then
  save_start=$(grep -n 'func (m \*Manager) saveGraph(' "$MANAGER" | head -1 | cut -d: -f1 || true)
  if [[ -z "$save_start" ]]; then
    echo "${GATE} FAIL: Manager.saveGraph not found in ${MANAGER}." >&2
    echo "${GATE} Leg B cannot check ordering against a function that is not there." >&2
    exit 1
  fi
  write_line=$(printf '%s' "$writes" | head -1 | awk -F: '{print $2}')
  # LoadYAML is the kernel parse+validate saveGraph runs before persisting.
  validate_line=$(awk -v s="$save_start" 'NR>=s && /coreag\.LoadYAML\(/ {print NR; exit}' "$MANAGER" || true)
  if [[ -z "$validate_line" ]]; then
    echo "${GATE} FAIL: saveGraph does not call coreag.LoadYAML before persisting." >&2
    echo "${GATE} UNIT-2's rule is that no graph the validator rejects reaches disk." >&2
    exit 1
  fi
  # Numeric-safe under `set -u`: a non-numeric or empty capture must not
  # crash the gate into an ambiguous exit — it is a scan failure, and a
  # gate that cannot compute its own comparison says so.
  if ! [[ "${write_line:-}" =~ ^[0-9]+$ ]] || ! [[ "${validate_line:-}" =~ ^[0-9]+$ ]]; then
    echo "${GATE} FAIL: could not resolve line numbers (write='${write_line:-}' validate='${validate_line:-}')." >&2
    echo "${GATE} Refusing to report clean from a comparison that did not run." >&2
    exit 1
  fi
  if (( validate_line > write_line )); then
    echo "${GATE} FAIL: saveGraph writes at line ${write_line} but validates at ${validate_line}." >&2
    echo "${GATE} Validation must PRECEDE the write, or an invalid graph is already" >&2
    echo "${GATE} on disk by the time it is rejected." >&2
    exit 1
  fi
fi

echo "${GATE} OK — one validated writer in ${PKG}/ (Manager.saveGraph), validation precedes the write."
