#!/usr/bin/env bash
# check-seam-implementers.sh — every interface in core/agentgraph/seams.go
# has >=1 non-test implementer or a //wiring:deferred directive
# (wiring-integrity-01PMAG04 WP06, spec §3.2 item 4).
#
# Catches the PromptTemplateSource shape (spec item 5: a seam interface
# with zero production implementers, self-documented in a comment but
# not machine-checkable) at authoring time for any FUTURE seam, not
# just that one. PromptTemplateSource itself lives in prompt_render.go,
# not seams.go, so it is out of this specific guard's scope by design —
# tasks.md scopes WP06 to seams.go — but it already carries a
# wiring:deferred directive from WP02 regardless.
#
# Go's interfaces are structural: there is no explicit `implements`
# declaration to grep for, so this is NOT a shell/grep check. Whether a
# type satisfies an interface is a type-checker question, and the only
# sound way to answer it is to ask the type checker
# (golang.org/x/tools/go/packages, already a module dependency via
# core/secrets/lint) — see scripts/ci/cmd/checkseams/main.go for the
# implementation. This script is a thin, self-hosted-runner-safe
# wrapper (no apt-get; `go build` is the only tool it needs).
#
# Exit codes:
#   0 — every seam interface has an implementer or a directive
#   1 — the checker itself failed to build or load packages
#   2 — at least one seam interface has neither
#
set -euo pipefail

WORKTREE_ROOT="$(git rev-parse --show-toplevel 2>/dev/null || pwd)"
cd "$WORKTREE_ROOT"

BIN="$(mktemp -d)/checkseams"
trap 'rm -rf "$(dirname "$BIN")"' EXIT

echo "[seam-implementers] building checker..."
if ! go build -o "$BIN" ./scripts/ci/cmd/checkseams/; then
  echo "[seam-implementers] FAIL: checkseams failed to build." >&2
  exit 1
fi

echo "[seam-implementers] checking core/agentgraph/seams.go interfaces against ./core/... implementers..."
"$BIN"
