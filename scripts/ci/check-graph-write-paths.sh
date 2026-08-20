#!/usr/bin/env bash
# check-graph-write-paths.sh — a graph reaches disk only through the one
# validated, Cedar-gated writer.
#
# WHAT THIS PROTECTS:
# model-authored-graphs-01PMGA01 UNIT-2 stopped the manager persisting
# graphs the validator rejects; UNIT-4 (THE SEAM) made graph writes ask
# Cedar. Both fixes live INSIDE Manager.saveGraph. Neither survives a
# write that happens anywhere else, and every existing test still passes
# when one appears, because they all drive saveGraph.
#
# THE RULE: every filesystem-mutating call in the package must sit
# INSIDE Manager.saveGraph, and saveGraph must validate before the first
# of them. Anything else needs a dated allowlist entry.
#
# ---- WHY THE FIRST VERSION OF THIS GATE WAS WRONG ----
#
# v1 counted mutators package-wide and compared bare line numbers. An
# independent review broke it against the REAL production file: move the
# write out of saveGraph into an unvalidated sibling method, and the
# count stays at one while the line comparison still "passes" — the gate
# reported OK for exactly the bypass its own docstring warns about. Its
# planted-violation proof only ever added a SECOND writer, so CI's
# gate-testing meta-suite could not see the hole either.
#
# v1 also over-matched: an unrelated `os.Create` elsewhere in the package,
# or an inline trailing comment merely NAMING a mutator, failed the build.
# A gate that fails on innocent code gets deleted by the next person.
#
# Both are fixed here: containment is checked structurally (is the call
# inside saveGraph's line range in the file that declares it), and
# anything legitimately outside is justifiable rather than fatal.
#
# Usage: bash scripts/ci/check-graph-write-paths.sh
#
# GRAPH_WRITE_PATHS_PKG overrides the scanned package. NOT a suppression
# knob — it cannot make a real violation pass; it only points the scan at
# a directory so gates_can_fail_test.go can plant probes.
#
# Exit codes: 0 clean; 1 a mutator outside saveGraph without an allowlist
# entry, a write before validation, or an unreadable/absent scan root.

set -euo pipefail

source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib/ci-gate.sh"

GATE="[graph-write-paths]"
PKG="${GRAPH_WRITE_PATHS_PKG:-core/rpc/views/agentgraph}"
ALLOWLIST="scripts/ci/allowlists/graph-write-paths-outside-savegraph.txt"

ci_require_dir "$PKG" "$GATE" \
  "Both legs scan it; with no scan root this gate cannot see a bypass."

mapfile -t GO_FILES < <(find "$PKG" -maxdepth 1 -name '*.go' -type f ! -name '*_test.go' | sort)
if [[ ${#GO_FILES[@]} -eq 0 ]]; then
  echo "${GATE} FAIL: no production .go files under ${PKG}/." >&2
  echo "${GATE} The scan would find zero writers and report clean — a vacuous pass." >&2
  exit 1
fi

MUTATORS='os\.WriteFile|os\.Create|os\.OpenFile|os\.Rename|ioutil\.WriteFile'

# Locate saveGraph: which file declares it, and its line range. The range
# ends at the next column-0 "}" — gofmt guarantees that shape, and every
# file here is gofmt-clean.
SAVE_FILE=""; SAVE_START=0; SAVE_END=0
for f in "${GO_FILES[@]}"; do
  read -r s e < <(awk '/^func \(m \*Manager\) saveGraph\(/{st=NR} st&&/^}$/{print st, NR; exit}' "$f") || true
  if [[ -n "${s:-}" && "${s:-0}" -gt 0 ]]; then SAVE_FILE="$f"; SAVE_START="$s"; SAVE_END="$e"; break; fi
done

if [[ -z "$SAVE_FILE" ]]; then
  echo "${GATE} FAIL: Manager.saveGraph not found in ${PKG}/." >&2
  echo "${GATE} The seam moved. This gate is now watching the wrong package," >&2
  echo "${GATE} which would otherwise read as 'clean' forever." >&2
  exit 1
fi

# Strip trailing comments BEFORE matching, so a line that merely NAMES a
# mutator inside a comment is not a call. v1 only skipped comment-only
# lines and failed on `x := 1 // never add os.WriteFile here`.
violations=0
inside_count=0
first_write_line=0

for f in "${GO_FILES[@]}"; do
  while IFS= read -r entry; do
    ln="${entry%%:*}"
    [[ -z "$ln" ]] && continue
    if [[ "$f" == "$SAVE_FILE" ]] && (( ln >= SAVE_START && ln <= SAVE_END )); then
      inside_count=$((inside_count + 1))
      if (( first_write_line == 0 || ln < first_write_line )); then first_write_line=$ln; fi
      continue
    fi
    # Outside saveGraph. Allowlisted?
    if [[ -f "$ALLOWLIST" ]] && grep -qF "$(basename "$f"):$ln" "$ALLOWLIST" 2>/dev/null; then
      continue
    fi
    if (( violations == 0 )); then
      echo "${GATE} FAIL: filesystem-mutating call outside Manager.saveGraph." >&2
    fi
    echo "${GATE}   $f:$ln" >&2
    violations=$((violations + 1))
  done < <(
    sed 's://.*::' "$f" | grep -nE "(${MUTATORS})\(" || true
  )
done

if (( violations > 0 )); then
  cat >&2 <<MSG
${GATE}
${GATE} saveGraph validates (UNIT-2) and asks Cedar (UNIT-4) before it
${GATE} persists. A write reached from anywhere else skips BOTH, and every
${GATE} existing test still passes, because they all drive saveGraph.
${GATE}
${GATE} Route the write through saveGraph. If it genuinely does not touch
${GATE} the graph library, add "<file>:<line>" to
${GATE}   ${ALLOWLIST}
${GATE} with a dated justification naming the blocker and the owner.
MSG
  exit 1
fi

if (( inside_count == 0 )); then
  echo "${GATE} FAIL: saveGraph contains no filesystem-mutating call." >&2
  echo "${GATE} It is supposed to be the one writer. Zero means the write moved" >&2
  echo "${GATE} out — the exact bypass this gate exists to catch — or the seam" >&2
  echo "${GATE} was refactored and this check is now watching nothing." >&2
  exit 1
fi

# Validation must precede the FIRST write inside saveGraph.
validate_line=$(awk -v s="$SAVE_START" -v e="$SAVE_END" \
  'NR>=s && NR<=e && /coreag\.LoadYAML\(/ {print NR; exit}' "$SAVE_FILE" || true)

if [[ -z "$validate_line" ]]; then
  echo "${GATE} FAIL: saveGraph does not call coreag.LoadYAML." >&2
  echo "${GATE} UNIT-2's rule is that no graph the validator rejects reaches disk." >&2
  exit 1
fi

if ! [[ "$validate_line" =~ ^[0-9]+$ ]]; then
  echo "${GATE} FAIL: could not resolve the validation line; refusing to report clean." >&2
  exit 1
fi

if (( validate_line > first_write_line )); then
  echo "${GATE} FAIL: saveGraph writes at line ${first_write_line} but validates at ${validate_line}." >&2
  echo "${GATE} Validation must PRECEDE the write, or an invalid graph is already" >&2
  echo "${GATE} on disk by the time it is rejected." >&2
  exit 1
fi

echo "${GATE} OK — ${inside_count} mutating call(s), all inside Manager.saveGraph (${SAVE_FILE}:${SAVE_START}-${SAVE_END}), validation at ${validate_line} precedes the first write at ${first_write_line}."
