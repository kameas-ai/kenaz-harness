#!/usr/bin/env bash
# check-config-nil-coverage.sh — the G-1 gate from
# kitty-specs/trust-surfaces-that-fire-01PMZ202 (spec.md §G-1, WP26):
# "a struct field that is declared, read, and never assigned in
# production." No existing gate in the repository can see this class —
# check-seam-implementers.sh asks whether an INTERFACE has an
# implementer; check-nil-optional-deps.sh (I18) triggers only on a
# doc-comment phrase on any interface field anywhere. This gate is
# narrower and structurally different: a CONCRETE field on one of the
# four canonical dependency-injection struct shapes
# (Config/Options/GateOptions/Deps), independent of whether its author
# remembered to write "optional" in a comment.
#
# Backed by a type-checker binary at scripts/ci/cmd/checkconfig (the
# check-seam-implementers.sh / check-nil-optional-deps.sh pattern:
# golang.org/x/tools/go/packages, already a module dependency; this
# shell script is a thin, self-hosted-runner-safe wrapper that only
# needs `go build` — no apt-get, no external tools).
#
# TWO TIERS, BOTH GATING (both carry a mandated planted-violation proof
# in gates_can_fail_test.go: "config-nil-coverage/unset-interface-field"
# and "config-nil-coverage/orphan-with-option"):
#
#   Tier 1 — unset nilable field. Every exported Config/Options/
#   GateOptions/Deps struct under core/ constructed in >=1 non-test
#   composite literal: every pointer/interface/func/map/chan/slice
#   field never set to a non-nil value (composite-literal key OR a
#   plain `x.Field = v` assignment outside the struct's own methods)
#   is a violation.
#
#   Tier 2 — orphan injector. Every exported With* function/method
#   under core/ whose first parameter is a non-empty interface, with
#   zero non-test callers under core/. See main.go's header for why
#   this interface-typed-first-arg restriction exists (an unrestricted
#   scan finds ~200 candidates dominated by legitimate test-only
#   mock-injection knobs, not this defect class) and why it still
#   catches every one of the spec's own five named examples.
#
# NOT IMPLEMENTED AS A THIRD GATING TIER: Tier 3 (a string field whose
# zero value silently disables a feature, per a doc-comment phrase —
# e.g. corefs.GateOptions.PolicyDir) is explicitly speced as
# "advisory (warn) in v1... a gate that over-fires gets disabled,
# which is worse than no gate," with no planted-violation obligation.
# This tool prints Tier-3 candidates to stdout as informational only;
# they never affect the exit code. See main.go's header for the full
# reasoning.
#
# Violations must appear in
# scripts/ci/allowlists/i16-config-nil-coverage.txt (NOT the spec
# text's literal "i16-nil-optional-deps.txt" — see that file's header
# for why: the name collides conceptually with the unrelated, already-
# shipped i18-nil-optional-deps.txt) with a DATED justification naming
# the blocker and owner. Allowlists shrink monotonically; a line that
# no longer corresponds to a violation is STALE and fails the gate.
#
# Exit codes:
#   0 — every Tier-1 field and Tier-2 With*-function is wired, called, or allowlisted
#   1 — the checker itself failed to build, load packages, read the allowlist,
#       or found zero candidates at all (the discovery-floor guard — see
#       main.go's `run()`: a scan that finds nothing is far more likely broken
#       than a genuinely empty codebase)
#   2 — at least one Tier-1 field or Tier-2 function is unlisted, or the allowlist is stale
#
# Usage: bash scripts/ci/check-config-nil-coverage.sh (from anywhere).

set -euo pipefail

WORKTREE_ROOT="$(git rev-parse --show-toplevel 2>/dev/null || pwd)"
cd "$WORKTREE_ROOT"

BIN="$(mktemp -d)/checkconfig"
trap 'rm -rf "$(dirname "$BIN")"' EXIT

echo "[config-nil-coverage] building checker..."
if ! go build -o "$BIN" ./scripts/ci/cmd/checkconfig/; then
  echo "[config-nil-coverage] FAIL: checkconfig failed to build." >&2
  exit 1
fi

echo "[config-nil-coverage] scanning core/ for unset Config/Options/GateOptions/Deps fields and orphan With* injectors..."
"$BIN"
