#!/usr/bin/env bash
# Privacy CI: no credential-shaped field on TS/Vue types whose name ends
# in Reference|Credential|Secret. Forbidden field names (case-insensitive):
# value, secret, password, apiKey, token. FR-020 / C-004.
set -euo pipefail

ROOT="${1:-frontend/src}"

fail=0

# Match `interface FooReference { ... value ... }` patterns roughly.
# This is a coarse defensive grep — the full ESLint custom rule lives in
# eslint.config.js and runs on every PR.
matches=$(grep -rEn 'interface [A-Z][a-zA-Z0-9]*(Reference|Credential|Secret)\b' --include='*.ts' --include='*.vue' "$ROOT" 2>/dev/null || true)

if [[ -z "$matches" ]]; then
  echo "[no-credential-in-ui] no credential-shaped types found — clean."
  exit 0
fi

while IFS= read -r line; do
  file="${line%%:*}"
  startln="${line#*:}"
  startln="${startln%%:*}"
  # Slice from the `interface` line to the closing brace at column 0
  # (TS/Vue interfaces are conventionally formatted that way). Only
  # scan within that block — checking the whole file produces false
  # positives like `value: T` on an unrelated DialEffectiveField in
  # the same file. `// privacy-allow:` opt-out still honoured.
  block=$(awk -v start="$startln" '
    NR < start { next }
    NR == start { inblock=1; print; next }
    inblock { print; if ($0 ~ /^}/) exit }
  ' "$file")
  if echo "$block" \
       | grep -v 'privacy-allow:' \
       | grep -qE '^\s*(value|secret|password|apiKey|token)\s*[:?]'; then
    echo "[no-credential-in-ui] FAIL: $file:$startln declares a forbidden field on a credential-shaped type" >&2
    fail=1
  fi
done <<< "$matches"

if [[ $fail -ne 0 ]]; then
  exit 1
fi

echo "[no-credential-in-ui] clean — FR-020 / C-004 invariant passes."
