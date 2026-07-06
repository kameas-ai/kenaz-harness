#!/usr/bin/env bash
# FR-005: Serve-dispatch drift check.
#
# Extracts all exported Bindings methods from core/rpc/bindings.go and
# compares them against the set of methods handled by the served-mode
# dispatcher in core/serve/server.go (both the RPC dispatch switch and
# the WS stream dispatch switch).
#
# Purpose: making it visible when a new desktop binding is added but
# serve mode is not updated.  The gap list is printed to stdout so CI
# logs capture it.  The check exits 0 (informational) by default so it
# never blocks a PR — it is a *visibility* tool, not a gate.
#
# To promote to gating, set SERVE_DRIFT_GATE=1 in the CI environment or
# pass --gate on the command line.  In gating mode, the script exits 1
# when unhandled methods are found.
#
# Usage:
#   bash scripts/ci/check-serve-dispatch-drift.sh          # informational
#   bash scripts/ci/check-serve-dispatch-drift.sh --gate   # fail on gap
#   SERVE_DRIFT_GATE=1 bash scripts/ci/check-serve-dispatch-drift.sh

set -euo pipefail

BINDINGS_FILE="core/rpc/bindings.go"
DISPATCH_FILE="core/serve/server.go"
GATE="${SERVE_DRIFT_GATE:-0}"

# Parse flags
for arg in "$@"; do
  case "$arg" in
    --gate) GATE=1 ;;
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

bindings_methods=$(
  grep -oE 'func \(b \*Bindings\) [A-Z][A-Za-z0-9_]+' "$BINDINGS_FILE" \
    | awk '{print $NF}' \
    | grep -vE "^($LIFECYCLE_SKIP)$" \
    | sort -u
)

# ── Step 2: collect methods handled in serve dispatch ────────────────────────
# We look for case literals in both:
#   (a) dispatch() — POST /rpc switch
#   (b) handleWS() — WS stream switch
# Both use the same `case "MethodName":` syntax.
# Auth_State is a serve-only synthetic method (no Bindings counterpart) —
# it's fine in the dispatch and won't show up in the gap list.
dispatch_methods=$(
  grep -oE 'case "[A-Za-z0-9_]+"' "$DISPATCH_FILE" \
    | sed 's/case "//;s/"//' \
    | sort -u
)

# ── Step 3: find the gap (bindings \ dispatch) ───────────────────────────────
gap=$(comm -23 <(echo "$bindings_methods") <(echo "$dispatch_methods"))

total_bindings=$(echo "$bindings_methods" | wc -l | tr -d ' ')
handled=$(echo "$dispatch_methods" | wc -l | tr -d ' ')
gap_count=$([ -n "$gap" ] && echo "$gap" | wc -l | tr -d ' ' || echo 0)

echo "[serve-drift] Desktop bindings: $total_bindings  Serve-handled: $handled  Gap: $gap_count"

if [[ -n "$gap" ]]; then
  echo ""
  echo "[serve-drift] Methods in desktop bindings NOT handled in serve mode:"
  echo "$gap" | sed 's/^/  /'
  echo ""
  echo "[serve-drift] These methods return \"not ported to served mode\" (FR-005)."
  if [[ "$GATE" == "1" ]]; then
    echo "[serve-drift] GATE=1: failing due to unhandled serve methods." >&2
    exit 1
  fi
  echo "[serve-drift] (informational — set SERVE_DRIFT_GATE=1 to gate)"
else
  echo "[serve-drift] No gap detected — all desktop bindings are handled in serve mode."
fi
