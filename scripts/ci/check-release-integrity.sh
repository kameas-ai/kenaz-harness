#!/usr/bin/env bash
#
# check-release-integrity.sh — reconcile git tags against published GitHub
# Releases, and fail loudly when a tag has no shippable artifacts.
#
# WHY THIS EXISTS
# ---------------
# `tag-on-merge.yml` pushes the SemVer tag, creates the GitHub Release, and
# then fire-and-forgets `gh workflow run release.yml` to build and upload the
# artifacts. It exits green the moment the tag is pushed — it never learns
# whether the artifacts landed. So when release.yml fails, what is left behind
# is a Release row with ZERO assets, which is indistinguishable from a healthy
# release in the releases list, behind a green checkmark on main.
#
# That is exactly what happened to v0.56.0 and v0.57.0, and — in the sibling
# repo kameas-ai/kenaz, where tag-on-merge does not create the Release at all —
# to eleven tags across four days (v1.20.0 … v1.30.0). Both were found by
# accident. Nothing in either repo compared "tags we cut" against "artifacts
# users can download", so silence read as success. This script is that
# comparison.
#
# WHAT COUNTS AS HEALTHY
# ----------------------
# For every `vX.Y.Z` tag (strict SemVer; legacy/`-rc`/`archive/*` tags are out
# of scope) there must exist a GitHub Release that is:
#   * present,
#   * NOT a draft (a draft Release is invisible to downloaders), and
#   * carrying at least one asset.
#
# The asset check is the important half: a Release row with zero assets is
# exactly what a partially-failed publish looks like, and it is indistinguishable
# from a healthy one in the web UI's release list.
#
# GRACE PERIOD
# ------------
# A tag younger than GRACE_MINUTES is not failed on its own — the release build
# is cross-platform and signed, and legitimately takes the better part of an
# hour. But grace only protects builds that are still *running*: if the Release
# workflow run for that tag has already completed with a non-success conclusion,
# the tag is failed immediately regardless of age. A known-failed release is not
# an in-flight release.
#
# PERMANENTLY-EMPTY TAGS
# ----------------------
# Some tags will never have a Release — they were burned by pipeline iterations
# or lost to the outage above. They are listed, one per line with a reason, in
# the ignore file. That file is the permanent record; it is deliberately NOT a
# "floor tag" cutoff, because a floor would silently swallow every gap beneath
# it and the whole point here is that nothing gets swallowed silently.
#
# USAGE
#   REPO=kameas-ai/kenaz-harness ./scripts/ci/check-release-integrity.sh
#
# ENV
#   REPO            owner/name (default: $GITHUB_REPOSITORY)
#   IGNORE_FILE     path to the ignore list (default: .github/release-integrity-ignore.txt)
#   GRACE_MINUTES   age below which an in-flight release is not failed (default: 90)
#   RELEASE_WORKFLOW  release workflow file name (default: release.yml)
#
# EXIT
#   0 — every tag reconciles (or is ignored / within grace)
#   1 — at least one tag has no downloadable release
#   2 — usage / tooling error

set -euo pipefail

REPO="${REPO:-${GITHUB_REPOSITORY:-}}"
IGNORE_FILE="${IGNORE_FILE:-.github/release-integrity-ignore.txt}"
GRACE_MINUTES="${GRACE_MINUTES:-90}"
RELEASE_WORKFLOW="${RELEASE_WORKFLOW:-release.yml}"

if [ -z "$REPO" ]; then
  echo "::error::REPO (or GITHUB_REPOSITORY) must be set" >&2
  exit 2
fi
if ! command -v gh >/dev/null 2>&1; then
  echo "::error::gh CLI not found on PATH" >&2
  exit 2
fi

SEMVER_RE='^v[0-9]+\.[0-9]+\.[0-9]+$'
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

# ── portable ISO-8601 → epoch ────────────────────────────────────────────────
# GNU date on the CI runner, BSD date when a human runs this on macOS.
iso_to_epoch() {
  local iso="$1"
  date -u -d "$iso" +%s 2>/dev/null \
    || date -u -j -f "%Y-%m-%dT%H:%M:%SZ" "$iso" +%s 2>/dev/null \
    || echo 0
}

# ── 1. every strict-SemVer tag on the remote ─────────────────────────────────
#
# Fetch and filter in two steps, on purpose. As one pipeline ending in
# `|| true`, a `gh api` failure (rate limit, expired token, network) produced
# an empty tags.txt, which produced an empty missing.txt, which produced
# "✅ Every SemVer tag reconciles" and exit 0. Verified: with gh stubbed to
# exit 1 the old script printed "OK: all 0 SemVer tags reconcile". The gate
# whose entire purpose is to make silence audible was itself silent when its
# data source went away.
#
# The `|| true` is still needed on the *filter* — grep exits 1 on no match —
# but the *fetch* must be allowed to fail loudly.
if ! gh api "repos/$REPO/git/refs/tags" --paginate --jq '.[].ref' > "$WORK/tags.raw" 2>"$WORK/tags.err"; then
  echo "::error::could not list tags for $REPO — the reconciliation cannot run" >&2
  sed 's/^/  /' "$WORK/tags.err" >&2 || true
  exit 2
fi
sed 's|^refs/tags/||' "$WORK/tags.raw" | grep -E "$SEMVER_RE" | sort > "$WORK/tags.txt" || true

# Zero SemVer tags in a repo that releases on every merge means the query
# succeeded but returned something unexpected. Reconciling zero tags against
# zero releases is a vacuous pass, so refuse it.
if [ ! -s "$WORK/tags.txt" ]; then
  echo "::error::$REPO has no vX.Y.Z tags at all — refusing to report 'all tags reconcile' over an empty set" >&2
  echo "If this repo genuinely has no releases yet, remove this workflow until it does." >&2
  exit 2
fi

# ── 2. every release, as `tag<TAB>draft<TAB>asset_count` ─────────────────────
# The list endpoint is the only one that returns drafts — `releases/tags/<tag>`
# 404s on a draft, so a per-tag lookup would misreport a draft as "no release".
#
# Failing this fetch is less dangerous than failing the tag fetch — an empty
# releases.tsv makes every tag look unreleased, which is loud rather than
# silent. But it is loud in the wrong way: a hundred fabricated "no release
# exists" errors would train the reader to ignore this workflow. Fail with the
# real reason instead.
if ! gh api "repos/$REPO/releases?per_page=100" --paginate \
     --jq '.[] | "\(.tag_name)\t\(.draft)\t\(.assets | length)"' > "$WORK/releases.raw" 2>"$WORK/releases.err"; then
  echo "::error::could not list releases for $REPO — the reconciliation cannot run" >&2
  sed 's/^/  /' "$WORK/releases.err" >&2 || true
  exit 2
fi
sort "$WORK/releases.raw" > "$WORK/releases.tsv"

# Downloadable == published (not a draft) AND carrying at least one asset.
awk -F'\t' '$2 == "false" && $3 > 0 { print $1 }' "$WORK/releases.tsv" \
  | sort > "$WORK/published.txt"

# ── 3. the ignore list ───────────────────────────────────────────────────────
: > "$WORK/ignored.txt"
if [ -f "$IGNORE_FILE" ]; then
  sed -e 's/#.*$//' -e 's/[[:space:]]*$//' "$IGNORE_FILE" \
    | grep -E "$SEMVER_RE" \
    | sort -u > "$WORK/ignored.txt" || true
fi

TAG_COUNT=$(wc -l < "$WORK/tags.txt" | tr -d ' ')
PUB_COUNT=$(wc -l < "$WORK/published.txt" | tr -d ' ')
IGN_COUNT=$(wc -l < "$WORK/ignored.txt" | tr -d ' ')

echo "repo=$REPO semver_tags=$TAG_COUNT downloadable_releases=$PUB_COUNT ignored=$IGN_COUNT grace=${GRACE_MINUTES}m"

comm -23 "$WORK/tags.txt" "$WORK/published.txt" > "$WORK/missing_all.txt"
comm -23 "$WORK/missing_all.txt" "$WORK/ignored.txt" > "$WORK/missing.txt"

# ── 4. hygiene notices on the ignore list (never fatal) ──────────────────────
# An ignore entry that has since been published, or that names a tag which
# does not exist, is stale — say so, so the list can be pruned instead of
# quietly growing forever.
while IFS= read -r tag; do
  [ -n "$tag" ] || continue
  if grep -qxF "$tag" "$WORK/published.txt"; then
    echo "::notice::ignore-list entry $tag now has a published release with assets — prune it from $IGNORE_FILE"
  elif ! grep -qxF "$tag" "$WORK/tags.txt"; then
    echo "::notice::ignore-list entry $tag is not a tag in this repo — prune it from $IGNORE_FILE"
  fi
done < "$WORK/ignored.txt"

# ── 5. classify each unreconciled tag ────────────────────────────────────────
NOW=$(date -u +%s)
FAILED=0
PENDING=0
: > "$WORK/report.md"

while IFS= read -r tag; do
  [ -n "$tag" ] || continue

  # What does exist, if anything? Distinguishes the three failure shapes:
  # no Release at all / a draft Release / a published Release with no assets.
  rel="$(awk -F'\t' -v t="$tag" '$1 == t { print $2 "\t" $3; exit }' "$WORK/releases.tsv")"
  rel_draft="${rel%%$'\t'*}"
  rel_assets="${rel##*$'\t'}"
  if [ -z "$rel" ]; then
    detail="no GitHub Release exists for this tag"
  elif [ "$rel_draft" = "true" ]; then
    detail="Release exists but is a DRAFT — invisible to downloaders (assets=$rel_assets)"
  else
    detail="Release exists but has ZERO assets — this is what a partially-failed publish looks like"
  fi

  # Tag age. Annotated tags carry a tagger date; lightweight tags fall back to
  # the commit's committer date.
  obj_type="$(gh api "repos/$REPO/git/ref/tags/$tag" --jq '.object.type' 2>/dev/null || echo '')"
  obj_sha="$(gh api "repos/$REPO/git/ref/tags/$tag" --jq '.object.sha' 2>/dev/null || echo '')"
  tag_date=''
  if [ "$obj_type" = "tag" ] && [ -n "$obj_sha" ]; then
    tag_date="$(gh api "repos/$REPO/git/tags/$obj_sha" --jq '.tagger.date' 2>/dev/null || echo '')"
  elif [ -n "$obj_sha" ]; then
    tag_date="$(gh api "repos/$REPO/commits/$obj_sha" --jq '.commit.committer.date' 2>/dev/null || echo '')"
  fi
  tag_epoch=0
  [ -n "$tag_date" ] && tag_epoch="$(iso_to_epoch "$tag_date")"
  age_min=999999
  if [ "$tag_epoch" -gt 0 ]; then
    age_min=$(( (NOW - tag_epoch) / 60 ))
  fi

  # Is the release build for this tag still running? tag-on-merge dispatches
  # release.yml with --ref <tag>, so head_branch is the tag name.
  run_state="$(gh api "repos/$REPO/actions/workflows/$RELEASE_WORKFLOW/runs?per_page=100" \
    --jq "[.workflow_runs[] | select(.head_branch == \"$tag\")] | sort_by(.created_at) | last | \"\(.status)/\(.conclusion)\"" \
    2>/dev/null || echo '')"
  case "$run_state" in
    ''|null|null/null) run_state='none/none' ;;
  esac

  in_flight=false
  case "$run_state" in
    queued/*|in_progress/*|requested/*|waiting/*|pending/*) in_flight=true ;;
  esac

  if [ "$age_min" -lt "$GRACE_MINUTES" ] && [ "$in_flight" = true ]; then
    PENDING=$((PENDING + 1))
    echo "::notice::$tag — release still in flight (${age_min}m old, run=$run_state); within ${GRACE_MINUTES}m grace"
    printf -- '- ⏳ `%s` — in flight (%sm old, run=`%s`)\n' "$tag" "$age_min" "$run_state" >> "$WORK/report.md"
    continue
  fi

  if [ "$age_min" -lt "$GRACE_MINUTES" ] && [ "$run_state" = "none/none" ]; then
    # No run yet — the dispatch may not have registered. Still inside grace.
    PENDING=$((PENDING + 1))
    echo "::notice::$tag — no release run found yet (${age_min}m old); within ${GRACE_MINUTES}m grace"
    printf -- '- ⏳ `%s` — awaiting release run (%sm old)\n' "$tag" "$age_min" >> "$WORK/report.md"
    continue
  fi

  # Everything else is a real gap: either past grace, or the release run has
  # already concluded unsuccessfully (grace does not protect a known failure).
  FAILED=$((FAILED + 1))
  echo "::error title=Tag without a release::$tag — $detail (tag is ${age_min}m old, release run=$run_state)"
  printf -- '- ❌ `%s` — %s _(age %sm, run `%s`)_\n' "$tag" "$detail" "$age_min" "$run_state" >> "$WORK/report.md"
done < "$WORK/missing.txt"

# ── 6. summary ───────────────────────────────────────────────────────────────
{
  echo "## Release integrity — \`$REPO\`"
  echo
  echo "| | |"
  echo "|---|---|"
  echo "| SemVer tags | $TAG_COUNT |"
  echo "| Tags with a downloadable release | $PUB_COUNT |"
  echo "| Known-empty (ignore list) | $IGN_COUNT |"
  echo "| In flight (within ${GRACE_MINUTES}m grace) | $PENDING |"
  echo "| **Unreleased** | **$FAILED** |"
  echo
  if [ -s "$WORK/report.md" ]; then
    cat "$WORK/report.md"
    echo
  fi
  if [ "$FAILED" -gt 0 ]; then
    echo "A tag with no downloadable release means users cannot install that version."
    echo "Re-run \`release.yml\` against the tag, or — if the tag is permanently"
    echo "unreleasable — add it to \`$IGNORE_FILE\` with a one-line reason."
  else
    echo "✅ Every SemVer tag reconciles to a published release with assets."
  fi
} >> "${GITHUB_STEP_SUMMARY:-/dev/stdout}"

if [ "$FAILED" -gt 0 ]; then
  echo "FAIL: $FAILED tag(s) have no downloadable release." >&2
  exit 1
fi
echo "OK: all $TAG_COUNT SemVer tags reconcile ($IGN_COUNT ignored, $PENDING in flight)."
