#!/usr/bin/env bash
# check-output-ports.sh — every agentgraph node Outputs["k"] write must be
# read somewhere: by Go, by a shipped/tested YAML graph, by the frontend,
# or carry a //wiring:deferred(<reason>) directive (wiring-integrity-
# 01PMAG04 WP05, spec §3.2 item 3 / FR-003).
#
# THE METHODOLOGY IS THE POINT — read docs/wiring-audit.md's "Re-run
# methodology" section before touching this file. The 2026-07-27 audit's
# raw Go-only grep for write-only output ports returned 18 hits; only 6
# were real dead ports. The other 12 were read through one of two
# channels a naive grep cannot see:
#
#   1. A YAML `condition:` expression. evalBranchExpr does a dynamic
#      key lookup against the port-values map at runtime — a graph
#      author writing `condition: "should_replan == true"` IS a read of
#      Outputs["should_replan"], but no Go symbol references that
#      string anywhere.
#   2. The frontend. Several ports are consumed only by
#      frontend/src/** (TypeScript/Vue), invisible to a Go-repo grep.
#
# This script reproduces all three passes (Go / YAML / frontend). A
# two-pass version reintroduces the 3x false-positive rate — the first
# person to "clean up" the noise deletes live code (the exact failure
# FR-005 exists to prevent). See also the CAUTION comment below about
# generic-English-word keys: this script is deliberately biased toward
# under-reporting (treating an ambiguous key as "covered") over
# over-reporting, because a live port wrongly flagged as dead is a
# worse outcome than a genuinely-dead port slipping through — the same
# risk-asymmetry FR-005 states for deletions generally.
#
# Exit codes:
#   0 — every Outputs["k"] write is read or explicitly deferred
#   2 — at least one write has neither a reader nor a directive
#
set -euo pipefail

WORKTREE_ROOT="$(git rev-parse --show-toplevel 2>/dev/null || pwd)"
cd "$WORKTREE_ROOT"

AG_DIR="core/agentgraph"

# YAML_DIRS — where pass 2 looks for a graph that reads a port.
#
# SHIPPED graphs and activities only. core/agentgraph/testdata was in this
# list and was REMOVED by agentgraph-total-convergence-01PMGX01 WP17
# (reachability review N9): it is a laundering vector. Anyone can make a
# dead output port look live by adding the key to a test fixture, and the
# gate's whole claim is that something READS the port — a fixture that
# merely mentions it proves nothing about production, which is the same
# confusion I3 exists to prevent on the node-kind side.
#
# The honest escape hatch for a port with no reader is the
# //wiring:deferred(<reason>) directive, which is reviewable at the write
# site. Verified green after the removal, so nothing was relying on it.
YAML_DIRS=(
  "core/rpc/views/agentgraph/library"
  "core/agentgraph/activities/yaml"
)

# --- Optional report mode: prints the raw (Go-only) vs adjusted
# (Go+YAML+frontend+directive) hit counts, mirroring spec §1.1's
# "18 raw, 6 real" table. Informational only; does not affect exit code.
REPORT_MODE=0
if [[ "${1:-}" == "--report" ]]; then
  REPORT_MODE=1
fi

# Non-test Go source files directly under core/agentgraph (the
# executors that write Outputs[...]).
mapfile -t GO_FILES < <(find "$AG_DIR" -maxdepth 1 -name '*.go' ! -name '*_test.go' | sort)

# All write sites: "<file>:<line>:<key>".
mapfile -t WRITE_LINES < <(
  grep -n -E 'Outputs\["[a-zA-Z_][a-zA-Z0-9_]*"\]\s*=' "${GO_FILES[@]}" \
    | sed -E 's/(^[^:]+:[0-9]+):.*Outputs\["([a-zA-Z_0-9]+)"\].*/\1:\2/'
)

if [[ ${#WRITE_LINES[@]} -eq 0 ]]; then
  echo "[output-ports] no Outputs[...] writes found — nothing to check (unexpected; verify the glob)." >&2
  exit 2
fi

# Distinct keys.
mapfile -t KEYS < <(printf '%s\n' "${WRITE_LINES[@]}" | sed -E 's/^[^:]+:[0-9]+://' | sort -u)

raw_dead=0
adjusted_dead=0
fail=0

for key in "${KEYS[@]}"; do
  # --- directive coverage: does any write site for this key carry a
  # //wiring:deferred(...) directive on the immediately preceding line?
  directive_hit=0
  while IFS= read -r wl; do
    f="${wl%%:*}"
    rest="${wl#*:}"
    ln="${rest%%:*}"
    prev=$((ln - 1))
    # [[:space:]] not \s (BSD grep on macOS dev boxes doesn't support
    # \s in -E mode; POSIX classes work everywhere). Greedy .+ (not
    # [^)]+) so a reason that itself contains parentheses still matches
    # through to the final closing paren on the line.
    if [[ $prev -ge 1 ]] && sed -n "${prev}p" "$f" | grep -qE '^[[:space:]]*//[[:space:]]*wiring:deferred\(.+\)[[:space:]]*$'; then
      directive_hit=1
      break
    fi
  done < <(printf '%s\n' "${WRITE_LINES[@]}" | grep -E ":${key}\$" || true)

  # --- pass 1: Go read. Count every quoted "<key>" occurrence in
  # non-test Go source across the whole repo (broader than just
  # agentgraph — RPC/chat-view code may read a run's Outputs bag too),
  # then subtract the write-assignment occurrences. Any remainder means
  # something reads the key (inputs.GetString("<key>"), a bare
  # Outputs["<key>"] read, a condition-string builder, etc).
  go_total=$(grep -rn "\"${key}\"" --include='*.go' core 2>/dev/null | grep -v '_test\.go' | wc -l | tr -d ' ')
  go_writes=$(grep -rn "Outputs\[\"${key}\"\]\s*=" --include='*.go' core 2>/dev/null | grep -v '_test\.go' | wc -l | tr -d ' ')
  go_read=0
  if [[ "$go_total" -gt "$go_writes" ]]; then
    go_read=1
  fi

  # --- pass 2: YAML read. A shipped/tested graph references the key —
  # as an edge port name, an attr, or inside a condition: string.
  yaml_read=0
  for d in "${YAML_DIRS[@]}"; do
    if [[ -d "$d" ]] && grep -rlq "\<${key}\>" "$d" --include='*.yaml' 2>/dev/null; then
      yaml_read=1
      break
    fi
  done

  # --- pass 3: frontend read. TypeScript/Vue references the key as a
  # quoted string (port name, event payload field, etc).
  frontend_read=0
  if [[ -d frontend/src ]] && grep -rlq "['\"]${key}['\"]" frontend/src --include='*.ts' --include='*.vue' 2>/dev/null; then
    frontend_read=1
  fi

  covered=0
  if [[ $directive_hit -eq 1 || $go_read -eq 1 || $yaml_read -eq 1 || $frontend_read -eq 1 ]]; then
    covered=1
  fi

  if [[ $go_read -eq 0 ]]; then
    raw_dead=$((raw_dead + 1))
  fi
  if [[ $covered -eq 0 ]]; then
    adjusted_dead=$((adjusted_dead + 1))
  fi

  if [[ $REPORT_MODE -eq 1 ]]; then
    printf '  %-20s go=%d yaml=%d frontend=%d directive=%d -> %s\n' \
      "$key" "$go_read" "$yaml_read" "$frontend_read" "$directive_hit" \
      "$([[ $covered -eq 1 ]] && echo covered || echo DEAD)"
  fi

  if [[ $covered -eq 0 ]]; then
    fail=1
    echo "" >&2
    echo "[output-ports] FAIL: Outputs[\"${key}\"] has no Go reader, no reference in a shipped/tested YAML graph, no frontend reader, and no //wiring:deferred directive." >&2
    echo "  Write site(s):" >&2
    printf '%s\n' "${WRITE_LINES[@]}" | grep -E ":${key}\$" | sed -E 's/^([^:]+):([0-9]+):.*/    \1:\2/' >&2
    echo "  Fix: wire a real reader, or add a //wiring:deferred(<reason>) comment on the line immediately above the Outputs[\"${key}\"] write — see docs/wiring-audit.md." >&2
  fi
done

if [[ $REPORT_MODE -eq 1 ]]; then
  echo ""
  echo "[output-ports] raw (Go-only) dead: ${raw_dead} / ${#KEYS[@]}   adjusted (Go+YAML+frontend+directive) dead: ${adjusted_dead} / ${#KEYS[@]}"
fi

if [[ $fail -ne 0 ]]; then
  echo "" >&2
  echo "[output-ports] FAIL — see offending ports above." >&2
  exit 2
fi

echo "[output-ports] clean — every Outputs[...] write is read or explicitly deferred (${#KEYS[@]} distinct ports checked)."
exit 0
