#!/usr/bin/env bash
# ci-gate.sh — shared preamble for the grep-based gates in scripts/ci/.
#
# WHY THIS EXISTS
# ---------------
# Most gates in this directory scan a hardcoded relative path (core/,
# frontend/src, core/rpc/bindings.go). Those paths only resolve when the
# script is invoked from the repo root. Invoked from anywhere else the grep
# matches nothing, the script prints "clean", and exits 0 — a gate that
# reports success precisely because it looked at nothing.
#
# That is not hypothetical. Run any of these from scripts/ci/ before this
# change and six of them announce "clean":
#
#   check-emitter-isolation      clean — no callers of runtime.EventsEmit found.
#   check-wailsjs-isolation      clean — no imports of wailsjs found.
#   check-single-persistence-file  core/rpc/settings.go not found — skipping
#   check-test-only-symbols      clean — invariant #3 passes.
#   check-no-credential-in-ui    no credential-shaped types found — clean.
#   check-no-user-content-in-slog  clean — invariant #2 passes.
#
# It is the same failure that let scripts/ci/check-bundle-size.mjs resolve
# dist/assets against the repo root, find nothing, and pass for months
# (fixed in #279 by anchoring to import.meta.url). This file is that fix
# applied to the shell side, once, instead of eight times.
#
# USAGE
#   source "$(dirname "${BASH_SOURCE[0]}")/lib/ci-gate.sh"
#   ci_require_dir  frontend/src "[wailsjs-isolation]"
#   ci_require_file core/rpc/bindings.go "[binding-names]"
#
# Sourcing it cd's to the repo root, so every relative path below the source
# line means what its author thought it meant.

# Resolve the repo root from THIS FILE's location — scripts/ci/lib/ → ../../..
# BASH_SOURCE[0] is this file even when sourced, which is the whole point.
_CI_GATE_LIB_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CI_REPO_ROOT="$(cd "${_CI_GATE_LIB_DIR}/../../.." && pwd)"
cd "${CI_REPO_ROOT}"

# ci_require_dir <path> <gate-prefix> [hint]
#
# Hard-fail when a directory a gate is about to scan does not exist. An
# absent scan root is never "clean" — it means the gate is pointed at the
# wrong place, or the tree it guards was deleted. Both are findings.
ci_require_dir() {
  local path="$1" gate="$2" hint="${3:-}"
  if [[ ! -d "$path" ]]; then
    echo "${gate} FAIL: scan root '${path}' does not exist under ${CI_REPO_ROOT}." >&2
    echo "${gate} A gate cannot pass by having nothing to look at. Either the path moved" >&2
    echo "${gate} (update this script in the same commit) or the guarded tree was removed." >&2
    [[ -n "$hint" ]] && echo "${gate} ${hint}" >&2
    exit 1
  fi
}

# ci_require_file <path> <gate-prefix> [hint]
#
# Same contract for a single file the gate parses.
ci_require_file() {
  local path="$1" gate="$2" hint="${3:-}"
  if [[ ! -f "$path" ]]; then
    echo "${gate} FAIL: '${path}' does not exist under ${CI_REPO_ROOT}." >&2
    echo "${gate} A gate cannot pass by having nothing to look at. Either the file moved" >&2
    echo "${gate} (update this script in the same commit) or the guarded code was removed." >&2
    [[ -n "$hint" ]] && echo "${gate} ${hint}" >&2
    exit 1
  fi
}
