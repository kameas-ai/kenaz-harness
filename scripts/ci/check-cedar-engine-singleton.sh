#!/usr/bin/env bash
# check-cedar-engine-singleton.sh — I15: exactly one Cedar engine
# construction reachable from rpc.New.
#
# THE DEFECT CLASS (WP05, consent-surfaces-truth-01PMTR01)
# ----------------------------------------------------------------------
# Before this gate, core/rpc/api.go called buildCedarGate(dataDir) or
# buildCedarEngineOrNil(dataDir) independently at thirteen separate call
# sites (nine + four). Each call constructs a FRESH *cedar.Engine with
# its own private atomic PolicySet, so a user who authors a policy
# through the editor, saves it, and watches ListPolicies report it
# "loaded" — which reloads exactly ONE of those thirteen engines: the
# one the cedarpolicy view happens to hold — sees zero of the other
# twelve gates change behaviour, in the same process, without a
# restart. That is the "Recorded, not fixed" remainder from the
# v0.63.1 release commit.
#
# check-cedar-gate-arguments.sh (I13) checks the ARGUMENT at a call
# site — is it cedar.AllowAll{} / nil? It has no vocabulary for "N
# separate engines exist where the product promises one", which is
# exactly why thirteen independent constructions passed it clean: every
# one of them passed a REAL, non-AllowAll, non-nil argument. The defect
# this gate closes is about INSTANCE COUNT, not argument shape.
#
# WHAT THIS CHECKS
# ----------------
# 1. Every non-test call to buildCedarGate(/buildCedarEngineOrNil( in
#    core/rpc/**/*.go — i.e. every USE, not the two function
#    DEFINITIONS — must total exactly ONE. That one call is the
#    boot-time construction (`a.cedarEngine = buildCedarEngineOrNil(...)`
#    in rpc.New); every other gate site must consult the shared engine
#    (a.cedarEngine / a.cedarGate()) instead of building its own.
# 2. `.NewEngine(` — the actual constructor these two builders wrap —
#    must appear in non-test core/**/*.go EXACTLY twice: once inside
#    buildCedarGate's body, once inside buildCedarEngineOrNil's. A third
#    occurrence anywhere is a second engine built by hand, bypassing the
#    singleton builders entirely — invisible to check 1, which only
#    watches the two named builder functions.
#
#    Two deliberate widenings over the first cut of this gate, each
#    verified by planting the evading construction and watching the
#    narrow form exit 0 (see the three cedar-engine-singleton cases in
#    scripts/ci/gates_can_fail_test.go):
#      - The pattern is `\.NewEngine\(`, NOT `cedar\.NewEngine\(`.
#        Aliasing the cedar import is an idiom this repo already uses
#        (`cedarpolicy "…/core/policy/cedar"` at
#        core/rpc/contextbootstrap_wiring.go:41 and
#        core/rpc/views/settings/fleet.go:15), so anchoring on the
#        default package name let `cedarpolicy.NewEngine(...)` walk
#        straight past. No other package under core/ exposes a
#        NewEngine, so the broader pattern has no false positives today
#        and would name any that appeared.
#      - The scan root is core/, NOT core/rpc/. The promise is ONE
#        engine per PROCESS; an engine constructed in any other package
#        breaks it just as completely, and a gate that only looks where
#        the defect happened to live last time is a gate that catches
#        the defect once.
#
#    A third widening (mission vm-execution-surface-truth-01PMZD14 WP03,
#    HV-03/UNIT-1's G-1): Check 2's scan additionally covers cmd/. A
#    hand-built `cedar.Engine` under cmd/harness-vm would satisfy "this
#    process has a policy guard" while breaking the single-engine promise
#    just as completely as one under core/ — and CORE_ROOT="core" alone
#    was blind to it (verified: the gate reported "no other production
#    caller under core" while cmd/harness-vm/agentexec.go left Policy
#    unset entirely, R-4 in spec.md). Check 1's RPC_ROOT is UNCHANGED —
#    it is about core/rpc's two named builder functions specifically, and
#    has no cmd/ analogue: cmd/harness-vm does not call
#    buildCedarGate/buildCedarEngineOrNil, it consults the shared engine
#    through rpc.API.CedarGate().
#
# Usage: bash scripts/ci/check-cedar-engine-singleton.sh (from anywhere).

set -euo pipefail

source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib/ci-gate.sh"

GATE="[cedar-engine-singleton]"
API_FILE="core/rpc/api.go"
RPC_ROOT="core/rpc"
# Check 2 is process-wide, not rpc-wide — see the header. cmd/ was added by
# WP03 above; core/ is unchanged from the original cut of this gate.
CORE_ROOT="core"
CMD_ROOT="cmd"

ci_require_file "$API_FILE" "$GATE"
ci_require_dir "$RPC_ROOT" "$GATE"
ci_require_dir "$CORE_ROOT" "$GATE"
ci_require_dir "$CMD_ROOT" "$GATE"

# ---------------------------------------------------------------------------
# Vacuous-pass guard: the two builder functions must still exist under
# their known names, or every count below is vacuously "clean" because
# there is nothing left to find.
# ---------------------------------------------------------------------------
if ! grep -qE '^func buildCedarGate\(' "$API_FILE"; then
  echo "${GATE} FAIL: buildCedarGate not found in ${API_FILE}." >&2
  echo "${GATE} This gate assumes it is the sole non-test entry point that wraps" >&2
  echo "${GATE} cedar.NewEngine as a cedar.Gate. If it was renamed or removed, update" >&2
  echo "${GATE} this script (or delete it) in the same commit." >&2
  exit 1
fi
if ! grep -qE '^func buildCedarEngineOrNil\(' "$API_FILE"; then
  echo "${GATE} FAIL: buildCedarEngineOrNil not found in ${API_FILE}." >&2
  echo "${GATE} This gate assumes it is the sole non-test entry point that wraps" >&2
  echo "${GATE} cedar.NewEngine as a *cedar.Engine. If it was renamed or removed," >&2
  echo "${GATE} update this script (or delete it) in the same commit." >&2
  exit 1
fi

fail=0

# ---------------------------------------------------------------------------
# Check 1: exactly one call site for the two builders combined.
# ---------------------------------------------------------------------------
calls=$(grep -rnE 'buildCedarGate\(|buildCedarEngineOrNil\(' --include='*.go' "$RPC_ROOT" 2>/dev/null \
  | grep -v '_test\.go' \
  | grep -vE ':func buildCedarGate\(' \
  | grep -vE ':func buildCedarEngineOrNil\(' \
  | grep -vE '^[^:]+:[0-9]+:[[:space:]]*//' \
  || true)
calls=$(printf '%s\n' "$calls" | grep -v '^$' || true)
count=0
if [[ -n "$calls" ]]; then
  count=$(printf '%s\n' "$calls" | wc -l | tr -d '[:space:]')
fi

if [[ "$count" -eq 0 ]]; then
  echo "${GATE} FAIL: found ZERO calls to buildCedarGate/buildCedarEngineOrNil outside" >&2
  echo "${GATE} their own definitions. Something restructured the boot path — either" >&2
  echo "${GATE} the singleton is now built some other way (update this gate to match)" >&2
  echo "${GATE} or the chassis boots with no Cedar engine wired to anything, which is a" >&2
  echo "${GATE} worse regression than the one this gate exists to catch." >&2
  exit 1
fi

if [[ "$count" -gt 1 ]]; then
  echo "" >&2
  echo "${GATE} FAIL: ${count} call sites construct a Cedar engine independently — want 1:" >&2
  printf '%s\n' "$calls" | sed 's/^/    /' >&2
  echo "" >&2
  echo "${GATE} Every gate site must consult the ONE engine rpc.New builds" >&2
  echo "${GATE} (a.cedarEngine / a.cedarGate()) instead of calling" >&2
  echo "${GATE} buildCedarGate/buildCedarEngineOrNil itself — otherwise a policy save +" >&2
  echo "${GATE} reload only reaches whichever gate happens to hold the instance that" >&2
  echo "${GATE} got reloaded, exactly the v0.63.1 'recorded, not fixed' defect." >&2
  fail=1
fi

# ---------------------------------------------------------------------------
# Check 2: `.NewEngine(` appears exactly twice — once in each builder's
# body. A third occurrence anywhere under core/ OR cmd/ is a hand-built
# engine bypassing the singleton entirely. Package-qualifier-agnostic and
# process-wide on purpose; see the header for the evasions that motivated
# each widening (the cmd/ root closes the one this gate could not see
# until WP03: a hand-built engine in cmd/harness-vm would pass Check 1
# untouched, since Check 1 only watches core/rpc's two named builders).
# ---------------------------------------------------------------------------
newengine_hits=$(grep -rnE '\.NewEngine\(' --include='*.go' "$CORE_ROOT" "$CMD_ROOT" 2>/dev/null | grep -v '_test\.go' || true)
newengine_hits=$(printf '%s\n' "$newengine_hits" | grep -v '^$' || true)
ne_count=0
if [[ -n "$newengine_hits" ]]; then
  ne_count=$(printf '%s\n' "$newengine_hits" | wc -l | tr -d '[:space:]')
fi

if [[ "$ne_count" -ne 2 ]]; then
  echo "" >&2
  echo "${GATE} FAIL: .NewEngine( appears ${ne_count} time(s) in non-test ${CORE_ROOT}+${CMD_ROOT}" >&2
  echo "${GATE} code — want exactly 2 (buildCedarGate's body + buildCedarEngineOrNil's" >&2
  echo "${GATE} body):" >&2
  printf '%s\n' "$newengine_hits" | sed 's/^/    /' >&2
  echo "" >&2
  echo "${GATE} A direct construction outside the two builders bypasses the singleton" >&2
  echo "${GATE} entirely — it always produces a second, un-reloadable engine no matter" >&2
  echo "${GATE} how many callers of buildCedarGate/buildCedarEngineOrNil there are." >&2
  fail=1
fi

if [[ "$fail" -ne 0 ]]; then
  echo "" >&2
  echo "${GATE} FAIL — see violations above." >&2
  exit 1
fi

echo "${GATE} clean — exactly one Cedar engine construction (${count} call site) is reachable from rpc.New; .NewEngine has no other production caller under ${CORE_ROOT} or ${CMD_ROOT}."
