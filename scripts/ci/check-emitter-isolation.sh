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

if [[ -z "$violations" ]]; then
  echo "[emitter-isolation] clean — no callers of runtime.EventsEmit found."
  exit 0
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
