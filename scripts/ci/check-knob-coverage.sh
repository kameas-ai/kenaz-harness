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
# autonomy-knobs-live-01PMAG02's own WP07 is expected to register
# autonomy.ResolvedKnobs's seven fields this way (some Register, some
# RegisterDeferred for whatever remains unwired when that WP lands) —
# see core/wiring/knobcoverage's package doc for the full contract.
# Until that lands, `-run 'TestKnobCoverage'` only matches this
# package's own self-test (core/wiring/knobcoverage/knobcoverage_test.go,
# TestKnobCoverage_Mechanism), which proves the mechanism against a
# synthetic struct — so this guard is exercised and green today, not a
# no-op placeholder waiting for a sibling mission.
#
# Exit codes:
#   0 — every TestKnobCoverage* test passed (includes the case where
#       only the mechanism's own self-test exists yet)
#   1 — go test itself failed to run (build error, etc.) — see output
#   2 — a TestKnobCoverage* test failed (an Uncovered() call reported
#       a field with neither a consumer nor a deferral)
#
set -euo pipefail

WORKTREE_ROOT="$(git rev-parse --show-toplevel 2>/dev/null || pwd)"
cd "$WORKTREE_ROOT"

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
echo "[knob-coverage] clean — every TestKnobCoverage* test passed."
exit 0
