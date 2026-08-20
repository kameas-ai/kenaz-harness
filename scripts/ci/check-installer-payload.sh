#!/usr/bin/env bash
# check-installer-payload.sh — entry-points-and-crash-reporting-01PMZD13
# UNIT-2. Every .exe release.yml builds into build/bin/ for Windows must be
# installed by the NSIS installer, or a shipped exe silently rides along in
# the zip only — the exact defect that left "Install & Restart" quitting
# without restarting on the installer channel (kenaz-updater.exe was signed
# and zipped but never `File`'d into the installer).
#
# WHAT THIS PROVES, AND WHAT IT DOES NOT
# ----------------------------------------
# This is a STATIC payload-manifest check: it cross-references build
# targets in release.yml against `File` / `!insertmacro wails.files`
# directives reachable from project.nsi. It does not run makensis and does
# not install anything. No self-hosted Windows runner exists for this repo
# and GitHub-hosted runners are reserved for release builds by org policy
# (CLAUDE.md § "Runner pool policy") — an actual install can only be
# verified in release.yml's own Windows job, not here.
#
# `!insertmacro wails.files` is treated as covering `${PRODUCT_EXECUTABLE}`
# only, because that is what build/windows/installer/wails_tools.nsh's
# expansion (`File "/oname=${PRODUCT_EXECUTABLE}" "${ARG_WAILS_*_BINARY}"`)
# actually does — it does not pick up any other build/bin/*.exe.
# ${PRODUCT_EXECUTABLE} is not itself one of the -o "build/bin/<name>.exe"
# targets this script extracts (it is produced by `wails build`, not a raw
# `go build -o`), so it never needs to be cross-referenced.
#
# Usage: bash scripts/ci/check-installer-payload.sh (from anywhere).

set -euo pipefail

source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib/ci-gate.sh"

GATE="[installer-payload]"
RELEASE_YML=".github/workflows/release.yml"
NSI="build/windows/installer/project.nsi"

ci_require_file "$RELEASE_YML" "$GATE"
ci_require_file "$NSI" "$GATE"

# Every named .exe build target: -o "build/bin/<name>.exe" in release.yml.
mapfile -t BUILT_EXES < <(grep -oE '\-o "build/bin/[A-Za-z0-9_.-]+\.exe"' "$RELEASE_YML" \
  | sed -E 's/.*build\/bin\/([A-Za-z0-9_.-]+\.exe)".*/\1/' | sort -u)

if [[ ${#BUILT_EXES[@]} -eq 0 ]]; then
  echo "${GATE} FAIL: found zero '-o \"build/bin/<name>.exe\"' build targets in ${RELEASE_YML} — the gate has nothing to check." >&2
  exit 1
fi

# Every literal `File "...<name>.exe"` payload directive in project.nsi
# (the installer's install Section). wails.files itself is not parsed as
# a source of build/bin/*.exe coverage — see header.
mapfile -t INSTALLED_EXES < <(grep -oE 'File "[^"]*\.exe"' "$NSI" \
  | sed -E 's#.*[\\/]([A-Za-z0-9_.-]+\.exe)".*#\1#' | sort -u)

fail=0
missing=()
for exe in "${BUILT_EXES[@]}"; do
  found=0
  for installed in "${INSTALLED_EXES[@]:-}"; do
    [[ "$installed" == "$exe" ]] && { found=1; break; }
  done
  if [[ $found -eq 0 ]]; then
    fail=1
    missing+=("$exe")
  fi
done

if [[ $fail -ne 0 ]]; then
  echo "${GATE} FAIL: release.yml builds these .exe target(s) that ${NSI} never installs:" >&2
  for m in "${missing[@]}"; do
    echo "  - ${m}" >&2
  done
  echo "${GATE} A signed, zipped exe with no installer payload line reproduces the" >&2
  echo "${GATE} kenaz-updater.exe defect this gate exists to prevent. This gate proves the" >&2
  echo "${GATE} PAYLOAD MANIFEST only, not a real Windows install — no self-hosted Windows" >&2
  echo "${GATE} runner exists for this repo." >&2
  exit 1
fi

echo "${GATE} clean — ${#BUILT_EXES[@]} built .exe target(s) all have an installer payload line (manifest only, not an install proof)."
