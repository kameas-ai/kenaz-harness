#!/usr/bin/env bash
# Privacy CI invariant: only core/rpc/emitter.go and
# core/rpc/stream_broker.go may call runtime.EventsEmit (plan §4.2,
# WP11). Any third caller fails the build.
set -euo pipefail

ALLOWED=(
  "core/rpc/emitter.go"
  "core/rpc/stream_broker.go"
)

violations=$(grep -rln 'runtime\.EventsEmit' --include='*.go' . 2>/dev/null || true)

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
