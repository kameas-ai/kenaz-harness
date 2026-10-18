#!/usr/bin/env bash
# check-risk-gate-decides.sh — WP08 (risk-rated-autonomy-01PMRA01): fail
# the build when the risk gate cannot decide.
#
# Delegates to scripts/ci/cmd/checkriskgatedecides, which asserts:
#
#   1. every production switch on a cedar.Outcome-shaped value that
#      already branches on Allow and Deny also branches explicitly on
#      Confirm (never via an unlabelled `default:`);
#   2. the RiskThreshold autonomy dial has its real knobcoverage.Register
#      consumer; and
#   3. the family floor (core/policy/risk/floor.go) sits strictly above
#      the autonomous tier's risk threshold (core/autonomy/presets.go).
#
# See that tool's package doc comment for the full rationale, scope
# notes, and exit-code contract.
set -euo pipefail
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib/ci-gate.sh"

ci_require_dir core/policy/cedar "[risk-gate-decides]"
ci_require_dir core/autonomy "[risk-gate-decides]"
ci_require_file core/policy/risk/floor.go "[risk-gate-decides]"

go run ./scripts/ci/cmd/checkriskgatedecides
