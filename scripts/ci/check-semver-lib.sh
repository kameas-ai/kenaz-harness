#!/usr/bin/env bash
# check-semver-lib.sh — self-test for scripts/ci/lib/semver.sh.
#
# lib/semver.sh decides whether release.yml's publish-s3 may move an env's
# stable manifest.json pointer. If its ordering is wrong the failure is not a
# red build — it is every auto-updater being pointed at an older release. That
# decision runs only inside a release, where nothing tests it, so the
# comparison and the decision table are pinned here and run on every PR.
#
# Planted-violation proof: scripts/ci/gates_can_fail_test.go
# "semver-lib/lexical-compare" appends a lexical semver_cmp to the lib (the
# string-compare mistake that orders v0.9.0 above v0.10.0) and asserts this
# gate fails.
set -euo pipefail
source "$(dirname "${BASH_SOURCE[0]}")/lib/ci-gate.sh"
ci_require_file scripts/ci/lib/semver.sh "[semver-lib]"
# shellcheck source=lib/semver.sh
source scripts/ci/lib/semver.sh

FAIL=0
N=0

expect_cmp() {
  local a="$1" b="$2" want="$3" got rc=0
  N=$((N + 1))
  got="$(semver_cmp "$a" "$b")" || rc=$?
  if [[ "$want" == invalid ]]; then
    if [[ $rc -ne 2 ]]; then
      echo "[semver-lib] FAIL: semver_cmp $a $b — want invalid (rc 2), got rc=$rc out='$got'" >&2
      FAIL=$((FAIL + 1))
    fi
    return 0
  fi
  if [[ $rc -ne 0 || "$got" != "$want" ]]; then
    echo "[semver-lib] FAIL: semver_cmp $a $b — want $want, got '$got' (rc=$rc)" >&2
    FAIL=$((FAIL + 1))
  fi
}

expect_decision() {
  local env="$1" new="$2" cur="$3" want="$4" got
  N=$((N + 1))
  got="$(pointer_decision "$env" "$new" "$cur")"
  if [[ "$got" != "$want" ]]; then
    echo "[semver-lib] FAIL: pointer_decision $env '$new' '$cur' — want $want, got '$got'" >&2
    FAIL=$((FAIL + 1))
  fi
}

# ── semver_cmp: core ordering ────────────────────────────────────────────────
expect_cmp v0.10.0  v0.9.0   1     # the lexical trap
expect_cmp v0.9.0   v0.10.0  -1
expect_cmp v0.93.0  v0.93.0  0
expect_cmp 0.93.0   v0.93.0  0     # leading v optional
expect_cmp v1.0.0   v0.99.99 1
expect_cmp v0.88.0  v0.87.0  1
expect_cmp v0.60.0  v0.93.0  -1    # re-running a superseded tag
expect_cmp v0.76.1  v0.77.0  -1
expect_cmp v0.85.10 v0.85.9  1
expect_cmp v0.08.0  v0.7.0   1     # leading zero is not octal
# ── prereleases ──────────────────────────────────────────────────────────────
expect_cmp v1.2.0-rc1  v1.2.0      -1
expect_cmp v1.2.0      v1.2.0-rc1  1
expect_cmp v1.2.0-rc2  v1.2.0-rc1  1
expect_cmp v1.2.0-rc10 v1.2.0-rc2  1   # natural, not ASCII (documented deviation)
expect_cmp v1.2.0-rc.10 v1.2.0-rc.2 1
expect_cmp v1.2.0-rc1  v1.1.9      1   # core wins over prerelease
expect_cmp v1.2.0-rc9  v1.3.0-rc1  -1
expect_cmp v1.0.0-alpha v1.0.0-alpha.1 -1
expect_cmp v1.0.0-alpha.1 v1.0.0-alpha.beta -1  # numeric < alphanumeric
expect_cmp v1.0.0-beta v1.0.0-alpha 1
expect_cmp v1.0.0-rc1  v1.0.0-rc1  0
expect_cmp v1.0.0+build.5 v1.0.0   0   # build metadata ignored
# ── invalid input ────────────────────────────────────────────────────────────
expect_cmp ""          v1.0.0  invalid
expect_cmp v1.0        v1.0.0  invalid
expect_cmp latest      v1.0.0  invalid
expect_cmp v1.0.0      "v1.0.0; rm -rf /" invalid

# ── pointer_decision ─────────────────────────────────────────────────────────
expect_decision prod  v0.94.0      v0.93.0      advance
expect_decision prod  v0.93.0      v0.93.0      advance  # idempotent re-run of latest
expect_decision prod  v0.60.0      v0.93.0      hold     # backfilling a zero-asset tag
expect_decision prod  v0.9.0       v0.10.0      hold
expect_decision prod  v1.0.0-rc1   v1.0.0       hold
expect_decision prod  v1.0.0       v1.0.0-rc1   advance
expect_decision prod  v0.94.0      ""           advance  # first publish (NoSuchKey)
expect_decision prod  v0.94.0      garbage      advance  # nothing parseable to protect
expect_decision prod  not-a-version v0.93.0     hold
expect_decision stage v0.95.0-rc2  v0.95.0-rc1  advance
expect_decision stage v0.95.0-rc10 v0.95.0-rc9  advance
expect_decision stage v0.94.0-rc3  v0.95.0-rc1  hold
expect_decision stage v0.95.0-rc1  v0.95.0-rc2  hold
expect_decision dev   v0.0.0-dev.aaaaaaa v0.0.0-dev.bbbbbbb advance  # rolling: always newest push
expect_decision dev   v0.0.0-dev.0000000 v9.9.9 advance

if [[ $FAIL -gt 0 ]]; then
  echo "[semver-lib] FAIL: $FAIL of $N cases wrong — publish-s3's stable-pointer guard cannot be trusted." >&2
  exit 1
fi
echo "[semver-lib] OK: $N cases."
