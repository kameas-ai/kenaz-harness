#!/usr/bin/env bash
# check-go-test-shards.sh — the sharded `-race` suite runs every package
# exactly once.
#
# THE DEFECT CLASS
# ----------------
# pr.yml used to run the whole Go race suite as ONE step:
#
#   go test ./core/... ./cmd/... . -race -count=1 -short -timeout 20m
#
# It is now split across the `test-go-shard` matrix (definitions in
# go-test-shards.sh). A sharded suite has two silent failure modes the
# old single step could not have:
#
#   GAP        a package no shard runs — e.g. a shard dropped from the
#              matrix, or a pattern edited so a subtree falls between two
#              shards. CI stays green; the package's tests never run.
#   DUPLICATE  a package two shards run — wasted runner time, and on a
#              4 GB runner the extra -race binary is OOM pressure.
#
# WHAT THIS CHECKS
# ----------------
#   1. The shard names pr.yml's matrix runs (`- shard: <name>` lines) are
#      all defined in go-test-shards.sh, with no name repeated.
#   2. pr.yml's shard step actually feeds `go-test-shards.sh list` into
#      `go test` (a matrix that runs something else is not a shard).
#   3. Over the shards the MATRIX runs (not merely the ones defined), every
#      package `go list ./core/... ./cmd/... .` reports appears exactly once.
#
# Needs a Go toolchain and module cache (lint-go has both) and, like every
# `go list` of this module, frontend/dist to exist for the embed.
#
# Planted-violation proofs: TestGoTestShardsGate_Planted* in
# gates_can_fail_test.go.
set -euo pipefail
source "$(dirname "${BASH_SOURCE[0]}")/lib/ci-gate.sh"

P="[go-test-shards]"
WF=.github/workflows/pr.yml
LISTER=scripts/ci/go-test-shards.sh
ci_require_file "$WF" "$P"
ci_require_file "$LISTER" "$P"

fail=0
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

# 1. Matrix shard names.
grep -E '^[[:space:]]*-[[:space:]]+shard:[[:space:]]*[A-Za-z0-9_-]+[[:space:]]*$' "$WF" \
  | sed -E 's/^[[:space:]]*-[[:space:]]+shard:[[:space:]]*//; s/[[:space:]]*$//' > "$tmp/matrix"
if [[ ! -s "$tmp/matrix" ]]; then
  echo "$P FAIL: no '- shard: <name>' matrix entries found in $WF — the guard has nothing to check." >&2
  exit 1
fi
bash "$LISTER" names > "$tmp/defined"

dups="$(LC_ALL=C sort "$tmp/matrix" | uniq -d)"
if [[ -n "$dups" ]]; then
  echo "$P FAIL: shard(s) listed more than once in the $WF matrix: $(echo $dups)" >&2
  fail=1
fi
while IFS= read -r s; do
  if ! grep -qxF "$s" "$tmp/defined"; then
    echo "$P FAIL: $WF matrix runs shard '$s', which $LISTER does not define." >&2
    fail=1
  fi
done < "$tmp/matrix"

# 2. The matrix step uses the lister.
if ! grep -qF 'scripts/ci/go-test-shards.sh list "${{ matrix.shard }}"' "$WF"; then
  echo "$P FAIL: $WF no longer runs 'scripts/ci/go-test-shards.sh list \"\${{ matrix.shard }}\"'" >&2
  echo "$P the shard job must take its package list from the lister this guard verifies." >&2
  fail=1
fi
[[ $fail -eq 0 ]] || exit 1

# 3. Exact-once coverage over the matrix shards.
bash "$LISTER" universe > "$tmp/universe"
if [[ ! -s "$tmp/universe" ]]; then
  echo "$P FAIL: go list returned no packages — refusing to report clean." >&2
  exit 1
fi
: > "$tmp/covered"
while IFS= read -r s; do
  bash "$LISTER" list "$s" | sed "s|\$|	$s|" >> "$tmp/covered"
done < "$tmp/matrix"

cut -f1 "$tmp/covered" | LC_ALL=C sort > "$tmp/covered.pkgs"
gaps="$(LC_ALL=C comm -23 "$tmp/universe" <(LC_ALL=C sort -u "$tmp/covered.pkgs"))"
dupe_pkgs="$(uniq -d "$tmp/covered.pkgs")"
extra="$(LC_ALL=C comm -13 "$tmp/universe" <(LC_ALL=C sort -u "$tmp/covered.pkgs"))"

if [[ -n "$gaps" ]]; then
  echo "$P FAIL: package(s) no matrix shard runs (GAP):" >&2
  echo "$gaps" | sed 's/^/  /' >&2
  fail=1
fi
if [[ -n "$dupe_pkgs" ]]; then
  echo "$P FAIL: package(s) run by more than one shard (DUPLICATE):" >&2
  while IFS= read -r p; do
    echo "  $p  <- $(awk -F'\t' -v p="$p" '$1 == p { print $2 }' "$tmp/covered" | paste -sd, -)" >&2
  done <<< "$dupe_pkgs"
  fail=1
fi
if [[ -n "$extra" ]]; then
  echo "$P FAIL: shard(s) list package(s) outside the universe:" >&2
  echo "$extra" | sed 's/^/  /' >&2
  fail=1
fi
[[ $fail -eq 0 ]] || exit 1

echo "$P clean — $(wc -l < "$tmp/universe" | tr -d ' ') packages, each in exactly one of: $(paste -sd' ' "$tmp/matrix")."
