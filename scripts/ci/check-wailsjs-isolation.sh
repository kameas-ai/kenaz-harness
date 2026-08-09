#!/usr/bin/env bash
# Privacy CI: only frontend/src/lib/harnessClient.ts may import from
# `wailsjs/*`. Plan §4.1 / FR-007 / C-001. ESLint also enforces.
#
# Paths are anchored to the repo root by lib/ci-gate.sh — with a cwd-relative
# default ROOT this grep matched nothing from any other directory and the
# script reported "clean — no imports of wailsjs found."
set -euo pipefail

source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib/ci-gate.sh"

ROOT="${1:-frontend/src}"

ci_require_dir "$ROOT" "[wailsjs-isolation]"

fail=0
matches=$(grep -rEn "from ['\"][^'\"]*wailsjs[^'\"]*['\"]" --include='*.ts' --include='*.vue' "$ROOT" 2>/dev/null || true)
if [[ -z "$matches" ]]; then
  echo "[wailsjs-isolation] clean — no imports of wailsjs found."
  exit 0
fi

while IFS= read -r line; do
  file="${line%%:*}"
  if [[ "$file" == *"frontend/src/lib/harnessClient.ts" ]]; then
    continue
  fi
  echo "[wailsjs-isolation] FAIL: $line" >&2
  fail=1
done <<< "$matches"

if [[ $fail -ne 0 ]]; then
  echo "[wailsjs-isolation] only frontend/src/lib/harnessClient.ts may import from wailsjs/*." >&2
  exit 1
fi

echo "[wailsjs-isolation] clean."
