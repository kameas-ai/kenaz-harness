#!/usr/bin/env bash
# check-onboarding-starter-tools.sh — FR-007
# (first-run-onboarding-01PMOB01 WP04): no shipped onboarding starter
# prompt may name a harness_* tool that register.go does not actually
# register.
#
# THE DEFECT CLASS
# -----------------
# code.md:23 instructed the onboarding model to call
# harness_write_propose_cedar_policy — a tool the 2026-08-14 unwired sweep
# deleted from register.go. Nothing caught it: the starter prompt is plain
# markdown, no Go compiler ever looks at it, and (per the mission's finding
# A9) delivery was itself dead at the time, so the lie was inert rather than
# model-visible. Once delivery is wired (WP02), this class stops being
# harmless — a starter's SystemPrompt is a promise made directly to the
# model on the user's first turn.
#
# WHAT THIS CHECKS
# ----------------
# Every harness_[a-z_]+ token found in
# core/mcp/builtin/harness/onboarding/*.md must be one of the tool-name
# string literals register.go declares (its `ToolXxx = "harness_..."`
# const block — read the literal values directly rather than trying to
# resolve `Name: ToolXxx` through a Go const, so this gate needs no Go
# toolchain). No allowlist: FR-001/FR-007 give this class zero tolerance —
# a starter prompt either names a real tool or it doesn't ship.
#
# SCOPE (C-007) — read before "fixing" a false negative
# -------------------------------------------------------
# This gate only ever sees the SHIPPED starters under
# core/mcp/builtin/harness/onboarding/*.md. `<DataDir>/onboarding/prompts/*.md`
# user overrides (starters.go's resolver merges them over the embedded set)
# are user-authored content outside CI's reach by construction. A user can
# name any tool they like in their own override; that is a runtime risk
# they own, not a gap in this gate. Do not read a green run of this script
# as a guarantee about user-authored starters.
#
# Usage: bash scripts/ci/check-onboarding-starter-tools.sh (from anywhere).

set -euo pipefail

source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib/ci-gate.sh"

GATE="[onboarding-starter-tools]"
PROMPTS_DIR="core/mcp/builtin/harness/onboarding"
REGISTER_FILE="core/mcp/builtin/harness/register.go"

ci_require_dir "$PROMPTS_DIR" "$GATE"
ci_require_file "$REGISTER_FILE" "$GATE"

# ---- 1. the registered tool-name set ----
registered=$(grep -oE '"harness_[a-z_]+"' "$REGISTER_FILE" | tr -d '"' | LC_ALL=C sort -u)

if [[ -z "$registered" ]]; then
  echo "${GATE} FAIL: found no harness_* tool-name declarations in ${REGISTER_FILE}." >&2
  echo "${GATE} A gate cannot pass by having nothing to look at — the naming convention" >&2
  echo "${GATE} this scan depends on has changed. Update the pattern in the same commit." >&2
  exit 1
fi

# ---- 2. every harness_* token named in a shipped starter ----
named=$(grep -ohE 'harness_[a-z_]+' "$PROMPTS_DIR"/*.md 2>/dev/null | LC_ALL=C sort -u || true)

# ---- 3. names in the prompts that are not in the registered set ----
fail=0
unregistered=""
if [[ -n "$named" ]]; then
  unregistered=$(comm -23 <(printf '%s\n' "$named") <(printf '%s\n' "$registered") || true)
fi

if [[ -n "$unregistered" ]]; then
  echo "" >&2
  echo "${GATE} FAIL: starter prompt(s) under ${PROMPTS_DIR} name a harness_* tool" >&2
  echo "${GATE} that ${REGISTER_FILE} does not register:" >&2
  printf '%s\n' "$unregistered" | sed 's/^/    /' >&2
  echo "" >&2
  echo "${GATE} A system prompt naming a tool the model cannot call is a promise the" >&2
  echo "${GATE} product breaks on the user's first turn. Remove the name or register the tool." >&2
  fail=1
fi

if [[ "$fail" -ne 0 ]]; then
  echo "" >&2
  echo "${GATE} FAIL — see violations above." >&2
  exit 1
fi

echo "${GATE} clean — every harness_* name in ${PROMPTS_DIR}/*.md is registered in ${REGISTER_FILE}."
echo "${GATE} NOTE (C-007): covers the SHIPPED starter set only — <DataDir>/onboarding/prompts/*.md"
echo "${GATE} user overrides are outside CI's reach by design; this is not a runtime guarantee for those."
