#!/usr/bin/env bash
# check-session-message-writers.sh — G-1 (chat-turn-integrity-01PMZ606 WP14,
# spec.md §6): "no writer to session_messages outside the sanctioned set."
#
# THE CLASS THIS GATE EXISTS FOR
# -------------------------------
# This mission's own P0 (UNIT-1/CHAT-01) was a durability mechanism —
# runPeriodicFlush's partial-message flush — writing INTO the user's
# transcript when it was only ever meant to checkpoint it. Every existing
# gate in this directory answers a different question (single writer for
# MOVE metadata, single persistence FILE, destructive migration coverage);
# none of them would have caught a second, unplanned call site of the
# functions that actually persist a row into session_messages. This gate
# is that missing tripwire: it enumerates every non-test call site of
# AppendMessage / AppendContinuation / ApplyCompaction and fails on any
# growth beyond a dated allowlist — not because a new call site is
# necessarily wrong, but because durability mechanisms writing into the
# transcript is exactly the class that must never land silently again.
#
# WHAT THIS GATE CAN AND CANNOT SEE (read before trusting it)
# -------------------------------------------------------------
# The check is `grep -c '\.<Symbol>\('` per non-test *.go file under
# core/, restricted to a LITERAL DOT immediately before the symbol name so
# it does not also match the method's own declaration
# (`func (m *Manager) AppendMessage(`, no dot precedes the name) or an
# interface method signature (`AppendMessage(ctx ...)` inside a `type X
# interface { ... }` block, also no leading dot). This is a deliberate,
# narrow textual match, not a type-checked call graph, and it has three
# known blind spots:
#
#   1. METHOD-NAME COLLISION. Any receiver type with a same-named method
#      would be counted identically to session.Manager's. One exists
#      today: core/eval.Recorder.AppendMessage (a distinct, unrelated
#      capture-for-eval method). It currently has ZERO non-test call
#      sites anywhere in the repo, so it contributes nothing to the
#      seeded baseline — but if something ever calls it, this gate will
#      demand an allowlist entry for a call that never touches
#      session_messages. That is a false positive this gate WILL produce;
#      it is intentionally accepted (a false positive here costs an
#      allowlist line and a beat of review — a missed real writer costs a
#      P0) rather than silently disambiguated by receiver type, which
#      grep cannot do soundly.
#   2. MULTI-LINE CALL EXPRESSIONS. A call split across lines
#      (`a.mgr.\n\tAppendMessage(...)`, which gofmt does not produce for
#      these call shapes today but could for a long argument list) puts
#      the dot and the symbol name on different lines and this gate does
#      not see it at all — neither as present nor as absent. No call in
#      the current tree is shaped this way; if one ever is, this gate
#      silently stops covering it until someone notices the line count
#      does not match reality.
#   3. LINE-COUNT GRANULARITY. `grep -c` counts MATCHING LINES, not
#      occurrences — two calls to the same symbol on one physical line
#      would count as one. No call site in the current tree does this.
#
# What the gate DOES promise: every call shaped like the ones enumerated
# below, on their own line, through any receiver, is counted per (file,
# symbol) pair, and the count may not increase without a dated allowlist
# update naming the new line. That is sufficient to catch the actual
# defect class (a NEW call site appearing) even though it cannot prove a
# call site's TYPE is session.Manager without a real Go type checker.
#
# ALLOWLIST FORMAT
# -----------------
# scripts/ci/allowlists/i-session-message-writers.txt, one
# `<repo-relative-path>:<Symbol>:<count>` per line. `count` is the number
# of matching lines this gate tolerates in that file for that symbol — NOT
# a call site identifier, because line numbers shift on unrelated edits
# and using them as keys would force an allowlist update on every nearby
# reformat. A file/symbol pair not listed is implicitly allowlisted at 0.
#
# Fails on:
#   - a (file, symbol) pair whose real count EXCEEDS its allowlist count
#     (a new writer landed — the class this gate exists for), or
#   - a (file, symbol) pair whose allowlist count EXCEEDS its real count
#     (a stale entry — allowlists shrink monotonically per CLAUDE.md; a
#     line that no longer matches anything is documenting a writer that
#     no longer exists and must be deleted in the same change that
#     removed the call).
#
# Usage: bash scripts/ci/check-session-message-writers.sh

set -euo pipefail

source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib/ci-gate.sh"

GATE="[session-message-writers]"
SCAN_ROOT="core"
ALLOWLIST="scripts/ci/allowlists/i-session-message-writers.txt"

ci_require_dir "$SCAN_ROOT" "$GATE"
ci_require_file "$ALLOWLIST" "$GATE"

SYMBOLS=(AppendMessage AppendContinuation ApplyCompaction)

mapfile -t GO_FILES < <(find "$SCAN_ROOT" -name '*.go' ! -name '*_test.go' | sort)

if [[ ${#GO_FILES[@]} -eq 0 ]]; then
  echo "${GATE} FAIL: no non-test Go files found under $SCAN_ROOT — nothing to check (unexpected)." >&2
  exit 2
fi

declare -A ALLOWED=()
while IFS= read -r line; do
  line="${line%%#*}"
  line="$(echo "$line" | tr -d '[:space:]')"
  [[ -z "$line" ]] && continue
  ALLOWED["$line"]=1
done < "$ALLOWLIST"

declare -A ACTUAL=()

for f in "${GO_FILES[@]}"; do
  for sym in "${SYMBOLS[@]}"; do
    count=$(grep -cE "\.${sym}\(" "$f" 2>/dev/null || true)
    [[ -z "$count" ]] && count=0
    if [[ "$count" -gt 0 ]]; then
      ACTUAL["${f}:${sym}"]="$count"
    fi
  done
done

fail=0

# Direction 1: a real count exceeding what the allowlist permits — the
# class this gate exists for.
for key in "${!ACTUAL[@]}"; do
  real="${ACTUAL[$key]}"
  # ALLOWED entries are "path:Symbol:count" but ACTUAL is keyed
  # "path:Symbol" -> count, so look up the count-suffixed form.
  allowed_line=""
  for al in "${!ALLOWED[@]}"; do
    if [[ "$al" == "${key}:"* ]]; then
      allowed_line="$al"
      break
    fi
  done
  allowed_count=0
  if [[ -n "$allowed_line" ]]; then
    allowed_count="${allowed_line##*:}"
  fi
  if [[ "$real" -gt "$allowed_count" ]]; then
    fail=1
    file="${key%%:*}"
    sym="${key##*:}"
    echo "" >&2
    echo "${GATE} FAIL: ${file} calls .${sym}( ${real} time(s); allowlist permits ${allowed_count}." >&2
    echo "  A new call site of AppendMessage/AppendContinuation/ApplyCompaction is a new" >&2
    echo "  writer into session_messages — this mission's own P0 was exactly that class," >&2
    echo "  landing unplanned. Either this is deliberate — add" >&2
    echo "  \"${file}:${sym}:${real}\" to ${ALLOWLIST} with a dated, owned justification —" >&2
    echo "  or it is an accidental second writer and must not exist." >&2
  fi
done

# Direction 2: an allowlist entry with no matching call left — a stale
# row. Allowlists shrink monotonically (CLAUDE.md); a row surviving past
# the writer it named is a lie the same way an unremoved gate-line is.
for al in "${!ALLOWED[@]}"; do
  file="$(echo "$al" | cut -d: -f1)"
  sym="$(echo "$al" | cut -d: -f2)"
  allowed_count="$(echo "$al" | cut -d: -f3)"
  key="${file}:${sym}"
  real="${ACTUAL[$key]:-0}"
  if [[ "$real" -lt "$allowed_count" ]]; then
    fail=1
    echo "" >&2
    echo "${GATE} FAIL: ${ALLOWLIST} allows ${file}:${sym} up to ${allowed_count} call site(s)," >&2
    echo "  but only ${real} remain(s) in the tree. Delete or shrink this line in the same" >&2
    echo "  change that removed the call — allowlists shrink monotonically." >&2
  fi
done

if [[ $fail -ne 0 ]]; then
  echo "" >&2
  echo "${GATE} FAIL — see violations above." >&2
  exit 2
fi

total=0
for v in "${ACTUAL[@]}"; do
  total=$((total + v))
done
echo "${GATE} clean — ${total} allowlisted call site(s) across ${#ACTUAL[@]} (file, symbol) pair(s), no additions, no stale entries."
exit 0
