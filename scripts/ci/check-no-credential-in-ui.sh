#!/usr/bin/env bash
# Privacy CI: flags a plausible secret-BYTES-holding field declared on a
# TS/Vue interface or type alias whose name plausibly denotes a credential
# payload — name ends in Reference|Credential|Secret, or contains "Auth" as
# its own PascalCase component anywhere in the name (not just as a suffix:
# DeviceAuthBeginResult and OAuthToken both count, Author/Authority do not).
# FR-020 / C-004.
#
# Paths are anchored to the repo root by lib/ci-gate.sh — with a cwd-relative
# default ROOT this grep matched nothing from any other directory and the
# script reported "no credential-shaped types found — clean."
#
# WHAT THIS GATE PROMISES, PRECISELY
# -----------------------------------
# It catches a field literally named value/secret/password/apiKey/token
# (case-insensitive, `_`/`-` tolerant on apiKey) declared as a SUFFIX of a
# field's identifier — clientSecret, client_secret, accessToken all count —
# on a credential-shaped type, in both `interface Foo { ... }` and
# `type Foo = { ... }` forms, whether the body is spread across multiple
# lines or packed onto one, and whether or not the member carries a TS
# modifier (`readonly`, `static`, `declare`, `public`, `private`,
# `protected`) or is written as a quoted key (`'client_secret': string`).
#
# Those last two were NOT true when this paragraph was first written:
# review round 5 (2026-08-25) demonstrated that `readonly clientSecret`
# and `'client_secret'` both passed, because the field regexes anchored on
# `^\s*` and could cross neither the space after a modifier nor a leading
# quote. `readonly` is an ordinary TS idiom, so that was a plausible
# non-adversarial miss. Both are now normalized away before the trigger
# test and both carry planted-violation proofs in gates_can_fail_test.go.
#
# It does NOT, and cannot with a regex of this shape, distinguish a field
# that NAMES a credential (a reference: an env var name, a key list, a
# pointer) from one that HOLDS the credential bytes. That distinction is
# semantic, not lexical: `tokenEnvVar` and `token_env_var` are references
# and correctly pass today, but they pass because "token" is a PREFIX of
# the identifier, not because the gate understood "EnvVar" as a reference
# marker — the field regex only matches the trigger word as a suffix
# (immediately before `:`/`?`). A semantically-identical rename of a
# reference field to a name that puts the trigger word LAST (e.g.
# `tokenEnvVar` -> `envVarToken`) would flip this gate's verdict on a field
# whose meaning did not change. This is a known, accepted limitation, not
# an oversight: closing it in general requires understanding what a name
# MEANS, not just how it's spelled. Treat a pass from this gate as "no
# credential field found trailing a known trigger word," not as a proof
# the type is safe.
#
# This grep is the ONLY enforcement of FR-020 / C-004 that runs in CI.
# There is no separate ESLint custom rule for this in eslint.config.js —
# an earlier version of this comment claimed one existed; it never did.
set -euo pipefail

source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib/ci-gate.sh"

ROOT="${1:-frontend/src}"

ci_require_dir "$ROOT" "[no-credential-in-ui]"

fail=0

# Match `interface FooReference { ... }` / `type FooCredential = { ... }`
# style declarations, plus any type whose name carries "Auth" as its own
# component — a suffix (FooAuth), a prefix (AuthFoo), or buried in the
# middle (DeviceAuthBeginResult, OAuthToken). "Auth" only counts when
# followed by an uppercase letter or the end of the identifier, so
# Author/Authority/Authenticate (lowercase immediately after "Auth") don't
# false-trigger.
matches=$(grep -rEn '\b(interface|type)[[:space:]]+[A-Z][a-zA-Z0-9]*((Reference|Credential|Secret)\b|Auth([A-Z][a-zA-Z0-9]*)?\b)' --include='*.ts' --include='*.vue' "$ROOT" 2>/dev/null || true)

if [[ -z "$matches" ]]; then
  echo "[no-credential-in-ui] no credential-shaped types found — clean."
  exit 0
fi

while IFS= read -r line; do
  file="${line%%:*}"
  startln="${line#*:}"
  startln="${startln%%:*}"

  # Slice from the declaration line to its matching closing brace by
  # tracking brace depth char-by-char, rather than assuming the closer
  # sits alone at column 0. Column-0 anchoring is what let a single-line
  # body (`interface FooAuth { client_secret: string }`) evade detection
  # entirely: the whole declaration lives on one line that doesn't START
  # with `}`, so the old awk scan for `^}` ran off the end of the block
  # looking for a closer that was never at the start of a line. Depth
  # tracking closes correctly whether the body is single-line or spread
  # across many. A bare type alias with no object body at all (e.g.
  # `type Foo = string | number;`) is also handled: it never opens a
  # brace, so the scan stops at the first top-level `;` instead of
  # running to end-of-file.
  block=$(awk -v start="$startln" '
    NR < start { next }
    {
      print
      n = length($0)
      for (i = 1; i <= n; i++) {
        c = substr($0, i, 1)
        if (c == "{") { depth++; opened = 1 }
        else if (c == "}") { depth-- }
        else if (c == ";" && depth == 0 && opened == 0) { exit }
      }
      if (opened && depth <= 0) exit
    }
  ' "$file")

  if echo "$block" | grep -q 'privacy-allow:'; then
    continue
  fi

  # Normalize the block into one field-candidate per line by breaking on
  # the structural characters TS/Vue field lists use as separators
  # (`{`, `}`, `;`, `,`). This is what makes a single-line body
  # (`interface FooAuth { client_secret: string }`) visible: without it,
  # the field regex's `^\s*` anchor only ever sees the FIRST token on a
  # physical line, which for a one-liner is the `interface` keyword, not
  # the field name.
  fields=$(printf '%s' "$block" | tr '{};,' '\n')

  violation_line=""
  while IFS= read -r fline; do
    # Strip leading TS member modifiers and an optional quoted-key opener
    # before any trigger test. Without this, `readonly clientSecret: string`
    # and `'client_secret': string` both evade every regex below: the `^\s*`
    # anchor cannot cross the space after `readonly`, and a leading quote
    # matches neither `\s` nor `[a-zA-Z0-9_]`. `readonly` is an ordinary TS
    # idiom, so this was a plausible non-adversarial miss, not just a
    # theoretical evasion (review round 5, 2026-08-25).
    fline=$(printf '%s' "$fline" | sed -E "s/^[[:space:]]*(readonly|static|declare|public|private|protected)[[:space:]]+/  /") 
    fline=$(printf '%s' "$fline" | tr -d "\"'")
    # Presence-flag booleans (hasSecret, hasToken, ...) name a fact ABOUT
    # a credential, not the credential itself — exclude the `has<Pascal>`
    # idiom before testing the trigger words below.
    if echo "$fline" | grep -qiE '^\s*has[A-Z][a-zA-Z0-9_]*\s*[:?]'; then
      continue
    fi
    # `value` is only forbidden as the WHOLE field name (the classic
    # `interface FooCredential { value: string }` shape this gate was
    # built for), not as a suffix of an unrelated compound. Matching it
    # as a suffix caught `modelValue` (the standard Vue v-model prop) and
    # `defaultValue` — neither carries secret bytes; both are generic
    # value bindings whose name has nothing to do with the credential
    # concept beyond sharing the word "value".
    if echo "$fline" | grep -qiE '^\s*_?value\s*[:?]'; then
      violation_line="$fline"
      break
    fi
    # secret/password/apiKey/token still match as a suffix of a compound
    # identifier (clientSecret, client_secret, accessToken, ...) — see
    # the header comment for what this does and does not promise about
    # field name word order.
    if echo "$fline" | grep -qiE '^\s*[a-zA-Z0-9_]*(secret|password|api[_-]?key|token)\s*[:?]'; then
      violation_line="$fline"
      break
    fi
  done <<< "$fields"

  if [[ -n "$violation_line" ]]; then
    echo "[no-credential-in-ui] FAIL: $file:$startln declares a forbidden field on a credential-shaped type" >&2
    fail=1
  fi
done <<< "$matches"

if [[ $fail -ne 0 ]]; then
  exit 1
fi

echo "[no-credential-in-ui] clean — FR-020 / C-004 invariant passes."
