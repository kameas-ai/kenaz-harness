#!/usr/bin/env bash
# check-listpending-coverage.sh — every `<Family>_ListPending` Wails binding
# must have a matching `listPending` reader somewhere in the typed
# frontend client layer (frontend/src/lib/*.ts).
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

# The reader may live in harnessClient.ts OR in any sibling standalone
# client module under frontend/src/lib/. Restricting this gate to
# harnessClient.ts alone was a real defect, found on release/v0.78.2:
# model-scheduled-jobs-01PMSJ01 put BlockedRequests_ListPending's reader in
# frontend/src/lib/blockedRequestsClient.ts:58 — a genuine reader, following
# the established standalone-client pattern that scheduledChatClient.ts,
# workflowsClient.ts and updateClient.ts already use — and the gate reported
# the binding unwired. The gate's stated property is "no client code ever
# calls it"; the property it actually checked was "harnessClient.ts calls
# it". Those came apart the first time a mission declined to grow the
# monolithic client. Scope stays the typed client layer (lib/*.ts), not all
# of frontend/src, so a .vue reaching past the client layer is still a miss.
CLIENT_FILES=("$CLIENT_FILE")
while IFS= read -r f; do
  [[ -f "$f" && "$f" != "$CLIENT_FILE" ]] && CLIENT_FILES+=("$f")
done < <(LC_ALL=C ls -1 frontend/src/lib/*.ts 2>/dev/null | LC_ALL=C sort)

if [[ "${#CLIENT_FILES[@]}" -eq 0 ]]; then
  echo "$GATE FAIL: discovered 0 client files under frontend/src/lib/ — the" >&2
  echo "$GATE layout changed and this gate can no longer see any reader." >&2
  exit 1
fi

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
  if grep -qF -- "$wails_pattern" "${CLIENT_FILES[@]}"; then
    continue
  fi
  if grep -qF -- "$served_pattern" "${CLIENT_FILES[@]}"; then
    continue
  fi
  echo "$GATE FAIL: ${fam}_ListPending has no reader in any of ${#CLIENT_FILES[@]} client file(s) under frontend/src/lib/." >&2
  echo "$GATE Searched: ${CLIENT_FILES[*]}" >&2
  echo "$GATE Looked for either '<bridge>().${fam}_ListPending(' (Wails transport) or" >&2
  echo "$GATE \"'${fam}_ListPending'\" (served transport). Neither call form was found —" >&2
  echo "$GATE this binding cannot reconcile any frontend state after a reload." >&2
  fail=1
done <<< "$families"

if [[ $fail -ne 0 ]]; then
  exit 1
fi

echo "$GATE clean — every *_ListPending binding has a reader in the typed client layer (${#CLIENT_FILES[@]} file(s) searched)."
