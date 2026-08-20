#!/usr/bin/env bash
# check-entrypoint-coverage.sh — entry-points-and-crash-reporting-01PMZD13
# UNIT-1. Recurrence prevention for the defect this mission fixes: before
# this gate existed, pr.yml's Go steps compiled and tested exactly one of
# the module's four cmd/ entry points (cmd/harness-vm). cmd/kenaz-updater
# and cmd/mcpsubcmd carried 573 lines of passing tests CI never ran, and
# cmd/harness-served's `serve` build tag was invoked by no workflow at all
# and did not even compile.
#
# THE DEFECT CLASS THIS CATCHES
# ------------------------------
# A naive "does some pr.yml pattern textually cover this directory" check
# would have been vacuous for exactly the defect that motivated this gate:
# `./cmd/...` (or even `./cmd/harness-served/...`) textually "covers"
# cmd/harness-served, but cmd/harness-served/main.go carries `//go:build
# serve`, so under any pr.yml step that does not pass `-tags serve`, the Go
# toolchain silently treats the package as having ZERO files and reports
# success without ever reading a line of it (confirmed by running
# `go vet ./cmd/...` against this tree before the -tags serve step existed:
# exit 0, no mention of cmd/harness-served). A pattern-presence check would
# have stayed green through that entire gap.
#
# So this gate is build-tag aware: for every cmd/<name>/main.go, it reads
# the file's leading `//go:build <tag>` line (if any) and requires a
# COVERING pr.yml Go step — one whose command has no `-tags` flag, for an
# untagged entry point; or one whose `-tags` value matches the file's own
# tag, for a tagged one. The module root (`.`) is checked the same way
# against the untagged patterns only — it carries no build tag today.
#
# PATTERN MATCHING (spec entry-points-and-crash-reporting-01PMZD13 §4.1):
# prefix-based — `./cmd/...` covers any `./cmd/<x>`; `./core/...` covers
# any `./core/<x>`; `.` covers the root package only; a literal
# `./cmd/<name>/...` or `./cmd/<name>` covers only that one directory.
#
# Usage: bash scripts/ci/check-entrypoint-coverage.sh (from anywhere).

set -euo pipefail

source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib/ci-gate.sh"

GATE="[entrypoint-coverage]"
PR_YML=".github/workflows/pr.yml"

ci_require_file "$PR_YML" "$GATE"
ci_require_dir "cmd" "$GATE"

# Extract every single-line `run: go (vet|test|build) ...` step from
# pr.yml. All of this mission's covering steps are single-line `run:`
# entries (no YAML block scalars) — confirmed by reading the file.
mapfile -t GO_STEPS < <(grep -E '^[[:space:]]*run: go (vet|test|build) ' "$PR_YML" | sed -E 's/^[[:space:]]*run: //')

if [[ ${#GO_STEPS[@]} -eq 0 ]]; then
  echo "${GATE} FAIL: found zero 'go vet|test|build' steps in ${PR_YML} — the gate has nothing to check patterns against." >&2
  exit 1
fi

# UNTAGGED_PATTERNS: package-pattern args from steps with no -tags flag.
# TAGGED_PATTERNS_<tag>: package-pattern args from steps whose -tags value
# is exactly <tag> (bash 3 compatible — no associative arrays assumed).
UNTAGGED_PATTERNS=()
TAG_NAMES=()
declare -a TAG_PATTERNS_LIST=()

for step in "${GO_STEPS[@]}"; do
  tag=""
  patterns=()
  read -ra words <<<"$step"
  for ((i = 0; i < ${#words[@]}; i++)); do
    w="${words[$i]}"
    case "$w" in
      -tags)
        tag="${words[$((i + 1))]}"
        ;;
      -tags=*)
        tag="${w#-tags=}"
        ;;
      ./* | .)
        patterns+=("$w")
        ;;
    esac
  done
  if [[ -n "$tag" ]]; then
    found=0
    for idx in "${!TAG_NAMES[@]}"; do
      if [[ "${TAG_NAMES[$idx]}" == "$tag" ]]; then
        TAG_PATTERNS_LIST[$idx]="${TAG_PATTERNS_LIST[$idx]} ${patterns[*]}"
        found=1
        break
      fi
    done
    if [[ $found -eq 0 ]]; then
      TAG_NAMES+=("$tag")
      TAG_PATTERNS_LIST+=("${patterns[*]}")
    fi
  else
    UNTAGGED_PATTERNS+=("${patterns[@]}")
  fi
done

# covers <dir> <pattern...> — true if any pattern's prefix-based match
# covers <dir> (a repo-relative path with no leading "./", or "." for the
# module root).
covers() {
  local dir="$1"
  shift
  local p
  for p in "$@"; do
    [[ -z "$p" ]] && continue
    if [[ "$dir" == "." ]]; then
      [[ "$p" == "." ]] && return 0
      continue
    fi
    case "$p" in
      ./...)
        return 0
        ;;
      ./cmd/...)
        [[ "$dir" == cmd/* ]] && return 0
        ;;
      ./core/...)
        [[ "$dir" == core/* ]] && return 0
        ;;
      "./${dir}/..." | "./${dir}")
        return 0
        ;;
    esac
  done
  return 1
}

fail=0
findings=()

# Every cmd/<name>/main.go — one directory level under cmd/. Scoped to the
# literal filename "main.go" deliberately: a cmd/ package may carry
# GOOS-conditional siblings (cmd/kenaz-updater/wait_windows.go carries
# `//go:build windows`, wait_other.go `//go:build !windows`) that are
# normal per-platform variants of the SAME entry point, resolved
# automatically by the toolchain's GOOS — not a second build mode needing
# its own `-tags` opt-in the way cmd/harness-served's `serve` tag is.
# Reading a tag off the wrong file conflates the two and produces a false
# positive (observed while writing this gate).
#
# Plain newline-delimited (not -print0/-Z): repo paths never contain
# newlines, and `grep -Z` was observed to emit '\n' rather than NUL on at
# least one BSD-grep build, silently emptying a `read -d ''` loop —
# confirmed by running it, not assumed.
while IFS= read -r mainfile; do
  [[ -z "$mainfile" ]] && continue
  dir="$(dirname "$mainfile")"
  dir="${dir#./}"

  req_tag=""
  tag_line="$(grep -m1 -E '^//go:build ' "$mainfile" || true)"
  if [[ -n "$tag_line" ]]; then
    req_tag="${tag_line#//go:build }"
    req_tag="$(echo "$req_tag" | tr -d '[:space:]')"
  fi

  if [[ -z "$req_tag" ]]; then
    if covers "$dir" "${UNTAGGED_PATTERNS[@]}"; then
      continue
    fi
    fail=1
    findings+=("${dir} — package main, no build tag, no untagged pr.yml Go step covers it")
    continue
  fi

  covering_found=0
  for idx in "${!TAG_NAMES[@]}"; do
    [[ "${TAG_NAMES[$idx]}" == "$req_tag" ]] || continue
    if covers "$dir" ${TAG_PATTERNS_LIST[$idx]}; then
      covering_found=1
    fi
  done
  if [[ $covering_found -eq 0 ]]; then
    fail=1
    findings+=("${dir} — package main, build tag '${req_tag}', no pr.yml Go step passes -tags ${req_tag} with a covering pattern")
  fi
done < <(find cmd -mindepth 2 -maxdepth 2 -name 'main.go' 2>/dev/null | sort -u || true)

# Module root.
if compgen -G "*.go" >/dev/null && grep -lqE '^package main$' ./*.go 2>/dev/null; then
  if ! covers "." "${UNTAGGED_PATTERNS[@]}"; then
    fail=1
    findings+=(". (module root) — package main, no untagged pr.yml Go step covers it")
  fi
fi

if [[ $fail -ne 0 ]]; then
  echo "${GATE} FAIL: entry point(s) with no compiling/testing coverage in ${PR_YML}:" >&2
  for f in "${findings[@]}"; do
    echo "  - ${f}" >&2
  done
  exit 1
fi

echo "${GATE} clean — every cmd/ entry point and the module root are covered by a pr.yml Go step."
