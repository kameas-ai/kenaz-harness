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
# Plus the two ways to mint move metadata without writing an assignment
# a field-level grep can see (found by adversarial review of WP01):
#   2b. Calling the move-column HELPERS (applyMoveColumns /
#       moveColumnValues) from somewhere other than the store's row-scan
#       sites — the assignment stays in moves.go, only the call moves.
#   2c. Direct SQL against session_messages.move_index / turn_span_id,
#       which never touches session.Message at all.
#
# Plus the reachability direction the convergence doctrine cares about:
#   3. The seam must have exactly one production caller, counted
#      repo-wide. Two callers is a second history-writing path; zero
#      means the seam is unwired and the whole schema is inert plumbing.
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
# anchoring keeps a same-named LOCAL variable from reading as a field
# write — `moveIndex = int64(...)` inside a column helper is not a
# second writer.
#
# The struct-literal alternative deliberately accepts the key ANYWHERE on
# the line, not just at line start: gofmt keeps a short literal on one
# line, so `return Message{moveKind: MoveKindFinal}` is the shape a
# second writer would most plausibly take. Anchoring it to `{` or `,`
# (rather than bare `moveKind:`) is what keeps prose in a comment —
# "moveKind: the discriminator" — from reading as code.
ASSIGN_RE='(\.(moveKind|moveIndex|moveTurnSpanID)[[:space:]]*=[^=])|(^[[:space:]]*(moveKind|moveIndex|moveTurnSpanID):)|([{,][[:space:]]*(moveKind|moveIndex|moveTurnSpanID)[[:space:]]*:)'

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

# ---- 2b. the minting HELPERS stay where the assignments are ---------------
#
# Clause 2 greps for the assignment itself, so a helper INSIDE moves.go
# that any other core/session file can call to stamp the fields —
# `applyMoveColumns(&m, sql.NullString{String: "final", Valid: true}, …)`
# — mints move metadata without a single character matching ASSIGN_RE.
# The helpers exist for the store's two row-scan sites; anything else
# calling them is clause 2 wearing a hat.
HELPER_RE='(applyMoveColumns|moveColumnValues)\('
helper_offenders=""
while IFS= read -r f; do
  case "$f" in
  "$SEAM_FILE" | core/session/store.go) continue ;;
  *_test.go) continue ;;
  esac
  if hits=$(grep -nE "$HELPER_RE" "$f" 2>/dev/null); then
    helper_offenders+="${f}:"$'\n'"$(echo "$hits" | sed 's/^/    /')"$'\n'
  fi
done < <(find core/session -name '*.go' -type f | sort)

if [[ -n "$helper_offenders" ]]; then
  echo "$GATE FAIL: the move-column helpers are called outside ${SEAM_FILE} / core/session/store.go:" >&2
  echo "$helper_offenders" >&2
  echo "$GATE applyMoveColumns / moveColumnValues are the store's row-scan and bind" >&2
  echo "$GATE plumbing. Calling them from anywhere else stamps move metadata onto a" >&2
  echo "$GATE Message without any line matching the clause-2 assignment pattern — a" >&2
  echo "$GATE second writer the compiler and clause 2 both wave through." >&2
  fail=1
fi

# ---- 2c. nothing writes the move COLUMNS behind the Go fields -------------
#
# The strongest bypass is not a Go assignment at all: an
# `UPDATE session_messages SET kind = …` in a new store method reaches
# the durable row without touching Message at all, and every clause above
# is blind to it. The column names move_index / turn_span_id are unique
# to this schema, so naming them outside the store + the migration that
# creates them is the signal.
# PRECISION (2026-08-14, model-moves-transcript-01PMCH01 WP02): the scan
# skips `//` comment lines and struct-tag lines. SQL never lives in
# either, so nothing this clause exists to catch can hide there — but
# prose describing the schema, and the `json:"move_index"` tag on the
# stream-boundary event that mirrors the column for the frontend, both
# named the columns in Go files and tripped a check about SQL. Narrowing
# to non-comment, non-tag lines keeps every INSERT/UPDATE/SELECT form
# caught (the planted probe in scripts/ci/gates_can_fail_test.go is one)
# while letting the schema be described in the language of the schema.
SQL_ALLOWED='^(core/session/store\.go|core/session/migrations_moves\.go|core/session/migrations\.go|core/session/moves\.go)$'
sql_offenders=$(grep -rn 'move_index\|turn_span_id' \
  --include='*.go' --include='*.sql' core/ 2>/dev/null |
  grep -v '^[^:]*_test\.go:' |
  grep -vE '^[^:]*:[0-9]+:[[:space:]]*//' |
  grep -vE '^[^:]*:[0-9]+:.*json:"' |
  cut -d: -f1 |
  grep -vE "$SQL_ALLOWED" |
  sort -u || true)

if [[ -n "$sql_offenders" ]]; then
  echo "$GATE FAIL: the move columns are named outside the store + migration:" >&2
  echo "$sql_offenders" | sed 's/^/    /' >&2
  echo "$GATE Direct SQL against session_messages.kind / move_index / turn_span_id" >&2
  echo "$GATE bypasses session.Message entirely — the unexported fields, the seam and" >&2
  echo "$GATE every clause above. Route the write through Manager.AppendTranscriptEntry." >&2
  fail=1
fi

# ---- 3. exactly one production caller of the seam ------------------------
#
# Scanned repo-wide, not just core/: a caller in cmd/ or a top-level
# package is exactly as much a second writing path, and scoping the scan
# to core/ would also let the gate report "zero callers" as "clean" if
# the one binding ever moved out.
callers=$(grep -rln 'AppendTranscriptEntry(' \
  --include='*.go' . 2>/dev/null |
  sed 's|^\./||' |
  grep -v '_test\.go$' |
  grep -v '^core/session/' |
  grep -v '^\(vendor\|node_modules\|frontend\)/' |
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
