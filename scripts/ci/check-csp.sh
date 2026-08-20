#!/usr/bin/env bash
# Privacy CI invariant #1 (plan §4.3): every shipped bundle's built HTML
# MUST ship a strict CSP. This script greps the file and asserts:
#   - script-src does NOT contain unsafe-inline or unsafe-eval
#   - connect-src matches the MODE's expectation (see table below)
#   - no http://  or https:// CDN host appears
#   - font-src is 'self'
#
# Run after `npm run build` (desktop mode) or `npm run build:served`
# (served mode).
#
# Paths are anchored to the repo root by lib/ci-gate.sh so the default
# argument means the same thing regardless of the caller's cwd.
#
# USAGE
#   bash scripts/ci/check-csp.sh [dist-html-path] [mode]
#
#   mode is "desktop" (default) or "served". connect-src expectations
#   differ deliberately — entry-points-and-crash-reporting-01PMZD13
#   UNIT-5:
#
#     | mode    | connect-src |
#     |---------|-------------|
#     | desktop | 'none'      | — the harness talks to nothing over HTTP.
#     | served  | 'self'      | — the browser MUST reach /rpc and /ws on
#     |         |             |   the harness's own HTTP server; 'none'
#     |         |             |   here is not a stricter version of the
#     |         |             |   desktop policy, it is a BROKEN one.
#
# HISTORY: before UNIT-5, this script only ever checked frontend/dist/
# index.html (the desktop bundle). The served-mode SPA
# (frontend/dist-served/served.html, built by `npm run build:served`,
# shipped as kenaz-harness-served-spa.tar.gz) was a second real artifact
# this gate never looked at, and pr.yml invoked the guard exactly once
# with no argument. The script's OWN previous suggested fix — a second
# invocation with the desktop connect-src assertion unchanged and the
# wrong filename (frontend/dist-served/index.html — the real file is
# served.html, per frontend/vite.config.ts's outDir/input and
# release.yml:879's own `test -f dist-served/served.html`) — was itself
# broken twice over and would have failed a correct served bundle. UNIT-5
# fixes both: the right filename, and a mode-aware connect-src table
# instead of a hardcoded 'none'.
set -euo pipefail

source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib/ci-gate.sh"

# CSP_CHECK_DIST_HTML / CSP_CHECK_MODE override the positional defaults —
# not a separate code path, the SAME comparison below runs either way.
# Exists so gates_can_fail_test.go's planted-violation case can point this
# gate at a throwaway fixture file (this gate reads a BUILT artifact,
# which the test harness does not produce) without a CLI-argument-passing
# harness change. Mirrors the UPGRADE_SNAPSHOTS_BASE_REF /
# UPGRADE_SNAPSHOTS_EXPECT_TAG precedent in check-upgrade-snapshots-locked.sh.
DIST_HTML="${1:-${CSP_CHECK_DIST_HTML:-frontend/dist/index.html}}"
MODE="${2:-${CSP_CHECK_MODE:-desktop}}"

case "$MODE" in
  desktop) EXPECT_CONNECT_SRC="connect-src 'none'" ;;
  served) EXPECT_CONNECT_SRC="connect-src 'self'" ;;
  *)
    echo "[csp] FAIL: unknown mode '$MODE' — expected 'desktop' or 'served'." >&2
    exit 1
    ;;
esac

if [[ ! -f "$DIST_HTML" ]]; then
  echo "[csp] $DIST_HTML not found — run 'npm run build' (desktop) or 'npm run build:served' (served) first" >&2
  exit 1
fi

CSP=$(grep -oE 'Content-Security-Policy" content="[^"]+"' "$DIST_HTML" | sed -E 's/.*content="([^"]+)".*/\1/' || true)

if [[ -z "$CSP" ]]; then
  echo "[csp] No <meta http-equiv=\"Content-Security-Policy\"> in $DIST_HTML" >&2
  exit 1
fi

echo "[csp] ($MODE) $DIST_HTML CSP: $CSP"

fail=0

if echo "$CSP" | grep -q "script-src[^;]*unsafe-eval"; then
  echo "[csp] FAIL: script-src contains unsafe-eval" >&2
  fail=1
fi

if echo "$CSP" | grep -qE "script-src[^;]*'unsafe-inline'"; then
  echo "[csp] FAIL: script-src contains 'unsafe-inline'" >&2
  fail=1
fi

if ! echo "$CSP" | grep -qF "$EXPECT_CONNECT_SRC"; then
  echo "[csp] FAIL: connect-src must be exactly '${EXPECT_CONNECT_SRC#connect-src }' in $MODE mode" >&2
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

echo "[csp] clean — invariant #1 (plan §4.3) passes for the $MODE bundle ($DIST_HTML)."
