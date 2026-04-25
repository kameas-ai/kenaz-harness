#!/usr/bin/env bash
# Privacy CI: Wails-reflected Bindings methods use `<View>_<Operation>`
# with `_` reserved as separator. Forbid double underscores and any
# underscore-inside-view/operation pattern (plan §8 R-6).
set -euo pipefail

BINDINGS_FILE="core/rpc/bindings.go"

if [[ ! -f "$BINDINGS_FILE" ]]; then
  echo "[binding-names] $BINDINGS_FILE not found"; exit 1
fi

# Match exported method names on *Bindings.
methods=$(grep -oE 'func \(b \*Bindings\) [A-Z][A-Za-z0-9_]+' "$BINDINGS_FILE" | awk '{print $NF}')

fail=0
for m in $methods; do
  case "$m" in
    *__*)
      echo "[binding-names] FAIL: method $m has double underscore" >&2
      fail=1
      ;;
  esac
  # Methods are either flat (top-level) or `View_Op`. Allow at most one underscore.
  count=$(echo -n "$m" | tr -cd '_' | wc -c | tr -d ' ')
  if [[ "$count" -gt 1 ]]; then
    echo "[binding-names] FAIL: method $m has more than one underscore (View_Op only)" >&2
    fail=1
  fi
done

if [[ $fail -ne 0 ]]; then
  exit 1
fi

echo "[binding-names] clean."
