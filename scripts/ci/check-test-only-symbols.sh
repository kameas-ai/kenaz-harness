#!/usr/bin/env bash
# Privacy CI invariant #3 (plan §4.3): exported identifiers prefixed
# Test/Fake/Stub/Fixture must live only in `_test.go` files inside
# core/rpc/ and core/rpc/views/*.
set -euo pipefail

DIRS=(core/rpc core/rpc/views)

fail=0

for d in "${DIRS[@]}"; do
  if [[ ! -d "$d" ]]; then continue; fi
  # Find non-_test.go files under d (recursive) and grep for exported
  # identifiers prefixed Test/Fake/Stub/Fixture.
  while IFS= read -r f; do
    matches=$(grep -nE '^(func|type|var|const)\s+(Test|Fake|Stub|Fixture)[A-Z]' "$f" || true)
    if [[ -n "$matches" ]]; then
      echo "[test-only-symbols] FAIL: $f exports test-only identifiers in a non-_test file:" >&2
      echo "$matches" >&2
      fail=1
    fi
  done < <(find "$d" -type f -name '*.go' ! -name '*_test.go')
done

if [[ $fail -ne 0 ]]; then
  echo "[test-only-symbols] privacy CI invariant #3 (plan §4.3) failed." >&2
  exit 1
fi

echo "[test-only-symbols] clean — invariant #3 passes."
