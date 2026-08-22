#!/usr/bin/env bash
# check-serve-gap-classification.sh — every entry in the I15 forward gap
# allowlist (scripts/ci/allowlists/i15-serve-dispatch-gap.txt) must sit
# under one of spec.md's five WP07 classes, and every entry under
# `untriaged` must carry a date and a named owner.
#
# WHY THIS EXISTS (served-mode-is-a-real-mode-01PMZ707 WP07, AC-717):
# "Every allowlist entry carries a class from Sec.5.7's five, and
# untriaged carries a date and an owner. A CI check (or the gate itself)
# rejects a classless entry." check-serve-dispatch-drift.sh's
# read_allowlist() strips every comment line and treats the file as a
# flat list of quoted names — it has never known or cared about
# classification, so a WP07 that reclassified 416 entries and then relied
# on the drift gate alone to keep them classified would have shipped a
# taxonomy nothing enforces: exactly the same "allowlist that never
# shrinks because nothing measures it" failure a gate that can never fail
# already was (E-2).
#
# WHAT COUNTS AS CLASSIFIED: a quoted entry line is classified if it
# appears anywhere AFTER a `# CLASS: <name>` header line and before the
# next one (or EOF), where <name> is one of the five spec.md Sec.5.7
# classes. An entry before the first CLASS header, or under a header
# whose name is not one of the five, fails.
#
# WHAT COUNTS AS DATED-AND-OWNED (untriaged only): the contiguous comment
# block immediately above the entry (the reason paragraph — this file
# groups entries with identical reasoning under one shared comment) must
# contain a four-digit year (a date) and the literal string "Owner:".
set -euo pipefail

source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib/ci-gate.sh"

GATE="[serve-gap-classification]"
ALLOWLIST="scripts/ci/allowlists/i15-serve-dispatch-gap.txt"

ci_require_file "$ALLOWLIST" "$GATE"

VALID_CLASSES="gated boundary-panelled unrouted desktop-only-by-nature untriaged"

fail=0
current_class=""
# reason_block accumulates comment lines since the last blank-line reset,
# so it holds exactly the paragraph immediately above the entry line(s)
# it applies to.
reason_block=""
last_was_entry=0
classless=()
undated=()

while IFS= read -r line; do
  if [[ "$line" =~ ^#\ CLASS:\ ([a-zA-Z-]+)\ --\  ]]; then
    candidate="${BASH_REMATCH[1]}"
    match=0
    for c in $VALID_CLASSES; do
      [[ "$c" == "$candidate" ]] && match=1
    done
    if [[ "$match" -eq 0 ]]; then
      echo "${GATE} FAIL: '# CLASS: ${candidate}' is not one of the five spec Sec.5.7 classes (${VALID_CLASSES})." >&2
      fail=1
    fi
    current_class="$candidate"
    reason_block=""
    continue
  fi
  if [[ "$line" =~ ^#(.*)$ ]]; then
    # A comment that FOLLOWS an entry starts a new reason paragraph. Without
    # this, a paragraph bleeds across entries: append
    #     # No date or owner here.
    #     "Zz_Injected"
    # directly after a group whose reason DID name a date and owner, and the
    # planted entry inherits that reason and passes.
    #
    # Found 2026-08-22 by CI, not locally — the planted-violation proof for
    # this very rule went vacuous the moment a file edit changed which
    # paragraph sat at EOF. Same shape as finding D1: a proof that depends on
    # surrounding content rather than planting its own conditions.
    if [[ "$last_was_entry" -eq 1 ]]; then
      reason_block=""
      last_was_entry=0
    fi
    reason_block="${reason_block}${BASH_REMATCH[1]}"$'\n'
    continue
  fi
  if [[ -z "${line// }" ]]; then
    # Blank line: do NOT reset reason_block here — a wrapped multi-line
    # comment paragraph in this file has no blank line inside it, but the
    # blank line AFTER a paragraph, before the next paragraph's comment
    # starts, must not bleed the old reason into the next group. Reset on
    # the next comment line instead, which happens naturally since a new
    # `#` line after a blank line starts a fresh accumulation only if we
    # reset here.
    reason_block=""
    continue
  fi
  if [[ "$line" =~ ^\"([A-Za-z0-9_]+)\"$ ]]; then
    name="${BASH_REMATCH[1]}"
    last_was_entry=1
    if [[ -z "$current_class" ]]; then
      classless+=("$name")
      continue
    fi
    if [[ "$current_class" == "untriaged" ]]; then
      has_date=0
      has_owner=0
      [[ "$reason_block" =~ 20[0-9][0-9] ]] && has_date=1
      [[ "$reason_block" == *"Owner:"* ]] && has_owner=1
      if [[ "$has_date" -eq 0 || "$has_owner" -eq 0 ]]; then
        undated+=("$name")
      fi
    fi
    continue
  fi
  # Any other non-blank, non-comment, non-quoted-entry line is a format
  # violation (the allowlist format is "one double-quoted method name per
  # line").
  echo "${GATE} FAIL: unrecognised line (not a comment, blank, or \"Quoted_Name\"): ${line}" >&2
  fail=1
done < "$ALLOWLIST"

if [[ "${#classless[@]}" -gt 0 ]]; then
  echo "${GATE} FAIL: ${#classless[@]} entries appear before any '# CLASS:' header (classless): ${classless[*]}" >&2
  fail=1
fi
if [[ "${#undated[@]}" -gt 0 ]]; then
  echo "${GATE} FAIL: ${#undated[@]} untriaged entries missing a date and/or 'Owner:' in their reason paragraph: ${undated[*]}" >&2
  fail=1
fi

# ---- per-class self-count check ----
#
# Each "# CLASS:" section states its own entry count. Nothing verified
# those until 2026-08-22: the independent review of PR #306 (finding N4)
# found the boundary-panelled header claiming 187 when the section held
# 188, and the file total claiming 416 against an actual 417.
#
# A stated count that drifts is the same failure as a stale trip-wire
# comment: the next reader either trusts a wrong number or learns to
# ignore the number, and both are worse than having none. Cheap to check,
# so checked.
while IFS='|' read -r cls stated actual; do
  [[ -z "$cls" ]] && continue
  if [[ "$stated" != "$actual" ]]; then
    if [[ -z "$stated" ]]; then
      echo "${GATE} FAIL: CLASS ${cls} has no '# N entries.' header line; the section holds ${actual}." >&2
    else
      echo "${GATE} FAIL: CLASS ${cls} header says ${stated} entries; the section holds ${actual}." >&2
    fi
    fail=1
  fi
done < <(awk '
  /^# CLASS: [a-z-]+ -- / { if (cls != "") print cls "|" stated "|" n; cls=$3; sub(/:$/,"",cls); stated=""; n=0; next }
  cls != "" && /^# [0-9]+ entries\.$/ { if (stated == "") stated=$2; next }
  cls != "" && /^"/ { n++ }
  END { if (cls != "") print cls "|" stated "|" n }
' "$ALLOWLIST")

if [[ "$fail" -ne 0 ]]; then
  echo "${GATE} Fix: give the entry a '# CLASS: <name>' header from {${VALID_CLASSES}}," >&2
  echo "${GATE} a reason paragraph naming a date and an 'Owner:' if untriaged," >&2
  echo "${GATE} and keep each section's '# N entries.' line equal to its real count." >&2
  exit 1
fi

echo "${GATE} clean — every entry in ${ALLOWLIST} carries a valid class, and every untriaged entry is dated and owned."
