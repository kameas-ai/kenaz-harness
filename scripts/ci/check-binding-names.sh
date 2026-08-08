#!/usr/bin/env bash
# Privacy CI: Wails-reflected Bindings methods use `<View>_<Operation>`
# with `_` reserved as separator. Forbid double underscores and any
# underscore-inside-view/operation pattern (plan §8 R-6).
set -euo pipefail

source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib/ci-gate.sh"

GATE="[binding-names]"
BINDINGS_FILE="core/rpc/bindings.go"

ci_require_file "$BINDINGS_FILE" "$GATE"

# Match exported method names on *Bindings.
#
# The receiver name is a wildcard, not the literal `b`. It used to be `b`:
# renaming the receiver to anything else (gofmt won't, but a refactor will)
# made this grep match zero methods, and a zero-method loop announced
# "clean." Verified — `func (bnd *Bindings) Foo__Bar()` passed the old gate.
methods=$(grep -oE 'func \([A-Za-z_][A-Za-z0-9_]* \*Bindings\) [A-Z][A-Za-z0-9_]+' "$BINDINGS_FILE" | awk '{print $NF}')

# Zero methods is not a clean bill of health — it means the extraction stopped
# matching the code. Make that loud rather than green.
if [[ -z "$methods" ]]; then
  echo "$GATE FAIL: parsed 0 exported *Bindings methods out of $BINDINGS_FILE." >&2
  echo "$GATE The method-declaration shape changed and this grep went stale." >&2
  echo "$GATE Fix the pattern here; a gate that inspects nothing must not pass." >&2
  exit 1
fi

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
