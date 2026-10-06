#!/usr/bin/env bash
# check-upgrade-snapshot-present.sh — the ABSENCE gate for the upgrade
# snapshot chain.
#
# RULE: the newest snapshot directory under
# core/storage/sqlite/testdata/upgrade/ must not be older than the newest
# release tag reachable in this checkout.
#
# WHY THIS EXISTS, AND WHY IT IS A SEPARATE GATE FROM THE LOCK GATE:
# check-upgrade-snapshots-locked.sh catches MODIFICATION of a committed
# snapshot and structurally cannot see ABSENCE. Nothing caught absence,
# and the hole opened on three consecutive releases:
#
#   v0.63.2  — PROVENANCE.md recorded it in its own words: "the lock gate
#              catches modification but nothing catches absence."
#   v0.64.0  — the release whose PR title claims "CI can finally see an
#              upgrade" shipped with no snapshot of its own. From tagging
#              until it was backfilled by hand, TestUpgradePath covered
#              nothing newer than v0.63.2 and passed the whole time.
#   v0.64.1  — open at the time this gate was written. Latest tag
#              v0.64.1, latest snapshot v0.64.0.
#
# That is the failure mode this gate exists for: TestUpgradePath keeps
# passing, it just silently stops covering anything new. A green suite is
# not evidence that the newest release is covered.
#
# WHY COMPARE AGAINST TAGS RATHER THAN A COMMITTED EXPECTED-VERSION FILE:
# same reasoning as the lock gate's header — a hand-maintained file is one
# more thing to forget, and forgetting is precisely the defect. Tags are
# the ground truth for "what has shipped".
#
# REQUIRES TAGS. A checkout with no release tags cannot answer the
# question this gate asks. That is a gate that cannot look at anything,
# so it FAILS rather than passing silently — the same rule the lock gate
# applies to an unresolvable base ref. In CI, ensure tags are fetched
# (actions/checkout with fetch-depth: 0, or an explicit `git fetch --tags`).
#
# UPGRADE_SNAPSHOT_PRESENT_MAX_TAG overrides the detected newest tag.
# This is NOT a suppression knob — it cannot make a real violation pass —
# it only selects which tag counts as "newest shipped", so that
# scripts/ci/gates_can_fail_test.go can plant a violation deterministically
# without creating real tags in the test repo.
#
# Usage: bash scripts/ci/check-upgrade-snapshot-present.sh
#
# Exit codes: 0 the chain reaches the newest release tag; 1 it does not,
# or the question could not be answered.

set -euo pipefail

source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib/ci-gate.sh"

GATE="[upgrade-snapshot-present]"
SNAP_ROOT="core/storage/sqlite/testdata/upgrade"

ci_require_dir "$SNAP_ROOT" "$GATE"

# --- newest release tag -------------------------------------------------
# Release candidates (v1.2.0-rc1) are deliberately EXCLUDED: an RC is a
# soak build, not a shipped release, and does not owe a snapshot. Only
# stable vX.Y.Z tags do.
MAX_TAG="${UPGRADE_SNAPSHOT_PRESENT_MAX_TAG:-}"
if [[ -z "$MAX_TAG" ]]; then
  MAX_TAG="$(git tag -l 'v*' \
    | grep -E '^v[0-9]+\.[0-9]+\.[0-9]+$' \
    | sort -V \
    | tail -1 || true)"
fi

if [[ -z "$MAX_TAG" ]]; then
  echo "${GATE} FAIL: no stable release tag (vX.Y.Z) is reachable in this checkout." >&2
  echo "${GATE}   This gate compares the snapshot chain against shipped tags; with no" >&2
  echo "${GATE}   tags it cannot look at anything, so it fails rather than passing." >&2
  echo "${GATE}   In CI, fetch tags (actions/checkout fetch-depth: 0, or git fetch --tags)." >&2
  exit 1
fi

# --- completeness: a snapshot directory must carry dump.sql ------------
# A directory is not a snapshot; its dump.sql is. v0.89.4 shipped
# (PR #382) as a PROVENANCE.md-only directory — no dump was ever
# committed — and this gate, keying on the DIRECTORY name alone,
# reported the chain level at v0.89.4 while TestUpgradePath silently
# skipped the dump-less directory. The chain actually stopped at
# v0.89.3, so the newest release's upgrade path was untested while
# everything stayed green: exactly the failure mode this gate exists
# for, smuggled past it by an empty-handed directory. Caught by the
# v0.90.0 release review (2026-10-06). A dump-less directory at ANY
# position in the chain is a half-executed ritual, so it fails
# outright rather than merely not counting as newest.
SNAP_NAMES="$(find "$SNAP_ROOT" -mindepth 1 -maxdepth 1 -type d -name 'v*' -exec basename {} \; \
  | grep -E '^v[0-9]+\.[0-9]+\.[0-9]+$' \
  | sort -V || true)"

INCOMPLETE=0
while IFS= read -r name; do
  [[ -z "$name" ]] && continue
  if [[ ! -f "$SNAP_ROOT/$name/dump.sql" ]]; then
    echo "${GATE} FAIL: ${SNAP_ROOT}/${name}/ has no dump.sql — a provenance-only directory is not a snapshot." >&2
    INCOMPLETE=1
  fi
done <<< "$SNAP_NAMES"
if [[ "$INCOMPLETE" -ne 0 ]]; then
  echo "${GATE}   Run: bash scripts/ci/upgrade-snapshot.sh <tag> to produce the missing dump.sql," >&2
  echo "${GATE}   or delete the directory if the tag genuinely owes no snapshot (it does not:" >&2
  echo "${GATE}   every stable tag owes one — see the release ritual in CLAUDE.md)." >&2
  exit 1
fi

# --- newest committed snapshot -----------------------------------------
# Every surviving candidate carries dump.sql (the completeness check
# above exits otherwise), so the newest directory name is now also the
# newest REPLAYABLE snapshot.
MAX_SNAP="$(printf '%s\n' "$SNAP_NAMES" | tail -1 || true)"

if [[ -z "$MAX_SNAP" ]]; then
  echo "${GATE} FAIL: no snapshot directories under ${SNAP_ROOT}/." >&2
  echo "${GATE}   Expected at least one vX.Y.Z/ directory containing dump.sql." >&2
  exit 1
fi

# --- the comparison -----------------------------------------------------
# sort -V puts the older version first. If the newest snapshot sorts
# strictly before the newest tag, the chain is behind.
NEWEST="$(printf '%s\n%s\n' "$MAX_TAG" "$MAX_SNAP" | sort -V | tail -1)"

if [[ "$MAX_SNAP" != "$MAX_TAG" && "$NEWEST" == "$MAX_TAG" ]]; then
  cat >&2 <<MSG
${GATE} FAIL: the upgrade-snapshot chain is behind the newest release tag.

  newest release tag: ${MAX_TAG}
  newest snapshot:    ${MAX_SNAP}

  TestUpgradePath will keep passing. It just covers nothing newer than
  ${MAX_SNAP}, so every user upgrading from ${MAX_TAG} traverses a migration
  path no test has ever exercised. That is not a hypothetical: v0.63.0
  shipped a P0 that made every upgraded install unable to list, open or
  create a session, and the entire pre-existing suite passed with the bug
  present.

  Fix (the release ritual, per CLAUDE.md):

    bash scripts/ci/upgrade-snapshot.sh ${MAX_TAG}
    # then hand-write core/storage/sqlite/testdata/upgrade/${MAX_TAG}/PROVENANCE.md
    # (the script writes only dump.sql — every provenance file to date was
    #  authored by hand, which is one reason this ritual keeps being
    #  half-executed)
    git add core/storage/sqlite/testdata/upgrade/${MAX_TAG}/
MSG
  exit 1
fi

echo "${GATE} OK — snapshot chain reaches ${MAX_SNAP} (newest release tag ${MAX_TAG})."
