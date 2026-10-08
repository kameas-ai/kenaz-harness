#!/usr/bin/env bash
# semver.sh — SemVer ordering for release-pipeline decisions.
#
# Sourced by .github/workflows/release.yml (publish-s3's stable-pointer guard)
# and self-tested by scripts/ci/check-semver-lib.sh. Pure bash + `sort -V`;
# no repo state, no network, safe to source from anywhere.
#
# WHY THIS EXISTS
# ---------------
# publish-s3 used to overwrite s3://<bucket>/kenaz-harness/manifest.json — the
# stable "latest" pointer read by core/update/manifest.go, the frontend
# update store and the docs download page — on EVERY run, and re-sorted
# index.json with released_at=now. Re-running release.yml against an old tag
# (the remedy check-release-integrity.sh recommends for a zero-asset release)
# would therefore have made that old tag "latest" for every auto-updater.
# The guard needs a version comparison that cannot get this wrong; a lexical
# compare says v0.9.0 > v0.10.0, which is exactly the backwards move.
#
# ORDERING
# --------
# SemVer 2.0.0 §11, with one deliberate deviation:
#   * MAJOR.MINOR.PATCH compare numerically.
#   * A version WITHOUT a prerelease outranks the same core WITH one
#     (v1.2.0-rc3 < v1.2.0).
#   * Prerelease identifiers compare left to right, split on '.'; numeric
#     identifiers numerically, numeric < alphanumeric, a shorter list loses
#     when every shared identifier is equal.
#   * DEVIATION: two alphanumeric identifiers compare in natural (version)
#     order via `sort -V`, not ASCII. Strict SemVer orders rc10 < rc2; this
#     repo's stage channel tags rc1, rc2, ... rc10 without a dot, and the
#     channel must treat rc10 as newer.
#   * Build metadata (+...) is ignored, per §10.
# A leading 'v' is optional.

# semver_valid <version> — exit 0 if parseable.
semver_valid() {
  [[ "$1" =~ ^v?[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z.-]+)?$ ]]
}

# _semver_cmp_ident <a> <b> — echo -1/0/1 for one prerelease identifier.
_semver_cmp_ident() {
  local a="$1" b="$2"
  if [[ "$a" == "$b" ]]; then echo 0; return; fi
  local an=0 bn=0
  [[ "$a" =~ ^[0-9]+$ ]] && an=1
  [[ "$b" =~ ^[0-9]+$ ]] && bn=1
  if (( an && bn )); then
    # Strip leading zeros so bash does not read them as octal.
    local ai=$((10#$a)) bi=$((10#$b))
    if (( ai < bi )); then echo -1; elif (( ai > bi )); then echo 1; else echo 0; fi
    return
  fi
  if (( an )); then echo -1; return; fi
  if (( bn )); then echo 1; return; fi
  local lo
  lo="$(printf '%s\n%s\n' "$a" "$b" | LC_ALL=C sort -V | head -n1)"
  if [[ "$lo" == "$a" ]]; then echo -1; else echo 1; fi
}

# semver_cmp <a> <b> — echo -1 if a<b, 0 if equal, 1 if a>b.
# Returns 2 (echoing nothing) if either side is not a valid version.
semver_cmp() {
  local a="$1" b="$2"
  semver_valid "$a" || return 2
  semver_valid "$b" || return 2
  a="${a#v}"; b="${b#v}"
  a="${a%%+*}"; b="${b%%+*}"

  local a_core="${a%%-*}" b_core="${b%%-*}"
  local a_pre="" b_pre=""
  [[ "$a" == *-* ]] && a_pre="${a#*-}"
  [[ "$b" == *-* ]] && b_pre="${b#*-}"

  local a1 a2 a3 b1 b2 b3
  IFS=. read -r a1 a2 a3 <<<"$a_core"
  IFS=. read -r b1 b2 b3 <<<"$b_core"
  local x y
  for pair in "$a1:$b1" "$a2:$b2" "$a3:$b3"; do
    x=$((10#${pair%%:*})); y=$((10#${pair##*:}))
    if (( x < y )); then echo -1; return 0; fi
    if (( x > y )); then echo 1; return 0; fi
  done

  if [[ -z "$a_pre" && -z "$b_pre" ]]; then echo 0; return 0; fi
  if [[ -z "$a_pre" ]]; then echo 1; return 0; fi
  if [[ -z "$b_pre" ]]; then echo -1; return 0; fi

  local -a ap bp
  IFS=. read -r -a ap <<<"$a_pre"
  IFS=. read -r -a bp <<<"$b_pre"
  local i n=${#ap[@]} r
  (( ${#bp[@]} < n )) && n=${#bp[@]}
  for (( i = 0; i < n; i++ )); do
    r="$(_semver_cmp_ident "${ap[$i]}" "${bp[$i]}")"
    if [[ "$r" != 0 ]]; then echo "$r"; return 0; fi
  done
  if (( ${#ap[@]} < ${#bp[@]} )); then echo -1
  elif (( ${#ap[@]} > ${#bp[@]} )); then echo 1
  else echo 0
  fi
}

# pointer_decision <target_env> <publishing_version> <current_pointer_version>
#
# Echo `advance` if publish-s3 may overwrite the env's canonical
# manifest.json (and insert at the top of index.json), or `hold` if it must
# not. The rules, in order:
#   dev             → advance. The dev channel is a rolling build of main
#                     labelled v0.0.0-dev.<sha>; SHAs carry no order, and
#                     the newest push is by definition the one to serve.
#   current empty   → advance. First publish to this bucket (NoSuchKey).
#   current invalid → advance. Nothing parseable to protect; the caller
#                     warns. (A malformed pointer is itself broken.)
#   new invalid     → hold. Never move a pointer to a version we cannot
#                     order. (derive-version already rejects these for
#                     stage/prod; this is defence in depth.)
#   new < current   → hold. The backwards move this guard exists for.
#   otherwise       → advance (equal = idempotent re-run of latest).
# The same rule serves stage (rc tags) and prod (stable tags) because each
# env has its own bucket, so "current" is always this channel's pointer.
pointer_decision() {
  local env="$1" new="$2" cur="$3"
  if [[ "$env" == dev ]]; then echo advance; return 0; fi
  if [[ -z "$cur" ]]; then echo advance; return 0; fi
  if ! semver_valid "$cur"; then echo advance; return 0; fi
  if ! semver_valid "$new"; then echo hold; return 0; fi
  if [[ "$(semver_cmp "$new" "$cur")" == -1 ]]; then echo hold; else echo advance; fi
}
