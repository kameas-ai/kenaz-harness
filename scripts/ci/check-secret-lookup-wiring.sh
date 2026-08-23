#!/usr/bin/env bash
# check-secret-lookup-wiring.sh — B4 (unwired sweep, release/v0.72.0): the
# THIRD of three production wiring fields PR #308's own regression suite
# never actually drove through the real wiring site.
#
# THE DEFECT CLASS
# -----------------
# core/rpc/api.go's buildChatRunner constructs the production
# *chat.ChatRunner via a chat.Config{...} composite literal. Its
# SecretLookup field (core/rpc/views/agentgraph/chat/chat_runner.go:426,
# type refs.SecretLookup) is what installs the @secret:<locator> resolver
# and turn-sanitizer on every StartStream (trust-surfaces-that-fire-
# 01PMZ202 WP19) — without it, core/tools/bash, core/tools/webfetch and
# core/mcp/transport/stdio/server.go's refs.ResolverFromContext(ctx) calls
# see nil forever and the literal "@secret:<locator>" token reaches
# whatever tool the model invokes, unresolved and unredacted.
#
# core/rpc/views/agentgraph/chat/secret_resolver_wiring_test.go proves the
# BEHAVIOUR is correct once SecretLookup is set — but it builds its own
# chat.Config{SecretLookup: lookup} by hand (buildSecretAwareRunner), never
# touching api.go's buildChatRunner. Deleting `SecretLookup: secretLookup,`
# from api.go leaves that whole test suite green.
#
# WHY THIS IS ITS OWN SCRIPT, NOT A NEW check-cedar-gate-arguments.sh
# CLAUSE
# ---------------------------------------------------------------------
# check-cedar-gate-arguments.sh's clause 3/5 mechanism resolves a Config
# struct field's WITNESS to a specific concrete type — *cedar.Engine —
# declared in the SAME FILE as the Config struct (permissions.Engine,
# contextsync.Gate: see clause 5). refs.SecretLookup is satisfied by
# *secrets.ExposureIndex, a DIFFERENT package with no such witness
# declared anywhere, and the field's type in chat.Config is spelled with
# a THIRD package's import alias (credstoreRefs.SecretLookup). Extending
# clause 5's same-file witness resolution to reach across three packages
# would need real cross-package type resolution — a job for a Go
# type-checker tool (in the spirit of check-seam-implementers.sh), not
# another regex. Given this is one named field at one named call site,
# a small dedicated script is safer to get right and to verify than
# distorting check-cedar-gate-arguments.sh's cedar-specific charter to
# cover a non-cedar collaborator.
#
# WHAT THIS CHECKS
# ----------------
# In core/rpc/api.go, EVERY non-test `chat.Config{ ... }` composite
# literal must assign `SecretLookup:` to something other than a bare
# `nil` or an omitted field. An omitted field is the Go zero value for an
# interface — nil — which is the exact defect this check exists to
# catch: no literal string to grep for, only an absence. Checking every
# occurrence rather than assuming exactly one keeps this from breaking
# the day a legitimate second production call site appears, and gives a
# planted-violation proof a clean way to add a second, deliberately-bad
# literal without touching buildChatRunner's real one (see
# scripts/ci/gates_can_fail_test.go's
# "secret-lookup-wiring/second-literal-omits-field" case).
#
# Usage: bash scripts/ci/check-secret-lookup-wiring.sh (from anywhere).

set -euo pipefail

source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib/ci-gate.sh"

GATE="[secret-lookup-wiring]"
API_FILE="core/rpc/api.go"
CHAT_CONFIG_FILE="core/rpc/views/agentgraph/chat/chat_runner.go"

ci_require_file "$API_FILE" "$GATE"
ci_require_file "$CHAT_CONFIG_FILE" "$GATE"

# ---------------------------------------------------------------------------
# Vacuous-pass guards: assert the raw material this check depends on is
# still there before drawing any conclusion from its absence — the same
# discipline check-cedar-gate-arguments.sh applies to its own clauses.
# ---------------------------------------------------------------------------
if ! grep -qE '^\tSecretLookup[[:space:]]+refs\.SecretLookup$' "$CHAT_CONFIG_FILE"; then
  echo "${GATE} FAIL: chat.Config no longer declares 'SecretLookup refs.SecretLookup' in ${CHAT_CONFIG_FILE}." >&2
  echo "${GATE} Either the field was renamed/retyped (update this script in the same" >&2
  echo "${GATE} commit) or the field was removed, in which case this check's premise" >&2
  echo "${GATE} no longer holds and it should be deleted alongside it." >&2
  exit 1
fi

if ! grep -qE '^func buildChatRunner\(' "$API_FILE"; then
  echo "${GATE} FAIL: buildChatRunner not found in ${API_FILE}." >&2
  echo "${GATE} It is the designated production constructor this check assumes owns" >&2
  echo "${GATE} the chat.Config{} literal. If it was renamed, rename it here too." >&2
  exit 1
fi

literal_count=$(grep -oE 'chat\.Config\{' "$API_FILE" | wc -l | tr -d '[:space:]')
if [[ "$literal_count" -eq 0 ]]; then
  echo "${GATE} FAIL: no 'chat.Config{' composite literal found in ${API_FILE}." >&2
  echo "${GATE} Clause has nothing to inspect, which is indistinguishable from passing." >&2
  echo "${GATE} Either buildChatRunner was refactored to build the config differently" >&2
  echo "${GATE} (update this script) or the production chat runner is no longer wired" >&2
  echo "${GATE} through a literal at all." >&2
  exit 1
fi

# ---------------------------------------------------------------------------
# Extract EVERY chat.Config{ ... } literal by brace counting (comments
# stripped per-line first, so a `// SecretLookup: nil` comment inside a
# literal cannot satisfy or defeat the match) and decide OMITTED / NIL / OK
# for each one's SecretLookup field in a single awk pass, emitting one
# short status line per occurrence ("<n> OMITTED|NIL|OK"). Deliberately
# NOT split into "capture the raw literal text, then grep/awk it again in
# a second pass": a second pass indexing the captured text by ordinal
# field position (a bare `$2` after `-F` on code containing its own literal
# tabs) was tried first and produced empty output every time its result
# was assigned to a shell variable via `$(...)` in this environment, while
# the exact same awk program printed correctly when piped straight to
# another command — reproduced deliberately with a minimal case
# (`awk -F'\t' '$1=="1"{print $2}'` vs `{print}`) before working around it
# by never doing field-position extraction inside a $()-captured awk call.
# Doing the whole decision in one pass sidesteps it entirely.
# ---------------------------------------------------------------------------
statuses=$(awk '
  function code(s) { sub(/\/\/.*$/, "", s); return s }
  function reset() { seen = 0; isnil = 0 }
  {
    c = code($0)
    if (!collecting && index(c, "chat.Config{") > 0) {
      collecting = 1
      depth = 0
      occurrence++
      reset()
    }
    if (collecting) {
      line = c
      gsub(/^[[:space:]]+/, "", line)
      if (line ~ /^SecretLookup:/) {
        seen = 1
        val = line
        sub(/^SecretLookup:[[:space:]]*/, "", val)
        gsub(/[[:space:]]/, "", val)
        sub(/,$/, "", val)
        if (val == "nil") isnil = 1
      }
      n = gsub(/\{/, "{", c); m = gsub(/\}/, "}", c)
      depth += n - m
      if (depth <= 0 && n + m > 0) {
        collecting = 0
        status = "OK"
        if (!seen) status = "OMITTED"
        else if (isnil) status = "NIL"
        print occurrence, status
      }
    }
  }
' "$API_FILE")

fail=0
while read -r occ status; do
  [[ -z "$occ" ]] && continue
  case "$status" in
    OMITTED)
      echo "" >&2
      echo "${GATE} FAIL: chat.Config{} literal #${occ} in ${API_FILE} never assigns" >&2
      echo "${GATE} SecretLookup: — the field is omitted, which is the same nil" >&2
      echo "${GATE} interface value as an explicit 'SecretLookup: nil'." >&2
      echo "${GATE} Without it, driveRun's 'if r.cfg.SecretLookup != nil' guard" >&2
      echo "${GATE} (chat_runner.go) never installs refs.WithResolver/WithTurnSanitizer," >&2
      echo "${GATE} and every @secret:<locator> token reaches the tool call verbatim." >&2
      fail=1
      ;;
    NIL)
      echo "" >&2
      echo "${GATE} FAIL: chat.Config{} literal #${occ} in ${API_FILE} assigns" >&2
      echo "${GATE} 'SecretLookup: nil' explicitly — the same unconditional disable as" >&2
      echo "${GATE} an omitted field. Pass the secretLookup parameter computed from the" >&2
      echo "${GATE} shared exposureIdx (nil-guarded — see the comment above it in" >&2
      echo "${GATE} ${API_FILE})." >&2
      fail=1
      ;;
  esac
done <<< "$statuses"

if [[ "$fail" -ne 0 ]]; then
  echo "" >&2
  echo "${GATE} FAIL — see violation(s) above." >&2
  exit 1
fi

echo "${GATE} clean — every chat.Config{} literal in ${API_FILE} assigns SecretLookup to a non-nil value."
