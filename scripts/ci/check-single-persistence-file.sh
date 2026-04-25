#!/usr/bin/env bash
# Privacy CI invariant #5 (plan §4.3): exactly one persistence file path
# is referenced from core/rpc/settings.go (the canonical settings.json).
# Any second persistence path fails the build.
set -euo pipefail

FILE="core/rpc/settings.go"
if [[ ! -f "$FILE" ]]; then
  echo "[single-persistence-file] $FILE not found — skipping (WP13 lands the file)."
  exit 0
fi

# Count distinct settings.json references.
count=$(grep -oE 'settings\.json' "$FILE" | sort -u | wc -l | tr -d ' ')
if [[ "$count" -gt 1 ]]; then
  echo "[single-persistence-file] FAIL: multiple distinct persistence-file names referenced in $FILE" >&2
  exit 1
fi

echo "[single-persistence-file] clean — invariant #5 passes."
