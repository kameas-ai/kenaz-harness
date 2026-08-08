#!/usr/bin/env bash
# Privacy CI invariant #1 (plan §4.3): the production-built dist/index.html
# MUST ship a strict CSP. This script greps the file and asserts:
#   - script-src does NOT contain unsafe-inline or unsafe-eval
#   - connect-src is exactly 'none'
#   - no http://  or https:// CDN host appears
#   - font-src is 'self'
#
# Run after `npm run build`.
#
# Paths are anchored to the repo root by lib/ci-gate.sh so the default
# argument means the same thing regardless of the caller's cwd. (This gate
# already failed loudly on a missing file rather than passing, unlike its
# siblings — the anchor keeps it that way for the right reason.)
#
# COVERAGE GAP: only the desktop bundle (frontend/dist) is checked. The
# served-mode SPA is built from frontend/served.html into
# frontend/dist-served and carries its own __CSP_PLACEHOLDER__ substitution;
# nothing checks that one. pr.yml would need a second invocation
# (`bash scripts/ci/check-csp.sh frontend/dist-served/index.html` after
# `npm run build:served`) — the script already takes the path as $1. Left to
# the pr.yml owner because #279 is concurrently editing that file.
set -euo pipefail

source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib/ci-gate.sh"

DIST_HTML="${1:-frontend/dist/index.html}"

if [[ ! -f "$DIST_HTML" ]]; then
  echo "[csp] $DIST_HTML not found — run 'npm run build' first" >&2
  exit 1
fi

CSP=$(grep -oE 'Content-Security-Policy" content="[^"]+"' "$DIST_HTML" | sed -E 's/.*content="([^"]+)".*/\1/' || true)

if [[ -z "$CSP" ]]; then
  echo "[csp] No <meta http-equiv=\"Content-Security-Policy\"> in $DIST_HTML" >&2
  exit 1
fi

echo "[csp] CSP: $CSP"

fail=0

if echo "$CSP" | grep -q "script-src[^;]*unsafe-eval"; then
  echo "[csp] FAIL: script-src contains unsafe-eval" >&2
  fail=1
fi

if echo "$CSP" | grep -qE "script-src[^;]*'unsafe-inline'"; then
  echo "[csp] FAIL: script-src contains 'unsafe-inline'" >&2
  fail=1
fi

if ! echo "$CSP" | grep -q "connect-src 'none'"; then
  echo "[csp] FAIL: connect-src must be exactly 'none'" >&2
  fail=1
fi

if ! echo "$CSP" | grep -q "font-src 'self'"; then
  echo "[csp] FAIL: font-src must be 'self'" >&2
  fail=1
fi

if echo "$CSP" | grep -qE 'https?://'; then
  echo "[csp] FAIL: external host present in CSP — no CDNs allowed" >&2
  fail=1
fi

if [[ $fail -ne 0 ]]; then
  exit 1
fi

echo "[csp] clean — invariant #1 (plan §4.3) passes."
