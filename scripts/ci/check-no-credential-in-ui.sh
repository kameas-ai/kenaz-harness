#!/usr/bin/env bash
# Privacy CI: no credential-shaped field on TS/Vue types whose name ends
# in Reference|Credential|Secret. Forbidden field names (case-insensitive):
# value, secret, password, apiKey, token. FR-020 / C-004.
#
# Paths are anchored to the repo root by lib/ci-gate.sh — with a cwd-relative
# default ROOT this grep matched nothing from any other directory and the
# script reported "no credential-shaped types found — clean."
set -euo pipefail

source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib/ci-gate.sh"

ROOT="${1:-frontend/src}"

ci_require_dir "$ROOT" "[no-credential-in-ui]"

fail=0

# Match `interface FooReference { ... value ... }` patterns roughly, plus
# any `Foo...Auth`-shaped type (e.g. `WireRecipeAuth`) — auth payloads
# routinely carry credential fields (client_secret, etc.) under a name
# that doesn't end in Reference/Credential/Secret.
#
# This grep is the ONLY enforcement of FR-020 / C-004 that runs in CI.
# There is no separate ESLint custom rule for this in eslint.config.js —
# an earlier version of this comment claimed one existed; it never did.
# Grep for `interface .*(Reference|Credential|Secret|Auth)` yourself if
# you doubt this.
matches=$(grep -rEn 'interface [A-Z][a-zA-Z0-9]*(Reference|Credential|Secret|Auth)\b' --include='*.ts' --include='*.vue' "$ROOT" 2>/dev/null || true)

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
  # Match the forbidden words as a bare field name OR as a suffix on a
  # compound field name (client_secret, clientSecret, access_token,
  # api_key, ...) so credential-shaped payload types (WireRecipeAuth's
  # `client_id?`/`client_secret?` pairing) can't smuggle credential bytes
  # in under a snake_case Go-mirror name. Deliberately does NOT match
  # `token_env_var` — that field name holds an env var *name*, not the
  # secret bytes, and matching mid-word would make this gate un-passable
  # for reference-only fields.
  if echo "$block" \
       | grep -v 'privacy-allow:' \
       | grep -qiE '^\s*[a-zA-Z0-9_]*(value|secret|password|api[_-]?key|token)\s*[:?]'; then
    echo "[no-credential-in-ui] FAIL: $file:$startln declares a forbidden field on a credential-shaped type" >&2
    fail=1
  fi
done <<< "$matches"

if [[ $fail -ne 0 ]]; then
  exit 1
fi

echo "[no-credential-in-ui] clean — FR-020 / C-004 invariant passes."
