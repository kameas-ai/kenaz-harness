#!/usr/bin/env bash
# check-nil-optional-deps.sh — the nil-optional-dependency gate.
#
# THE DEFECT CLASS
# -----------------
# An optional interface field on a config/deps struct that no production
# wiring site ever assigns, so at runtime it is nil and the feature
# silently doesn't happen — while the type system, the tests (which
# construct the struct directly), and the field's own doc comment all
# say it works. Six confirmed instances shipped before this gate
# existed (see scripts/ci/cmd/checknilopts/main.go's header for the
# list and docs/unwired-ledger.md's 2026-08-20 entry for the full
# history): ChatRunDispatcher, wfsched.Dispatcher,
# registry.Options.Cost, a nil cedarPolicyAPI, the fs.Prompter that
# denied every kenaz__write_file call, and HV-03's registry.Options.Policy
# in cmd/harness-vm.
#
# THREE MISSIONS SPECCED THIS GATE; NONE BUILT IT
# -------------------------------------------------
# model-scheduled-jobs-01PMSJ01 §7 G-1 and model-settings-reach-the-model-
# 01PMZ101 §7 G-2 independently designed the same new gate, triggered by
# a doc-comment phrase. fleet-enforcement-truth-01PMZ505 §7 G-1 proposed
# widening check-cedar-gate-arguments.sh (I13) instead, triggered by
# field type + directory location. See scripts/ci/cmd/checknilopts/
# main.go's header for the full comparison — this tool takes the
# doc-phrase design.
#
# WHAT THIS GATE CANNOT SEE
# -----------------------------
# Two structural blind spots — full detail in scripts/ci/cmd/checknilopts/
# main.go's header, kept here as a pointer so a reader of just this
# script isn't misled into thinking the gate has full coverage:
#   1. A field with NO doc comment at all cannot trigger a doc-comment
#      match, by construction — this is what let registry.Options.Cost
#      and .Policy ship invisible to this gate right up until the PR
#      #332 review round found it (both now carry a true doc comment,
#      closing those two specific cases; the general class limitation —
#      any future undocumented optional interface field — remains).
#      Z505's type+location design (above) does not have this blind
#      spot; this gate does not implement that design (see main.go for
#      why) and this is the cost of that choice.
#   2. The module root (main.go) is outside scanPatterns
#      (./core/... and ./cmd/... only) for BOTH field discovery and
#      assignment search — found chasing core/update/bootswap.Config.
#      Relauncher, whose one real production call site is main.go.
#
# WHY THIS IS A GO TOOL, NOT A GREP
# ------------------------------------
# "Is this field's type an interface" and "is it assigned a non-nil
# value anywhere in core/+cmd/, across composite literals, Set*/With*
# calls, and plain field-assignment statements" are type-checker
# questions with no reliable textual proxy — the same reason
# check-seam-implementers.sh (scripts/ci/cmd/checkseams) is a Go tool.
# This shares its packages.Load pattern and its self-hosted-runner-safe
# wrapper shape (no apt-get; `go build` is the only tool needed).
#
# Violations must appear in scripts/ci/allowlists/i18-nil-optional-deps.txt
# with a DATED justification naming the blocker and owner — same
# contract as every other gate in this directory. Allowlists shrink
# monotonically; a line that no longer corresponds to a violation is
# STALE and fails the gate.
#
# Exit codes:
#   0 — every optional-by-doc-comment interface field is wired or allowlisted
#   1 — the checker itself failed to build, load packages, or read the allowlist
#   2 — at least one field is neither wired nor allowlisted, or the allowlist is stale
#
# Usage: bash scripts/ci/check-nil-optional-deps.sh (from anywhere).

set -euo pipefail

WORKTREE_ROOT="$(git rev-parse --show-toplevel 2>/dev/null || pwd)"
cd "$WORKTREE_ROOT"

BIN="$(mktemp -d)/checknilopts"
trap 'rm -rf "$(dirname "$BIN")"' EXIT

echo "[nil-optional-deps] building checker..."
if ! go build -o "$BIN" ./scripts/ci/cmd/checknilopts/; then
  echo "[nil-optional-deps] FAIL: checknilopts failed to build." >&2
  exit 1
fi

echo "[nil-optional-deps] scanning core/+cmd/ for optional-by-doc-comment interface fields with no production assignment..."
"$BIN"
