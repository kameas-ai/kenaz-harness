#!/usr/bin/env bash
# check-listpending-coverage.sh — every `<Family>_ListPending` Wails binding
# must have a matching `listPending` reader somewhere in
# frontend/src/lib/harnessClient.ts.
#
# WHY THIS EXISTS (consent-surfaces-truth-01PMTR01 WP03 / dead-code-audit
# finding A11): `Permissions_ListPending` existed end to end on the Go side
# — bindings.go, the view, the registry — with ZERO callers anywhere in the
# frontend. A permission prompt lost across a reload never returned: the
# turn hung until the 5-minute registry timeout fired and fail-closed
# denied it. Nothing caught this because the existing gates check different
# things — check-binding-names.sh checks naming, check-wailsjs-isolation.sh
# checks import direction — and a *_ListPending binding with a body that
# compiles and a doc comment that reads correctly looks, by every existing
# measure, exactly like a wired one.
#
# This gate would have caught A11 on day one: it fails when a binding's
# name promises reconciliation ("*_ListPending") and no client code ever
# calls it.
#
# WHAT COUNTS AS A READER: either call form already in use for the two
# ListPending families that DO have readers (Elicit, Confirm) —
#   - a Wails-transport call:   b().<Family>_ListPending(
#   - a served-transport call:  'X_ListPending'  (transport.call<...>('X_ListPending', ...))
# Either is sufficient; a family need not support both transports.
set -euo pipefail

source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib/ci-gate.sh"

GATE="[listpending-coverage]"
BINDINGS_FILE="core/rpc/bindings.go"
CLIENT_FILE="frontend/src/lib/harnessClient.ts"

ci_require_file "$BINDINGS_FILE" "$GATE"
ci_require_file "$CLIENT_FILE" "$GATE"

# Collect additional split bindings_*.go files the same way check-codegen.sh
# does, so a ListPending binding added to a split file is not invisible here.
BINDINGS_SOURCES=("$BINDINGS_FILE")
while IFS= read -r f; do
  [[ -f "$f" ]] && BINDINGS_SOURCES+=("$f")
done < <(LC_ALL=C ls -1 core/rpc/bindings_*.go 2>/dev/null | LC_ALL=C sort)

# Extract every `<Family>_ListPending` method name declared on *Bindings.
families=$(grep -ohE 'func \([A-Za-z_][A-Za-z0-9_]* \*Bindings\) [A-Za-z0-9]+_ListPending\(' "${BINDINGS_SOURCES[@]}" \
  | sed -E 's/^func \([A-Za-z_][A-Za-z0-9_]* \*Bindings\) ([A-Za-z0-9]+)_ListPending\(/\1/')

if [[ -z "$families" ]]; then
  echo "$GATE FAIL: parsed 0 *_ListPending bindings out of ${BINDINGS_SOURCES[*]}." >&2
  echo "$GATE Either the naming convention changed (update the pattern here) or" >&2
  echo "$GATE every *_ListPending binding was removed — both are findings, not a clean bill." >&2
  exit 1
fi

fail=0
while IFS= read -r fam; do
  [[ -z "$fam" ]] && continue
  wails_pattern="().${fam}_ListPending("
  served_pattern="'${fam}_ListPending'"
  if grep -qF -- "$wails_pattern" "$CLIENT_FILE"; then
    continue
  fi
  if grep -qF -- "$served_pattern" "$CLIENT_FILE"; then
    continue
  fi
  echo "$GATE FAIL: ${fam}_ListPending has no reader in $CLIENT_FILE." >&2
  echo "$GATE Looked for either 'b().${fam}_ListPending(' (Wails transport) or" >&2
  echo "$GATE \"'${fam}_ListPending'\" (served transport). Neither call form was found —" >&2
  echo "$GATE this binding cannot reconcile any frontend state after a reload." >&2
  fail=1
done <<< "$families"

if [[ $fail -ne 0 ]]; then
  exit 1
fi

echo "$GATE clean."
