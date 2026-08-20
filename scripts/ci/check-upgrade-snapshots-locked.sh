#!/usr/bin/env bash
# check-upgrade-snapshots-locked.sh — CI gate for
# upgrade-path-coverage-01PMUG01 spec.md §4 ("Snapshots for released
# tags are immutable") / §6.2.
#
# RULE: any file under core/storage/sqlite/testdata/upgrade/<tag>/ for a
# <tag> that is <= the latest release tag AND already present at the
# merge-base with the target branch, must be byte-identical to its
# merge-base content. A snapshot directory that does not exist yet at
# the merge-base (this is its first commit) is not a violation — it is
# how the chain grows. Once merged, any later PR editing it is.
#
# WHY THIS SHAPE, NOT A COMMITTED HASH FILE (contrast check-codegen.sh's
# wailsjs gate): a hash file needs updating by hand on every legitimate
# new-tag commit, which is exactly the workflow scripts/ci/upgrade-
# snapshot.sh already produces byte-identical output for — an extra
# hash-file dance would just be one more thing to forget. Comparing
# against git history directly means "add a new snapshot" needs no gate
# maintenance at all, and "edit an old one" is caught unconditionally.
#
# Usage: bash scripts/ci/check-upgrade-snapshots-locked.sh
#
# Exit codes: 0 clean; 1 a locked snapshot changed or the base ref could
# not be resolved (never a silent pass — an unresolvable base ref is a
# gate that cannot look at anything, which is a failure, not clean).

set -euo pipefail

source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib/ci-gate.sh"

GATE="[upgrade-snapshots-locked]"
SNAP_ROOT="core/storage/sqlite/testdata/upgrade"

ci_require_dir "$SNAP_ROOT" "$GATE"

# Resolve a base ref to diff against. Prefer origin/main (the real
# target-branch tip in CI); fall back to a local main branch for a dev
# checkout that has no origin remote fetched. Failing to resolve either
# is a gate failure, not a pass — see header.
#
# UPGRADE_SNAPSHOTS_BASE_REF overrides the candidate list. This is not a
# suppression knob — it cannot make a real violation pass — it only
# selects WHICH commit counts as "already shipped" history. Used by
# scripts/ci/gates_can_fail_test.go's planted-violation case: by the
# time that test runs, this mission's own commit has already put
# v0.63.0/dump.sql into HEAD's history, so pointing the base ref at
# HEAD lets the test mutate the working tree and observe a REAL git-
# diff-driven failure, not a mocked one.
BASE_REF="${UPGRADE_SNAPSHOTS_BASE_REF:-}"
if [[ -z "$BASE_REF" ]]; then
  for candidate in origin/main main; do
    if git rev-parse -q --verify "$candidate" >/dev/null 2>&1; then
      BASE_REF="$candidate"
      break
    fi
  done
elif ! git rev-parse -q --verify "$BASE_REF" >/dev/null 2>&1; then
  echo "${GATE} FAIL: UPGRADE_SNAPSHOTS_BASE_REF=${BASE_REF} does not resolve." >&2
  exit 1
fi
if [[ -z "$BASE_REF" ]]; then
  echo "${GATE} FAIL: could not resolve a base ref (tried origin/main, main)." >&2
  echo "${GATE} A gate that cannot find its comparison base cannot verify anything." >&2
  echo "${GATE} In CI, ensure the checkout step fetches the target branch." >&2
  exit 1
fi

MERGE_BASE="$(git merge-base HEAD "$BASE_REF" 2>/dev/null || true)"
if [[ -z "$MERGE_BASE" ]]; then
  echo "${GATE} FAIL: 'git merge-base HEAD ${BASE_REF}' found no common ancestor." >&2
  exit 1
fi

# Latest release tag: highest vX.Y.Z (no -rc suffix) tag reachable from
# the base ref, so a checkout of a topic branch still resolves the
# same "latest shipped" answer a checkout of main would.
#
# UPGRADE_SNAPSHOTS_EXPECT_TAG overrides this COMPUTED value — not a
# separate code path. It exists so gates_can_fail_test.go's planted-
# violation case can substitute a tag with no snapshot directory and
# observe the SAME missing-snapshot comparison below fail on it, rather
# than exercising a parallel branch nothing else runs. Mirrors the
# UPGRADE_SNAPSHOTS_BASE_REF precedent above.
LATEST_TAG="$(git tag --list 'v[0-9]*.[0-9]*.[0-9]*' --merged "$MERGE_BASE" 2>/dev/null \
  | grep -Ev -- '-rc' | sort -V | tail -1 || true)"
if [[ -n "${UPGRADE_SNAPSHOTS_EXPECT_TAG:-}" ]]; then
  LATEST_TAG="$UPGRADE_SNAPSHOTS_EXPECT_TAG"
fi

fail=0
checked=0

semver_le() {
  # returns 0 (true) if $1 <= $2, both "vX.Y.Z"
  [[ "$(printf '%s\n%s\n' "$1" "$2" | sort -V | tail -1)" == "$2" ]]
}

for dir in "$SNAP_ROOT"/v*/; do
  [[ -d "$dir" ]] || continue
  tag="$(basename "$dir")"

  if [[ -n "$LATEST_TAG" ]] && ! semver_le "$tag" "$LATEST_TAG"; then
    # A snapshot for a tag ahead of the latest release (e.g. a snapshot
    # being prepared for the release this branch is cutting) is not
    # locked yet — it hasn't shipped.
    continue
  fi

  # Does this directory exist at the merge-base? If not, it is new in
  # this branch — not a violation.
  if ! git cat-file -e "${MERGE_BASE}:${dir%/}" 2>/dev/null; then
    continue
  fi

  while IFS= read -r -d '' f; do
    rel="${f#./}"
    checked=$((checked + 1))
    base_content=""
    if ! base_content="$(git show "${MERGE_BASE}:${rel}" 2>/dev/null)"; then
      echo "${GATE} FAIL: ${rel} exists now but not at merge-base ${MERGE_BASE} inside an already-locked directory ${dir} — a file was ADDED to a locked snapshot." >&2
      fail=1
      continue
    fi
    current_content="$(cat "$f")"
    if [[ "$base_content" != "$current_content" ]]; then
      echo "${GATE} FAIL: ${rel} differs from its merge-base (${MERGE_BASE}) content." >&2
      echo "${GATE}   Snapshot directories for already-released tags are immutable (spec §4)." >&2
      echo "${GATE}   diff:" >&2
      diff <(printf '%s' "$base_content") <(printf '%s' "$current_content") | head -20 >&2 || true
      fail=1
    fi
  done < <(find "$dir" -type f -print0)

  # Deletions: a file present at merge-base but missing now. Plain
  # newline-delimited (not -z/NUL) for portability — GNU sed's -z is
  # unavailable on the BSD sed shipped with macOS dev machines, and
  # snapshot filenames never contain newlines.
  while IFS= read -r leaf; do
    [[ -z "$leaf" ]] && continue
    rel="${dir}${leaf}"
    [[ -f "$rel" ]] && continue
    echo "${GATE} FAIL: ${rel} existed in the locked snapshot at merge-base ${MERGE_BASE} and is missing now." >&2
    fail=1
  done < <(git ls-tree -r --name-only "${MERGE_BASE}:${dir%/}" 2>/dev/null)
done

if [[ "$fail" -ne 0 ]]; then
  echo "" >&2
  echo "${GATE} FAIL — see violations above. Regenerating with scripts/ci/upgrade-snapshot.sh" >&2
  echo "${GATE} does not fix this: a locked snapshot that regenerates differently is itself a finding (spec §6.2)." >&2
  exit 1
fi

# Missing-snapshot check — entry-points-and-crash-reporting-01PMZD13
# UNIT-3 (owner task #38). Everything above catches a locked snapshot
# being EDITED; nothing above can see one being ABSENT, because the lock
# loop only ever iterates directories that exist ("for dir in
# $SNAP_ROOT/v*/"). That let the chain fall a full release behind twice in
# a row (v0.63.2 and v0.64.0 each shipped without their own snapshot ever
# landing in a commit) with this gate green throughout — a hole nothing
# else in CI or in TestUpgradePath (core/storage/sqlite/upgrade_path_test.go,
# which also only iterates directories present under $SNAP_ROOT) could see
# either.
#
# Deliberately does NOT reuse the ":92 exemption" logic above — that
# exemption is for a snapshot AHEAD of LATEST_TAG (a release branch
# staging its own snapshot before its tag exists), which must stay legal.
# This check is the opposite direction: LATEST_TAG itself has no snapshot
# at all.
if [[ -n "$LATEST_TAG" ]] && [[ ! -f "${SNAP_ROOT}/${LATEST_TAG}/dump.sql" ]]; then
  echo "${GATE} FAIL: the latest release tag (${LATEST_TAG}) has no snapshot at ${SNAP_ROOT}/${LATEST_TAG}/dump.sql." >&2
  echo "${GATE} Every release must commit its own snapshot (bash scripts/ci/upgrade-snapshot.sh ${LATEST_TAG})" >&2
  echo "${GATE} plus a hand-written PROVENANCE.md — the script writes dump.sql only." >&2
  exit 1
fi

echo "${GATE} clean — checked ${checked} file(s) across locked snapshot directories (base ${MERGE_BASE}, latest release ${LATEST_TAG:-none})."
