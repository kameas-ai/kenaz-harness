#!/usr/bin/env bash
# check-audit-store-before-retention.sh — G-1
# (audit-that-tells-the-truth-01PMZA10 WP12, tasks.md §UNIT-10, plan.md
# Rule 3: "nothing advertises retention before UNIT-8. G-1 makes it
# mechanical.")
#
# THE DEFECT CLASS
# -----------------
# Before this mission, `AuditSettingsPanel.vue` unconditionally told users
# "The retention sweep runs nightly." and "...permanently deleted during
# the nightly sweep." while nothing in the process actually deleted a row
# — the Audit view was an in-memory ring with no store, no scheduler, and
# no sweep. UNIT-4 made the store real; UNIT-8 made the local sweeper
# real and drove the panel from a wired fact
# (Settings_GetAuditSettings().retention_enforced, set from
# `a.localAuditRetentionScheduler != nil` — never a literal). This gate
# is the standing guard against that ordering ever reverting: any future
# change that removes the store wiring must ALSO remove every claim that
# retention is enforced, in the same commit, or CI fails.
#
# WHAT THIS CHECKS
# ----------------
# 1. Does `core/rpc/api.go`'s `audit.NewAPI(...)` construction carry the
#    `audit.WithStore(` option? (The one production call site — see
#    api.go's own UNIT-4 comment above the auditOpts slice.)
#
# 2. If (1) is true, the gate is satisfied unconditionally: a real store
#    is wired, so nothing downstream can be lying about retention being
#    enforced over a store that does not exist. This mirrors Rule 6
#    (plan.md): once the store lands, the panel copy is fact-driven and
#    honest in BOTH the "keep_forever" and "delete_after_window" states,
#    so there is nothing left for this gate to police.
#
# 3. If (1) is false (the store option has been removed), the gate scans
#    a DERIVED input set — no opt-in allowlist — for anything that still
#    claims retention is enforced:
#      - a hardcoded `RetentionEnforced: true` literal (Go, non-test)
#      - a hardcoded `SetAuditRetentionEnforced(true)` call (Go, non-test)
#      - a scheduled-sweep construction site
#        (`NewLocalRetentionScheduler(` / `RetentionSweep(`, Go, non-test)
#      - a frontend panel string promising a sweep ("nightly sweep",
#        "runs nightly", "retention sweep runs") in a non-test .vue/.ts
#        file
#    Any match with the store option absent is exactly the "toggle that
#    reports it is on" class CLAUDE.md's unwired-sweep ritual exists to
#    close, and the gate fails naming every offending line.
#
# Planted-violation proof: scripts/ci/gates_can_fail_test.go
# "audit-store-before-retention/store-removed-sweep-claim-remains" —
# removes the `audit.WithStore(` call while
# `eventlog.NewLocalRetentionScheduler(` stays present, and asserts the
# gate rejects it.
#
# Usage: bash scripts/ci/check-audit-store-before-retention.sh

set -euo pipefail

source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib/ci-gate.sh"

GATE="[audit-store-before-retention]"
API_GO="core/rpc/api.go"
RPC_DIR="core/rpc"
FRONTEND_DIR="frontend/src"

ci_require_file "$API_GO" "$GATE"
ci_require_dir "$RPC_DIR" "$GATE"
ci_require_dir "$FRONTEND_DIR" "$GATE"

if grep -q 'audit\.WithStore(' "$API_GO"; then
  echo "${GATE} clean — audit.NewAPI's construction carries audit.WithStore(); the store is wired, so retention claims are backed by it."
  exit 0
fi

echo "${GATE} audit.WithStore( is absent from ${API_GO} — checking for retention claims that outlived it..." >&2

violations=0

check_pattern() {
  local desc="$1" pattern="$2" dir="$3"
  local hits
  hits="$(grep -rnE "$pattern" "$dir" --include='*.go' --include='*.vue' --include='*.ts' 2>/dev/null \
    | grep -v -E '_test\.go:|\.spec\.ts:' || true)"
  if [[ -n "$hits" ]]; then
    echo "${GATE} FAIL: ${desc}, with no store option on audit.NewAPI:" >&2
    echo "$hits" | sed "s/^/${GATE}   /" >&2
    violations=1
  fi
}

check_pattern "a hardcoded RetentionEnforced: true literal" 'RetentionEnforced:[[:space:]]*true\b' "$RPC_DIR"
check_pattern "a hardcoded SetAuditRetentionEnforced(true) call" 'SetAuditRetentionEnforced\(true\)' "$RPC_DIR"
check_pattern "a scheduled local-retention-sweeper construction site" 'NewLocalRetentionScheduler\(' "$RPC_DIR"
check_pattern "a direct RetentionSweep( invocation" '\bRetentionSweep\(' "$RPC_DIR"
check_pattern "a frontend panel string promising a sweep" '(nightly sweep|runs nightly|retention sweep runs)' "$FRONTEND_DIR"

if [[ "$violations" -ne 0 ]]; then
  echo "${GATE} FAIL — retention is claimed somewhere above while audit.NewAPI carries no store" >&2
  echo "${GATE} option. Either restore audit.WithStore( in ${API_GO}, or remove every claim" >&2
  echo "${GATE} above in the SAME commit (plan.md Rule 3)." >&2
  exit 1
fi

echo "${GATE} clean — no retention claim found while the store option is absent."
