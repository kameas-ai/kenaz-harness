#!/usr/bin/env bash
# check-no-forbidden-compaction-symbols.sh — CI gate for
# kitty-specs/agentgraph-total-convergence-01PMGX01/spec.md §6 I4 (WP01:
# `fix(ci): WP01 — convergence invariant gates`).
#
# I4: exactly one compaction entry point. `SuppressAutomaticCompaction`
# does not exist. `runPreSendCompaction` does not exist. Spec §1.2: the
# FR-041 `env.Compactor` pipeline is built and wired (core/rpc/api.go)
# but `chat_runner.go` sets `SuppressAutomaticCompaction: true`
# unconditionally, so the pipeline never runs; what actually executes is
# `runPreSendCompaction()` against a second, pre-kernel `core/compaction`
# package. Both symbols are grep-forbidden.
#
# scripts/ci/allowlists/i4-forbidden-compaction-symbols.txt holds today's
# permitted exceptions (both symbols, since they are live in
# core/rpc/views/agentgraph/chat/chat_runner.go and core/agentgraph today —
# spec §4.4 / WP08 retires them). Unlike the I10 gate, an allowlist entry
# here going STALE (the symbol no longer exists) is the intended,
# celebrated outcome, not a special case: it means the symbol was deleted,
# so the line must be deleted too (spec §4.1 shrinking allow-lists).
#
# Usage: bash scripts/ci/check-no-forbidden-compaction-symbols.sh
# (from anywhere).

set -euo pipefail

source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib/ci-gate.sh"

GATE="[no-forbidden-compaction-symbols]"
SCAN_ROOT="core"
ALLOW_FILE="scripts/ci/allowlists/i4-forbidden-compaction-symbols.txt"

ci_require_dir "$SCAN_ROOT" "$GATE"
ci_require_file "$ALLOW_FILE" "$GATE"

fail=0

load_allowlist() {
  grep -vE '^[[:space:]]*(#|$)' "$1" || true
}

allow=$(load_allowlist "$ALLOW_FILE")

while IFS= read -r symbol; do
  [[ -z "$symbol" ]] && continue
  present=0
  if grep -rlw "$symbol" --include='*.go' "$SCAN_ROOT" 2>/dev/null | grep -v '_test\.go' | grep -q .; then
    present=1
  fi
  allowed=0
  if printf '%s\n' "$allow" | grep -qxF "$symbol"; then
    allowed=1
  fi

  if [[ "$present" -eq 1 && "$allowed" -eq 0 ]]; then
    echo "" >&2
    echo "${GATE} FAIL: forbidden symbol '${symbol}' found outside test files and not in ${ALLOW_FILE}:" >&2
    grep -rnw "$symbol" --include='*.go' "$SCAN_ROOT" 2>/dev/null | grep -v '_test\.go' | sed 's/^/    /' >&2
    fail=1
  fi
done < <(printf 'SuppressAutomaticCompaction\nrunPreSendCompaction\n')

# Stale allowlist entries: the symbol has been fully removed from non-test
# source. Here that is the GOOD outcome (I4's whole point), but the line
# must still be deleted so the allow-list keeps shrinking (spec §4.1).
while IFS= read -r symbol; do
  [[ -z "$symbol" ]] && continue
  if ! grep -rlw "$symbol" --include='*.go' "$SCAN_ROOT" 2>/dev/null | grep -v '_test\.go' | grep -q .; then
    echo "" >&2
    echo "${GATE} FAIL: STALE entry in ${ALLOW_FILE} — '${symbol}' no longer appears outside test files. Delete the line (this is the intended outcome of spec §4.4/WP08 — the list should shrink)." >&2
    fail=1
  fi
done <<< "$allow"

if [[ "$fail" -ne 0 ]]; then
  echo "" >&2
  echo "${GATE} FAIL — see violations above." >&2
  exit 1
fi

echo "${GATE} clean — no forbidden compaction symbol outside ${ALLOW_FILE}."
