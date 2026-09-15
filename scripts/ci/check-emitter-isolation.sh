#!/usr/bin/env bash
# Privacy CI invariant: only core/rpc/emitter.go and
# core/rpc/stream_broker.go may call runtime.EventsEmit (plan §4.2,
# WP11). Any third caller fails the build.
#
# Paths are anchored to the repo root by lib/ci-gate.sh — this gate greps `.`,
# so invoked from any other directory it found nothing and reported "clean".
set -euo pipefail

source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib/ci-gate.sh"

ci_require_dir core "[emitter-isolation]"

ALLOWED=(
  "core/rpc/emitter.go"
  "core/rpc/stream_broker.go"
)

# Match call syntax — `runtime.EventsEmit(` — so doc comments that
# reference the symbol by name (e.g. "only emitter.go calls
# runtime.EventsEmit ...") don't trip the guard. Exclude .claude/
# (worktree scratch space) and vendor/ if present.
violations=$(grep -rln 'runtime\.EventsEmit(' \
  --include='*.go' \
  --exclude-dir='.claude' \
  --exclude-dir='vendor' \
  --exclude-dir='node_modules' \
  . 2>/dev/null || true)

# Discovery floor (Finding #74, CI-gate-hardening, 2026-09-14): a bare
# "found nobody, clean" here does not distinguish "the invariant holds"
# from "the runtime.EventsEmit( pattern stopped matching anything at
# all" — and the two known-legitimate callers (emitter.go,
# stream_broker.go) should ALWAYS be in this set. If they are not, the
# scan itself is broken, not the invariant.
if [[ -z "$violations" ]]; then
  echo "[emitter-isolation] FAIL: found zero callers of runtime.EventsEmit anywhere under core/ —" >&2
  echo "[emitter-isolation] not even ${ALLOWED[0]} or ${ALLOWED[1]}, both of which are known, live" >&2
  echo "[emitter-isolation] callers. This is almost certainly a broken grep pattern (a rename of" >&2
  echo "[emitter-isolation] EventsEmit, a call-syntax change), not an emitter-free codebase." >&2
  exit 1
fi

fail=0
while IFS= read -r f; do
  rel="${f#./}"
  ok=0
  for allow in "${ALLOWED[@]}"; do
    if [[ "$rel" == "$allow" ]]; then
      ok=1
      break
    fi
  done
  if [[ $ok -eq 0 ]]; then
    echo "[emitter-isolation] FAIL: $rel calls runtime.EventsEmit (only emitter.go + stream_broker.go are allowed)" >&2
    fail=1
  fi
done <<< "$violations"

if [[ $fail -ne 0 ]]; then
  exit 1
fi

echo "[emitter-isolation] clean — invariant passes."
