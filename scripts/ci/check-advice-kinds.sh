#!/usr/bin/env bash
# check-advice-kinds.sh — WP03 (laya-advisors-01LAYA001): fail the build
# when an AdviceKind registration is incomplete or a suggest-only kind
# reaches an auto-act call site.
#
# Delegates to scripts/ci/cmd/checkadvicekinds, which asserts:
#
#   1. core/advice/kind.go's own shape (both SafetyClass values, the
#      AdviceKind struct's four spec-named fields) is still discoverable
#      — the non-vacuous floor. Zero advice kinds ship in production
#      until WP04-06, so "kind count" itself is NOT the floor (see the
#      tool's package doc for why a >=1-kind gate would break every PR
#      on this release branch for weeks).
#   2. every `advice.(Must)?Register(advice.AdviceKind{...})` composite
#      literal found under core/ and cmd/ carries id, extractor,
#      prompt_version, and safety class.
#   3. no `advice.RequireCanAutoAct("<id>")` call site names a kind
#      registered SafetySuggestOnly.
#
# See that tool's package doc comment for the full rationale and exit-code
# contract.
set -euo pipefail
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib/ci-gate.sh"

ci_require_dir core/advice "[advice-kinds]"
ci_require_file core/advice/kind.go "[advice-kinds]"

go run ./scripts/ci/cmd/checkadvicekinds
