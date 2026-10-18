#!/usr/bin/env bash
# check-keyring-seam.sh — keyring-seam-01 (2026-09-15).
#
# Enforces the single-seam contract for OS keychain access: only
# core/keyring/ (the seam package) may import github.com/zalando/go-keyring
# directly. Every other package must go through core/keyring's
# Get/Set/Delete/ErrNotFound instead.
#
# WHY THIS EXISTS. Seven packages independently patched their own TestMain
# with keyring.MockInit() over several releases because each had a
# process-global OS-keychain call reachable from a test — the two most
# recent being core/rpc/views/catalog and core/rpc/views/contexts (PR #348,
# landed the same day this gate was written). Per-package mocking is
# whack-a-mole: any new test that establishes signed-in fleet state can
# silently reintroduce a real-keychain hit, which on the owner's own
# machine surfaced as repeated "fleet:prod:access_token cannot be found"
# Keychain popups (tests carry no KENAZ_HARNESS_ENV and default to the prod
# account namespace), and as sequential hangs of
# check-tests-are-hermetic.sh (macOS's security agent HANGS, not errors,
# inside go-keyring's exec of /usr/bin/security when there is no
# interactive session under a sandboxed HOME). core/keyring closes the
# class structurally: it selects the in-memory mock itself, once, under
# `go test`, before any test or goroutine runs. This gate is what keeps the
# class closed — it fails the build the moment a NEW file reaches around
# the seam and imports the vendor SDK directly.
#
# DISCOVERY FLOOR. If core/keyring/ (the seam package itself) is not found,
# this gate FAILS LOUDLY. This is intentionally NOT the fork-tolerant
# "package removed, nothing to check, PASS" shape check-no-fleet-imports.sh
# uses for core/fleet/ — core/keyring is a first-party package this repo
# always ships, not an optional vendor boundary. Its absence means this
# gate would otherwise be checking an import restriction against a package
# that does not exist, which is a silent-green false pass, not a real one.
#
# Usage: bash scripts/ci/check-keyring-seam.sh

set -euo pipefail

source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib/ci-gate.sh"

GATE="[keyring-seam]"

ci_require_dir "core/keyring" "$GATE" \
  "core/keyring is the seam package this gate enforces against; if it was renamed or moved, update SEAM_PKG below in the same commit."

MODULE="$(go list -m)"
SEAM_PKG="${MODULE}/core/keyring"
VENDOR_PKG="github.com/zalando/go-keyring"

echo "${GATE} seam package: ${SEAM_PKG}"
echo "${GATE} vendor package: ${VENDOR_PKG}"

# Dated allowlist for legitimate exceptions. Empty today — there should be
# none; the whole point of the seam is that nothing else needs the vendor
# import. Add entries here only with a date + reason (CLAUDE.md: allowlists
# shrink monotonically).
ALLOWLIST=(
  "${SEAM_PKG}"
)

ALL_PKGS=$(go list -tags ignore ./core/... ./cmd/... 2>/dev/null || true)
if [ -z "$ALL_PKGS" ]; then
  ALL_PKGS=$(go list ./core/... ./cmd/... 2>/dev/null || true)
fi

if [ -z "$ALL_PKGS" ]; then
  echo "${GATE} FAIL: 'go list ./core/... ./cmd/...' returned no packages." >&2
  echo "${GATE} core/keyring/ exists (checked above), so this is a toolchain" >&2
  echo "${GATE} failure (missing module cache? build error?), not an empty tree." >&2
  echo "${GATE} Re-run 'go list ./core/... ./cmd/...' to see the real error." >&2
  go list ./core/... ./cmd/... >/dev/null || true
  exit 1
fi

VIOLATIONS=()

for pkg in $ALL_PKGS; do
  allowed=false
  for a in "${ALLOWLIST[@]}"; do
    if [[ "$pkg" == "$a" ]]; then
      allowed=true
      break
    fi
  done
  if $allowed; then
    continue
  fi

  # Check non-test imports, in-package test imports, and external test
  # ("_test" package) imports — a *_test.go file reaching around the seam
  # is exactly the class this gate exists to catch (that is how all seven
  # prior per-package MockInit patches got in).
  imports=$(go list -f '{{join .Imports " "}}' "$pkg" 2>/dev/null || true)
  test_imports=$(go list -f '{{join .TestImports " "}}' "$pkg" 2>/dev/null || true)
  xtest_imports=$(go list -f '{{join .XTestImports " "}}' "$pkg" 2>/dev/null || true)
  if echo "$imports $test_imports $xtest_imports" | grep -q "${VENDOR_PKG}"; then
    VIOLATIONS+=("$pkg")
  fi
done

if [ ${#VIOLATIONS[@]} -eq 0 ]; then
  echo "${GATE} clean — no direct ${VENDOR_PKG} imports outside ${SEAM_PKG}: PASS"
  exit 0
fi

echo "${GATE} FAIL — the following packages import ${VENDOR_PKG} directly:" >&2
for v in "${VIOLATIONS[@]}"; do
  echo "  $v" >&2
done
echo "" >&2
echo "${GATE} Only ${SEAM_PKG} may import ${VENDOR_PKG}. Route through" >&2
echo "${GATE} core/keyring's Get/Set/Delete/ErrNotFound instead." >&2
exit 1
