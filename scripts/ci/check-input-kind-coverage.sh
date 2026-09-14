#!/usr/bin/env bash
# check-input-kind-coverage.sh — G-2 (automation-actually-runs-01PMZ404
# UNIT-17): every declared corewf.InputKind value reaches an explicit
# per-kind control in the workflow run form.
#
# THE DEFECT CLASS
# -----------------
# core/workflows/types.go declares InputKind as a closed six-value enum
# (spec §1.9): string, multiline, enum, file, artifact_ref, project_ref.
# Before UNIT-14, frontend/src/views/workflows/WorkflowsView.vue's run
# form branched on exactly ONE value (`multiline`) with a bare `v-else`
# plain text box for the other five — so a workflow author who declared
# an `enum` input with a fixed option list still got a free-text box
# the user could fill with any string, silently discarding the
# constraint (A-11: "the author's constraint is validated at authoring
# time and discarded at run time"). U14 fixed this by adding four more
# explicit v-else-if arms. This gate is what stops it from regressing:
# a SEVENTH InputKind value added later, with no matching v-else-if arm,
# would silently fall through to the SAME bare v-else plain-text box —
# exactly the shape that shipped for five of six kinds before U14, and
# nothing short of a human noticing would catch it.
#
# WHY "≥ COUNT" ALONE IS THE WRONG SHAPE HERE
# ---------------------------------------------
# The naive version of this gate (spec §8's literal wording: "assert
# the number of v-if/v-else-if arms is >= the number of enum values")
# does not fit this component's actual, CORRECT shape: WorkflowsView.vue
# has five explicit v-else-if arms (multiline/enum/file/artifact_ref/
# project_ref) plus ONE bare v-else that is `string`'s intentional
# fallback control (a plain text box IS the right control for `string`
# — it needs no special widget). A pure arm-count check would either
# (a) undercount by one and false-positive on the correct implementation
# every single run, or (b) if a bare v-else is allowed to silently
# "cover" any number of missing kinds, become exactly the vacuous gate
# spec's own AC-018 warns against: a 7th kind with no arm would still
# pass, because the v-else would "cover" it too — the precise defect
# this gate exists to catch.
#
# So the actual check is a SET check, not a count: every InputKind
# value EXCEPT AT MOST ONE must have its own explicit
# v-if/v-else-if="inp.kind === '<value>'" arm. The one exception is
# spent on `string` today (verified below, not assumed) via the
# trailing bare v-else. Adding a 7th kind consumes no new exception
# slot — the budget is fixed at one — so it must get its own explicit
# arm or the gate fires.
#
# Exit codes:
#   0 — every kind has an explicit arm, or exactly one is covered by a
#       verified trailing bare v-else
#   1 — could not read the source files (path drift)
#   2 — one or more kinds have neither an explicit arm nor the one
#       allowed v-else fallback
#
# Usage: bash scripts/ci/check-input-kind-coverage.sh

set -euo pipefail

WORKTREE_ROOT="$(git rev-parse --show-toplevel 2>/dev/null || pwd)"
cd "$WORKTREE_ROOT"

TYPES_GO="core/workflows/types.go"
VIEW_VUE="frontend/src/views/workflows/WorkflowsView.vue"

if [[ ! -f "$TYPES_GO" ]]; then
  echo "[input-kind-coverage] FAIL: $TYPES_GO not found." >&2
  exit 1
fi
if [[ ! -f "$VIEW_VUE" ]]; then
  echo "[input-kind-coverage] FAIL: $VIEW_VUE not found." >&2
  exit 1
fi

# Extract every declared InputKind<Name> InputKind = "<value>" literal.
enum_values="$(grep -oE 'InputKind[A-Za-z]+[[:space:]]+InputKind[[:space:]]*=[[:space:]]*"[a-z_]+"' "$TYPES_GO" \
  | grep -oE '"[a-z_]+"$' | tr -d '"' | sort -u)"
if [[ -z "$enum_values" ]]; then
  echo "[input-kind-coverage] FAIL: found zero InputKind constants in $TYPES_GO — check the pattern, this is almost certainly a bug in this gate, not an empty enum." >&2
  exit 1
fi
enum_count=$(echo "$enum_values" | wc -l | tr -d ' ')

# Extract every kind literal explicitly checked in a v-if/v-else-if arm
# in the input-rendering block (inp.kind === '<value>').
explicit_kinds="$(grep -oE "inp\.kind === '[a-z_]+'" "$VIEW_VUE" \
  | grep -oE "'[a-z_]+'\$" | tr -d "'" | sort -u)"
explicit_count=$(echo "$explicit_kinds" | sed '/^$/d' | wc -l | tr -d ' ')

missing="$(comm -23 <(echo "$enum_values") <(echo "$explicit_kinds"))"
missing_count=$(echo "$missing" | sed '/^$/d' | wc -l | tr -d ' ')

echo "[input-kind-coverage] ${enum_count} declared InputKind value(s), ${explicit_count} with an explicit v-if/v-else-if arm."

if [[ "$missing_count" -eq 0 ]]; then
  echo "[input-kind-coverage] clean — every InputKind value has an explicit arm."
  exit 0
fi

if [[ "$missing_count" -gt 1 ]]; then
  echo "[input-kind-coverage] FAIL: ${missing_count} InputKind value(s) have no explicit v-if/v-else-if arm (at most 1 may fall through to a verified bare v-else):" >&2
  echo "$missing" | sed 's/^/    /' >&2
  exit 2
fi

# Exactly one kind is unmatched. This is only acceptable if (a) it is
# "string" — the one value the gate has verified today has an
# intentional plain-text-box fallback — and (b) a bare v-else (not
# v-else-if) actually exists in the file to catch it. Both conditions
# are re-checked here, not assumed, so a future edit that removes the
# bare v-else or repurposes it does not silently keep passing.
only_missing="$(echo "$missing" | head -1)"
if [[ "$only_missing" != "string" ]]; then
  echo "[input-kind-coverage] FAIL: InputKind value \"$only_missing\" has no explicit v-if/v-else-if arm, and it is not \"string\" (the only value this gate allows to fall through to the bare v-else default)." >&2
  exit 2
fi
if ! grep -qE '^\s*v-else\s*$' "$VIEW_VUE"; then
  echo "[input-kind-coverage] FAIL: InputKind value \"string\" has no explicit arm and no bare v-else fallback was found in $VIEW_VUE — the render form now silently drops the string input kind entirely." >&2
  exit 2
fi

echo "[input-kind-coverage] clean — \"string\" is the one value covered by a verified bare v-else fallback; every other InputKind value has an explicit arm."
exit 0
