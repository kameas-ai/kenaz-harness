#!/usr/bin/env bash
# check-seam-implementers.sh — every interface in core/agentgraph/seams.go
# PLUS every exported interface under core/ that is the type of a field
# on an exported *Config/*Options/*Deps struct has >=1 non-test
# implementer or a //wiring:deferred directive (wiring-integrity-
# 01PMAG04 WP06, spec §3.2 item 4; widened by automation-actually-runs-
# 01PMZ404 UNIT-17, G-1a).
#
# Catches the PromptTemplateSource shape (spec item 5: a seam interface
# with zero production implementers, self-documented in a comment but
# not machine-checkable) at authoring time for any FUTURE seam, not
# just that one. PromptTemplateSource itself lives in prompt_render.go,
# not seams.go, so it was out of this guard's original scope by design —
# tasks.md scoped WP06 to seams.go — but it already carried a
# wiring:deferred directive from WP02 regardless.
#
# G-1a WIDENING (UNIT-17): the original design's input set was ONE FILE
# IN ONE PACKAGE. automation-actually-runs-01PMZ404 found seven
# unimplemented interfaces (ArtifactsReadWriter, ToolCaller,
# NetworkAuthorizer, corewf.AuditEmitter, slashcmd.ToolDispatcher,
# catalog.RecipeRegistry, wfsched.Dispatcher) on corewf.Deps and sibling
# *Config/*Options structs elsewhere in core/ — none of them visible to
# this gate because none lived in seams.go. The DERIVED input set
# (scripts/ci/cmd/checkseams/main.go's derivedInterfaces) closes that:
# any exported *Config/*Options/*Deps struct anywhere under core/ is in
# scope automatically, no edit to this script or that struct's owning
# package required. Running the widened gate against the tree
# immediately surfaced seven MORE such interfaces in unrelated
# subsystems (storage, llm view, hooks, memory view, subagent dispatch)
# — each is wiring:deferred'd at its own declaration with a dated,
# owned justification (see docs/unwired-ledger.md, 2026-09-12) rather
# than fixed here, since fixing them is out of this mission's scope.
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

echo "[seam-implementers] checking core/agentgraph/seams.go interfaces + derived *Config/*Options/*Deps field interfaces against ./core/... implementers..."
"$BIN"
