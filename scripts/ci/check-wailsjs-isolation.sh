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

# NO DISCOVERY FLOOR HERE, DELIBERATELY (Finding #74, CI-gate-hardening,
# 2026-09-14 audit): checked against the real tree — harnessClient.ts's
# own header comment explains why zero matches is TODAY'S correct state,
# not a broken scan: "Until regeneration, WailsBindings resolves through
# the runtime-attached `window.go.rpc.Bindings` object Wails injects at
# boot" — i.e. this codebase currently reaches the Wails surface through
# a runtime-injected global, not a static `from 'wailsjs/...'` import,
# so a fresh checkout with wailsjs/ ungenerated legitimately has ZERO
# matches for this pattern anywhere, including in the one file allowed
# to have them. A floor asserting "must be non-empty" would hard-fail
# CI on this correct, current architecture. If harnessClient.ts is ever
# changed back to a static import (post code-generation), this comment's
# premise should be revisited alongside it.
#
# The early exit below is NOT the floor (there is none) — it is a
# required guard against a separate bug: `while read -r line; do ...
# done <<< "$matches"` on an EMPTY $matches still executes the loop body
# ONCE with line="" (a here-string always ends in a newline, so `read`
# gets one empty line before EOF), which fell through every check below
# and printed "FAIL: " with an empty violation. Removing this exit
# while auditing for Finding #74 reproduced that exact false failure —
# left here as the fix, not as unrelated legacy code.
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
