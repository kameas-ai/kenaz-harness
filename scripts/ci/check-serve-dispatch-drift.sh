#!/usr/bin/env bash
# FR-005 / I15: Serve-dispatch drift check.
#
# Extracts all exported Bindings methods from core/rpc/bindings.go and
# compares them against the set of methods handled by the served-mode
# dispatcher in core/serve/server.go (both the RPC dispatch switch and
# the WS stream dispatch switch), in BOTH directions:
#
#   forward  (bindings \ dispatch) — a desktop binding served mode never
#            answers. Gap allowlist: scripts/ci/allowlists/i15-serve-dispatch-gap.txt
#   reverse  (dispatch \ bindings) — a served-only case with no desktop
#            binding of the same name, e.g. a typo'd case string or an
#            orphaned served-only surface. Gap allowlist:
#            scripts/ci/allowlists/i15-serve-dispatch-reverse.txt
#
# THIS IS A GATE, NOT JUST VISIBILITY (promoted
# served-mode-is-a-real-mode-01PMZ707 WP02, spec §5.2, escalation-register
# E-2 :2067). GATE="${SERVE_DRIFT_GATE:-1}" — gating is now the DEFAULT.
# Before WP02 this script exited 0 unconditionally and required an operator
# to opt in with SERVE_DRIFT_GATE=1 or --gate; nothing ever set that in CI,
# so a docstring calling it "a *visibility* tool, not a gate" was literally
# true and 419 desktop bindings drifted out of served-mode reach with no PR
# ever failing over it.
#
# Set SERVE_DRIFT_GATE=0 (or pass --no-gate) to run informational-only, e.g.
# while iterating locally on a change that is expected to grow the gap
# before its allowlist entry lands in the same commit.
#
# An UNALLOWLISTED gap entry — in EITHER direction — fails the gate. A
# new desktop binding needs a serve dispatch case, a per-method
# disposition (boundary panel, gate, desktop-only-by-nature proof) with
# a dated allowlist line, or it fails the build. Allowlists shrink
# monotonically; nothing is added to either file without a date
# (CLAUDE.md § "Release ritual: unwired sweep" / "Rules").
#
# Usage:
#   bash scripts/ci/check-serve-dispatch-drift.sh             # gates (default)
#   bash scripts/ci/check-serve-dispatch-drift.sh --no-gate    # informational
#   SERVE_DRIFT_GATE=0 bash scripts/ci/check-serve-dispatch-drift.sh

set -euo pipefail

source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib/ci-gate.sh"

BINDINGS_FILE="core/rpc/bindings.go"
DISPATCH_FILE="core/serve/server.go"
ALLOWLIST_FWD="scripts/ci/allowlists/i15-serve-dispatch-gap.txt"
ALLOWLIST_REV="scripts/ci/allowlists/i15-serve-dispatch-reverse.txt"
GATE="${SERVE_DRIFT_GATE:-1}"

# Parse flags
for arg in "$@"; do
  case "$arg" in
    --gate) GATE=1 ;;
    --no-gate) GATE=0 ;;
  esac
done

if [[ ! -f "$BINDINGS_FILE" ]]; then
  echo "[serve-drift] $BINDINGS_FILE not found; skipping" >&2
  exit 0
fi

if [[ ! -f "$DISPATCH_FILE" ]]; then
  echo "[serve-drift] $DISPATCH_FILE not found; skipping" >&2
  exit 0
fi

# ── Step 1: collect exported Bindings methods (Wails-reflected surface) ──────
# Exported methods are those starting with an uppercase letter.
# Internal helpers (SetSettingsStore, SetContext, ctx) are excluded by
# the uppercase-only regex, but we also explicitly exclude lifecycle
# methods that Wails calls internally and are never called from the
# frontend JS.
LIFECYCLE_SKIP="SetSettingsStore|SetContext"

# Receiver name is a wildcard — see check-binding-names.sh for why hardcoding
# `b` silently reduced the extracted set to zero.
bindings_methods=$(
  grep -oE 'func \([A-Za-z_][A-Za-z0-9_]* \*Bindings\) [A-Z][A-Za-z0-9_]+' "$BINDINGS_FILE" \
    | awk '{print $NF}' \
    | grep -vE "^($LIFECYCLE_SKIP)$" \
    | sort -u
)

# ── Step 2: collect methods handled in serve dispatch ────────────────────────
# We look for case literals in both:
#   (a) dispatch() — POST /rpc switch
#   (b) handleWS() — WS stream switch
# Both use the same `case "MethodName":` syntax.
dispatch_methods=$(
  grep -oE 'case "[A-Za-z0-9_]+"' "$DISPATCH_FILE" \
    | sed 's/case "//;s/"//' \
    | sort -u
)

total_bindings=$(echo "$bindings_methods" | wc -l | tr -d ' ')
handled=$(echo "$dispatch_methods" | wc -l | tr -d ' ')

# ── Step 3: forward gap (bindings \ dispatch) ────────────────────────────────
fwd_gap=$(comm -23 <(echo "$bindings_methods") <(echo "$dispatch_methods"))
fwd_gap_count=$([ -n "$fwd_gap" ] && echo "$fwd_gap" | wc -l | tr -d ' ' || echo 0)

# ── Step 4: reverse gap (dispatch \ bindings) ────────────────────────────────
rev_gap=$(comm -13 <(echo "$bindings_methods") <(echo "$dispatch_methods"))
rev_gap_count=$([ -n "$rev_gap" ] && echo "$rev_gap" | wc -l | tr -d ' ' || echo 0)

echo "[serve-drift] Desktop bindings: $total_bindings  Serve-handled: $handled  Forward gap: $fwd_gap_count  Reverse gap: $rev_gap_count"

# ── Step 5: read both allowlists (comment-stripped, DATA lines only) ────────
read_allowlist() {
  local path="$1"
  if [[ -f "$path" ]]; then
    grep -v '^[[:space:]]*#' "$path" | grep -v '^[[:space:]]*$' || true
  fi
}

fwd_allow_data=$(read_allowlist "$ALLOWLIST_FWD")
rev_allow_data=$(read_allowlist "$ALLOWLIST_REV")

is_allowlisted() {
  local name="$1" data="$2"
  [[ -n "$data" && "$data" == *"\"${name}\""* ]]
}

# ── Step 6: subtract allowlisted entries from each gap ───────────────────────
fwd_unallowlisted=""
if [[ -n "$fwd_gap" ]]; then
  while IFS= read -r name; do
    [[ -z "$name" ]] && continue
    if ! is_allowlisted "$name" "$fwd_allow_data"; then
      fwd_unallowlisted+="${name}"$'\n'
    fi
  done <<< "$fwd_gap"
fi
fwd_unallowlisted="${fwd_unallowlisted%$'\n'}"

rev_unallowlisted=""
if [[ -n "$rev_gap" ]]; then
  while IFS= read -r name; do
    [[ -z "$name" ]] && continue
    if ! is_allowlisted "$name" "$rev_allow_data"; then
      rev_unallowlisted+="${name}"$'\n'
    fi
  done <<< "$rev_gap"
fi
rev_unallowlisted="${rev_unallowlisted%$'\n'}"

status=0

if [[ -n "$fwd_unallowlisted" ]]; then
  echo ""
  echo "[serve-drift] FORWARD: desktop bindings with NO serve dispatch case and NO allowlist entry:"
  echo "$fwd_unallowlisted" | sed 's/^/  /'
  echo ""
  echo "[serve-drift] Fix: add a serve dispatch case (port it), or a dated line in $ALLOWLIST_FWD"
  echo "[serve-drift] naming the disposition (boundary panel, !isServedMode() gate, or"
  echo "[serve-drift] desktop-only-by-nature proof) per CLAUDE.md's unwired-sweep disposition rules."
  status=1
fi

if [[ -n "$rev_unallowlisted" ]]; then
  echo ""
  echo "[serve-drift] REVERSE: serve dispatch cases with NO matching desktop binding and NO allowlist entry:"
  echo "$rev_unallowlisted" | sed 's/^/  /'
  echo ""
  echo "[serve-drift] Fix: this is usually a typo'd case string (fix it to match the real binding"
  echo "[serve-drift] name), or a genuinely serve-only method — if so, add a dated, justified line"
  echo "[serve-drift] to $ALLOWLIST_REV."
  status=1
fi

if [[ "$status" -eq 0 ]]; then
  echo "[serve-drift] clean — every forward and reverse gap entry is dispatched or explicitly allowlisted."
fi

if [[ "$status" -ne 0 ]]; then
  if [[ "$GATE" == "1" ]]; then
    echo "[serve-drift] GATE=1 (default): failing due to unallowlisted serve-dispatch drift." >&2
    exit 1
  fi
  echo "[serve-drift] (SERVE_DRIFT_GATE=0: informational — would fail with gating on)"
fi

exit 0
