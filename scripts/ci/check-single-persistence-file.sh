#!/usr/bin/env bash
# Privacy CI invariant #5 (plan §4.3): exactly one persistence file path is
# referenced from the settings store. Any second persistence path fails the
# build — one file is what makes "delete this file and you are back to
# factory" a true statement.
#
# THIS GATE WAS INCAPABLE OF FAILING BEFORE 2026-08-08. Two independent
# reasons, either one sufficient:
#
#   1. It read core/rpc/settings.go, which has not existed since the settings
#      view moved to core/rpc/views/settings/. The `[[ ! -f ]]` branch printed
#      "not found — skipping (WP13 lands the file)" and exited 0. WP13 landed
#      the file somewhere else; the skip became permanent.
#
#   2. The count was `grep -oE 'settings\.json' FILE | sort -u | wc -l`. The
#      regex matches exactly one literal string, so `sort -u` collapses every
#      hit to a single line no matter how many there are. `count > 1` was
#      arithmetically unreachable. Even pointed at the right file, with ten
#      different persistence paths in it, this printed "clean".
#
# What it does now: extract every distinct persistence-file basename literal
# from the settings store implementation and fail if there is more than one,
# or zero (zero means the extraction stopped matching the code — a broken
# gate, not a clean one).
set -euo pipefail

source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib/ci-gate.sh"

GATE="[single-persistence-file]"
FILE="core/rpc/views/settings/impl.go"

ci_require_file "$FILE" "$GATE" \
  "The settings FileStore moved. Re-point FILE at its new home rather than letting the gate skip."

# Distinct persistence-file basenames: any quoted "<name>.<persistence-ext>"
# literal. Deliberately broad on the extension — swapping settings.json for
# settings.db is still a second persistence path if the first one survives.
paths=$(grep -oE '"[A-Za-z0-9._-]+\.(json|db|sqlite|sqlite3|yaml|yml|toml|ini|cfg)"' "$FILE" | sort -u || true)
count=$(printf '%s' "$paths" | grep -c . || true)

if [[ "$count" -eq 0 ]]; then
  echo "$GATE FAIL: no persistence-file literal found in $FILE." >&2
  echo "$GATE The store either stopped naming its file inline or this extraction went stale." >&2
  echo "$GATE Zero matches is a broken gate, not a clean tree — fix the extraction here." >&2
  exit 1
fi

if [[ "$count" -gt 1 ]]; then
  echo "$GATE FAIL: $count distinct persistence-file names referenced in $FILE:" >&2
  echo "$paths" | sed 's/^/    /' >&2
  echo "$GATE Invariant #5 (plan §4.3) requires exactly one." >&2
  exit 1
fi

echo "$GATE clean — invariant #5 passes (single persistence file: $paths)."
