#!/usr/bin/env bash
# check-knob-coverage.sh — every registered resolved-config struct field
# has a runtime consumer or an explicit deferral (wiring-integrity-
# 01PMAG04 WP07, spec §3.2 item 2).
#
# core/wiring/knobcoverage is the GENERAL mechanism: any package can
# call knobcoverage.Register[T]("Field", "where it's consumed") or
# knobcoverage.RegisterDeferred[T]("Field", "why not yet") for any
# struct type, then assert knobcoverage.Uncovered[T]() is empty in a
# test named TestKnobCoverage*. This script doesn't know or care which
# structs are tracked — it just runs every such test across the module:
#
#   go test ./core/... -run 'TestKnobCoverage'
#
# so a new mission wiring a new dial system gets coverage enforcement
# by adding a registration + a test, with ZERO changes to this script
# or to pr.yml.
#
# autonomy-knobs-live-01PMAG02 WP07 landed that registration:
# core/rpc/views/agentgraph/chat/knob_coverage_guard_test.go asserts
# knobcoverage.Uncovered[autonomy.ResolvedKnobs]() is empty against the
# seven Register + two RegisterDeferred calls in chat_runner.go,
# kernel_tool_adapter.go and llm_provider_adapter.go.
#
# VACUOUS-PASS GUARD (unwired sweep, 2026-08-14). This script's original
# header blessed the state where `-run 'TestKnobCoverage'` matched only
# core/wiring/knobcoverage's own synthetic self-test — reasonable while
# WP07 was in flight, and a hole once it landed: deleting or renaming the
# real guard test would leave the gate matching the self-test alone, and
# it would still print "clean". A gate whose success is indistinguishable
# from "nothing is registered" is the exact defect
# scripts/ci/gates_can_fail_test.go exists to catch. So the script now
# requires at least one TestKnobCoverage* test OUTSIDE the mechanism's
# own package before it will report clean.
#
# Known scope limit, tracked in docs/unwired-ledger.md rather than fixed
# here: the only struct any package registers today is
# autonomy.ResolvedKnobs (9 fields). settings.Settings (76 fields) and
# dials.DialConfig (13) are outside the mechanism entirely, which is why
# the inert dials in those two structs were found by hand this sweep and
# not by this gate.
#
# Exit codes:
#   0 — every TestKnobCoverage* test passed AND a real (non-self-test)
#       guard exists
#   1 — go test itself failed to run (build error, etc.) — see output
#   2 — a TestKnobCoverage* test failed (an Uncovered() call reported
#       a field with neither a consumer nor a deferral)
#   3 — no TestKnobCoverage* test outside core/wiring/knobcoverage; the
#       gate would have passed by checking only its own mechanism
#
set -euo pipefail

WORKTREE_ROOT="$(git rev-parse --show-toplevel 2>/dev/null || pwd)"
cd "$WORKTREE_ROOT"

# The mechanism's own self-test proves the machinery works against a
# synthetic struct. It is not evidence that any REAL struct is tracked.
MECHANISM_PKG="core/wiring/knobcoverage"
real_guards=$(grep -rlE '^func TestKnobCoverage' --include='*_test.go' core 2>/dev/null \
  | grep -v "^${MECHANISM_PKG}/" || true)
if [[ -z "$real_guards" ]]; then
  echo "[knob-coverage] FAIL: no TestKnobCoverage* test exists outside ${MECHANISM_PKG}." >&2
  echo "  Every registration in the tree has been deleted, renamed, or moved into the" >&2
  echo "  mechanism's own package. This gate would otherwise report 'clean' while" >&2
  echo "  asserting nothing about any real config struct." >&2
  echo "  Fix: restore (or add) a guard that asserts knobcoverage.Uncovered[T]() is empty" >&2
  echo "  for a real resolved-config type — see core/rpc/views/agentgraph/chat/knob_coverage_guard_test.go." >&2
  exit 3
fi
echo "[knob-coverage] real guard(s) found:"
printf '  %s\n' $real_guards

echo "[knob-coverage] running every TestKnobCoverage* test across ./core/..."
if ! go test ./core/... -run '^TestKnobCoverage' -count=1 -v 2>&1; then
  echo "" >&2
  echo "[knob-coverage] FAIL: a registered resolved-config field has no consumer and no deferral." >&2
  echo "  Fix: wire a real consumer and call knobcoverage.Register[T](\"Field\", \"where\"), or" >&2
  echo "  call knobcoverage.RegisterDeferred[T](\"Field\", \"why not yet\") if it's a deliberate gap." >&2
  echo "  See core/wiring/knobcoverage's package doc for the full contract." >&2
  exit 2
fi

echo ""
echo "[knob-coverage] clean — every TestKnobCoverage* test passed (and a real guard exists)."
exit 0
