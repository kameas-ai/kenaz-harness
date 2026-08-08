#!/usr/bin/env bash
# Privacy CI invariant #3 (plan §4.3): exported identifiers prefixed
# Test/Fake/Stub/Fixture must live only in `_test.go` files inside
# core/rpc/ and core/rpc/views/*.
#
# Paths are anchored to the repo root by lib/ci-gate.sh. The `[[ ! -d ]] &&
# continue` below is a real guard for a fork that removed a directory, but
# with a cwd-relative DIRS it also skipped both directories — and printed
# "clean" — whenever the script ran from anywhere but the repo root.
set -euo pipefail

source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib/ci-gate.sh"

# core/rpc must exist; core/rpc/views is covered by the recursive find below
# but is listed explicitly for readability.
ci_require_dir core/rpc "[test-only-symbols]"

DIRS=(core/rpc core/rpc/views)

fail=0

for d in "${DIRS[@]}"; do
  if [[ ! -d "$d" ]]; then continue; fi
  # Find non-_test.go files under d (recursive) and grep for exported
  # identifiers prefixed Test/Fake/Stub/Fixture.
  while IFS= read -r f; do
    # Honour `// privacy-allow:` line-end annotations so legitimate
    # domain types whose name happens to start with Test/Fake/Stub/
    # Fixture (e.g. TestResult, the structured outcome of the
    # TestProvider RPC) can opt out with a comment.
    matches=$(grep -nE '^(func|type|var|const)\s+(Test|Fake|Stub|Fixture)[A-Z]' "$f" \
      | grep -v 'privacy-allow:' || true)
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
