#!/usr/bin/env bash
# check-adapter-catalog-parity.sh — G-1 (model-settings-reach-the-model-
# 01PMZ101 WP12, spec §7 G-1): adapter↔catalog parity, checked in BOTH
# directions.
#
# THE DEFECT CLASS
# -----------------
# core/llm/registry/registry.go registers a ProviderAdapter per Kind.
# core/llm/capabilities.Catalog separately keys its descriptors on a
# provider string loaded from core/llm/capabilities/data/*.yaml (plus a
# small alias table in loader.go for kinds that mirror another provider's
# wire shape). Nothing before this gate checked that the two sets AGREE.
#
# This reproduces this mission's own motivating P0 (spec §1.1):
# azure-openai and custom-openai were registered adapter kinds with NO
# capability-catalog entry, so Catalog.Describe fell into the
# unknown-provider branch and every tool-bearing request — the default
# chat path — was refused, and every image attachment was refused
# unconditionally. That defect shipped and was found only by a manual
# audit. THIS is the gate that would have caught it — its absence
# reproduces roadmap finding P-7 (a landed banner claiming a test that
# does not exist).
#
# The reverse direction catches the OTHER half: a catalog YAML file
# nothing resolves against (this is what made ollama.yaml a dead file
# before WP13 registered a real "ollama" adapter kind — E-001).
#
# WHY THIS IS A GO TOOL, NOT A GREP
# -----------------------------------
# Resolving `r.adapters[<pkg>.Kind]`'s <pkg> identifier to the actual
# registered STRING requires following registry.go's own import block to
# a package directory and reading that package's `const Kind = "..."` —
# a purely textual, per-line grep cannot see registry.go's call-site
# identifiers as the literal catalog keys. See
# scripts/ci/cmd/checkadaptercatalog/main.go's package doc for the full
# design, including how conditional registrations (three of the eight
# kinds are behind runtime feature flags — spec §13 item 5's stated
# hazard) are handled: the parser is deliberately blind to control flow,
# so a registration inside an `if` block is found exactly the same as an
# unconditional one.
#
# Violations must appear in
# scripts/ci/allowlists/i20-adapter-catalog-parity.txt with a DATED
# justification naming the blocker and owner — same contract as every
# other gate in this directory. Allowlists shrink monotonically; a line
# that no longer corresponds to a violation is STALE and fails the gate.
#
# Exit codes:
#   0 — every registered kind resolves to a catalog entry (direct or
#       aliased) and every catalog file is reachable from a registered
#       kind, modulo the allowlist; the allowlist has no stale entries.
#   1 — the checker itself failed to build or the scan found a
#       structural problem (zero adapters/files found, malformed alias
#       table, missing import, etc.) — a tool defect, not a finding.
#   2 — at least one real violation, or a stale allowlist entry.
#
# Usage: bash scripts/ci/check-adapter-catalog-parity.sh (from anywhere).

set -euo pipefail

WORKTREE_ROOT="$(git rev-parse --show-toplevel 2>/dev/null || pwd)"
cd "$WORKTREE_ROOT"

BIN="$(mktemp -d)/checkadaptercatalog"
trap 'rm -rf "$(dirname "$BIN")"' EXIT

echo "[adapter-catalog-parity] building checker..."
if ! go build -o "$BIN" ./scripts/ci/cmd/checkadaptercatalog/; then
  echo "[adapter-catalog-parity] FAIL: checkadaptercatalog failed to build." >&2
  exit 1
fi

echo "[adapter-catalog-parity] checking registry.go registrations against capabilities/data/*.yaml..."
"$BIN"
