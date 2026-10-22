#!/usr/bin/env bash
# go-test-shards.sh — the ONE place pr.yml's Go `-race` shards are defined.
#
# pr.yml's `test-go-shard` matrix job runs, per shard:
#
#   go test $(bash scripts/ci/go-test-shards.sh list <shard>) -race ...
#
# and check-go-test-shards.sh reads the same definitions to prove that the
# shards the matrix actually runs cover every package of the original
# single-job universe (`./core/... ./cmd/... .`) EXACTLY once: no package
# silently dropped, none run twice.
#
# USAGE
#   go-test-shards.sh names           # every defined shard name, one per line
#   go-test-shards.sh universe        # every package in the universe (./rel form)
#   go-test-shards.sh list <shard>    # the packages in <shard> (./rel form)
#
# PATTERNS
#   Repo-relative directories. `core/rpc` matches exactly that package;
#   `core/llm/...` matches core/llm and everything below it (go-style).
#   The shard whose pattern is the single word `REST` takes every universe
#   package no other shard's pattern matches — so a newly added package
#   lands in a shard automatically instead of failing CI. A package matched
#   by two explicit shards is a duplicate and fails the guard.
#
# BALANCING (2026-10-22, by test-function count — wall time per package is
# not recorded anywhere CI can read; rebalance by moving patterns between
# `heavy` and REST, the guard keeps it honest):
#   rpc    core/rpc alone — 553 tests, ~580 s on its own in v0.91.0; it is
#          the critical path whatever else happens, so it runs alone on the
#          8 GB runner.
#   heavy  core/rpc subpackages + core/llm + core/fleet + core/agentgraph
#          (~3,900 tests)
#   rest   everything else (~4,200 tests)
set -euo pipefail
source "$(dirname "${BASH_SOURCE[0]}")/lib/ci-gate.sh"

# Universe: exactly the package patterns the pre-sharding single
# `go test ./core/... ./cmd/... .` step ran.
UNIVERSE_PATTERNS=(./core/... ./cmd/... .)

# Shard definitions: name -> space-separated patterns.
shard_patterns() {
  case "$1" in
    rpc)   echo "core/rpc" ;;
    heavy) echo "core/rpc/middleware/... core/rpc/views/... core/llm/... core/fleet/... core/agentgraph/..." ;;
    rest)  echo "REST" ;;
    *)     return 1 ;;
  esac
}
SHARD_NAMES=(rpc heavy rest)

module_path() {
  awk '$1 == "module" { print $2; exit }' go.mod
}

# universe prints every package as a repo-relative path ("." for the root),
# sorted, one per line. GOWORK=off would break local offline workspaces, so
# it is left to the caller's environment.
universe() {
  local mod
  mod="$(module_path)"
  [[ -n "$mod" ]] || { echo "[go-test-shards] cannot read module path from go.mod" >&2; exit 1; }
  go list "${UNIVERSE_PATTERNS[@]}" \
    | awk -v mod="$mod" '{ if ($0 == mod) print "."; else if (index($0, mod "/") == 1) print substr($0, length(mod) + 2); else { print "[go-test-shards] package outside module: " $0 > "/dev/stderr"; exit 1 } }' \
    | LC_ALL=C sort -u
}

# matches <pkg> <pattern>
matches() {
  local pkg="$1" pat="$2"
  if [[ "$pat" == */... ]]; then
    local base="${pat%/...}"
    [[ "$pkg" == "$base" || "$pkg" == "$base"/* ]]
  else
    [[ "$pkg" == "$pat" ]]
  fi
}

# explicit_match <pkg> — exit 0 when any non-REST shard pattern matches.
explicit_match() {
  local pkg="$1" name pat
  for name in "${SHARD_NAMES[@]}"; do
    for pat in $(shard_patterns "$name"); do
      [[ "$pat" == REST ]] && continue
      matches "$pkg" "$pat" && return 0
    done
  done
  return 1
}

dotslash() {
  if [[ "$1" == "." ]]; then echo "."; else echo "./$1"; fi
}

list_shard() {
  local shard="$1" pats pkg pat out=()
  if ! pats="$(shard_patterns "$shard")"; then
    echo "[go-test-shards] unknown shard '$shard' (defined: ${SHARD_NAMES[*]})" >&2
    exit 2
  fi
  while IFS= read -r pkg; do
    if [[ "$pats" == REST ]]; then
      explicit_match "$pkg" || out+=("$(dotslash "$pkg")")
      continue
    fi
    for pat in $pats; do
      if matches "$pkg" "$pat"; then
        out+=("$(dotslash "$pkg")")
        break
      fi
    done
  done < <(universe)
  # An empty list is never acceptable: `go test` with no package arguments
  # tests the CURRENT directory, which would turn a mis-defined shard into
  # a green job that ran one unrelated package.
  if [[ ${#out[@]} -eq 0 ]]; then
    echo "[go-test-shards] shard '$shard' matches no packages — a shard must not be empty" >&2
    exit 1
  fi
  printf '%s\n' "${out[@]}"
}

case "${1:-}" in
  names)    printf '%s\n' "${SHARD_NAMES[@]}" ;;
  universe) universe | while IFS= read -r p; do dotslash "$p"; done ;;
  list)     [[ $# -eq 2 ]] || { echo "usage: $0 list <shard>" >&2; exit 2; }; list_shard "$2" ;;
  *)        echo "usage: $0 names | universe | list <shard>" >&2; exit 2 ;;
esac
