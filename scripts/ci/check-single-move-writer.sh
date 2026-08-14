#!/usr/bin/env bash
# check-single-move-writer.sh — convergence guard for the transcript-move
# writer (model-moves-transcript-01PMCH01 WP01, spec §4: "One writer:
# session_write/the chat runner persists moves through the same seam — no
# second history-writing path").
#
# WHY A GATE AND NOT JUST THE COMPILER
# ------------------------------------
# session.Message's move fields (moveKind / moveIndex / moveTurnSpanID)
# are unexported, so no package outside core/session can set them — that
# half genuinely is compiler-enforced, and it is the strong half. This
# gate covers the two holes the compiler cannot see:
#
#   1. A second writer INSIDE core/session. Nothing stops a future
#      manager method, or the compaction summary path, from stamping
#      moveKind itself. Assignment must stay in moves.go.
#   2. Someone exporting the fields "to make testing easier", which
#      silently deletes the compiler guarantee everywhere at once.
#      Checked positively: the unexported declarations must be present.
#
# Plus the reachability direction the convergence doctrine cares about:
#   3. The seam must have exactly one production caller. Two callers is a
#      second history-writing path; zero means the seam is unwired and
#      the whole schema is inert plumbing.
#
# Exit codes:
#   0 — one writer, one seam caller, fields still unexported
#   1 — a violation, or the scan found nothing (a broken gate, not a
#       clean tree)
set -euo pipefail

source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib/ci-gate.sh"

GATE="[single-move-writer]"

SEAM_FILE="core/session/moves.go"
TYPES_FILE="core/session/types.go"

ci_require_file "$SEAM_FILE" "$GATE" \
  "The transcript-move seam moved. Re-point SEAM_FILE at its new home rather than letting the gate skip."
ci_require_file "$TYPES_FILE" "$GATE" \
  "session.Message moved. Re-point TYPES_FILE rather than letting the gate skip."

fail=0

# ---- 1. the fields are still unexported ----------------------------------
#
# Positive-presence check: each of the three must be declared lowercase on
# session.Message. Exporting one (MoveKind MoveKind) removes the
# declaration this greps for and fails here, loudly, instead of quietly
# dissolving the cross-package guarantee.
for field in moveKind moveIndex moveTurnSpanID; do
  if ! grep -qE "^[[:space:]]+${field}[[:space:]]+" "$TYPES_FILE"; then
    echo "$GATE FAIL: unexported field '${field}' is not declared in ${TYPES_FILE}." >&2
    echo "$GATE The single-writer rule rests on these three being unexported: that is what" >&2
    echo "$GATE stops every package outside core/session from minting move metadata." >&2
    echo "$GATE If the field was renamed, update this gate in the same commit. If it was" >&2
    echo "$GATE EXPORTED, do not update the gate — restore it." >&2
    fail=1
  fi
done

# ---- 2. only moves.go assigns them ---------------------------------------
#
# An assignment is either a field-selector target (`m.moveKind =`) or a
# struct-literal key (`moveKind:`). Reads (`m.moveKind` in a return or a
# comparison) are fine and deliberately not matched, and the leading `.`
# / line-start-key anchoring keeps a same-named LOCAL variable from
# reading as a field write — `moveIndex = int64(...)` inside a column
# helper is not a second writer.
ASSIGN_RE='(\.(moveKind|moveIndex|moveTurnSpanID)[[:space:]]*=[^=])|(^[[:space:]]*(moveKind|moveIndex|moveTurnSpanID):)'

offenders=""
while IFS= read -r f; do
  case "$f" in
  "$SEAM_FILE") continue ;;
  *_test.go) continue ;;
  esac
  if hits=$(grep -nE "$ASSIGN_RE" "$f" 2>/dev/null); then
    offenders+="${f}:"$'\n'"$(echo "$hits" | sed 's/^/    /')"$'\n'
  fi
done < <(find core/session -name '*.go' -type f | sort)

if [[ -n "$offenders" ]]; then
  echo "$GATE FAIL: move metadata is assigned outside ${SEAM_FILE}:" >&2
  echo "$offenders" >&2
  echo "$GATE Every transcript entry — classic or move — goes through" >&2
  echo "$GATE Manager.AppendTranscriptEntry. A second site that stamps moveKind is the" >&2
  echo "$GATE second history-writing path spec §4 forbids: two writers means two" >&2
  echo "$GATE definitions of what a move IS, and the pair that drifts is the one that" >&2
  echo "$GATE ships an orphaned tool_use to a provider." >&2
  fail=1
fi

# Sanity: moves.go must itself contain assignments. If it does not, the
# regex above stopped matching the code and clause 2 is passing on an
# empty search — the exact "gate that inspected nothing" failure the
# 2026-08-08 sweep found six of.
if ! grep -qE "$ASSIGN_RE" "$SEAM_FILE"; then
  echo "$GATE FAIL: no move-field assignment found in ${SEAM_FILE}." >&2
  echo "$GATE The seam is supposed to be the one place these are set. Zero matches means" >&2
  echo "$GATE this gate's pattern went stale, not that the tree is clean." >&2
  fail=1
fi

# ---- 3. exactly one production caller of the seam ------------------------
callers=$(grep -rln 'AppendTranscriptEntry(' \
  --include='*.go' core/ 2>/dev/null |
  grep -v '_test\.go$' |
  grep -v '^core/session/' |
  sort || true)
caller_count=$(printf '%s' "$callers" | grep -c . || true)

if [[ "$caller_count" -eq 0 ]]; then
  echo "$GATE FAIL: no production caller of Manager.AppendTranscriptEntry." >&2
  echo "$GATE The seam exists and nothing reaches it — the move schema would be inert" >&2
  echo "$GATE plumbing, which is the defect the release ritual's unwired sweep hunts." >&2
  fail=1
elif [[ "$caller_count" -gt 1 ]]; then
  echo "$GATE FAIL: $caller_count production callers of Manager.AppendTranscriptEntry:" >&2
  echo "$callers" | sed 's/^/    /' >&2
  echo "$GATE Exactly one binding is allowed — llmHistoryWriter.AppendEntry in" >&2
  echo "$GATE core/rpc/api.go, which is what agentgraph.HistoryWriter resolves to." >&2
  echo "$GATE Route the new call site through that seam instead of opening a second one." >&2
  fail=1
fi

if [[ "$fail" -ne 0 ]]; then
  exit 1
fi

echo "$GATE clean — move metadata is written only by ${SEAM_FILE}, reached through one seam caller (${callers})."
