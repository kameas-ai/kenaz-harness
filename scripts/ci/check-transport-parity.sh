#!/usr/bin/env bash
# check-transport-parity.sh — G-1
# (connector-lifecycle-truth-01PMZ303 UNIT-15, tasks.md §UNIT-15, spec.md §9)
#
# THE DEFECT CLASS
# -----------------
# `dispatch.Pool.closeOneByTag` fans a single logical operation
# (CloseOne) out across a switch statement, one case per transport tag
# ("stdio", "http", "sse", "inprocess"). Before connector-lifecycle-
# truth-01PMZ303 UNIT-6, the http/sse arms of this exact switch were
# COMMENT-ONLY ("http and sse pools do not yet expose a per-server
# CloseOne method") with no return statement, so execution fell through
# to the function's shared `return fmt.Errorf(...)` tail... except at
# the time, that tail was a bare `return nil` — reporting SUCCESS for a
# close that never happened, on every http/sse recipe. Nothing caught
# this: every case existed, every case was "registered" in the switch,
# and there was no gate that inspected what a case's BODY actually did.
#
# WHAT THIS CHECKS
# ----------------
# Parses `closeOneByTag`'s switch statement in
# core/mcp/dispatch/pool.go (or the overlay path below) and, for each
# known transport tag ("stdio", "http", "sse", "inprocess"), asserts
# its case block contains a real `.CloseOne(` call — not just a
# comment, not just a log line, not an empty body. A case whose block
# has NO `.CloseOne(` call fails the gate by name.
#
# THE HONEST LIMITATION (read this before assuming it generalises)
# ------------------------------------------------------------------
# This is Class B (per-variant-gap): everything here is registered
# and everything is consumed; only one ARM of a switch can be empty.
# No gate in this repo inspects arbitrary switch-arm bodies for
# parity against an interface method set — that is not a tractable
# general check without a real Go AST walk keyed on the specific
# interface (StdioSubPool minus coremcp.Pool), which this script does
# not attempt. This gate is hand-scoped to the ONE production switch
# this mission found broken (closeOneByTag) — it is not a template
# for "every switch over a transport tag is checked automatically."
# spec.md §1.8's 46-recipe primary_auth gap (frontend arm-coverage) is
# a SECOND instance of the same defect class this gate does not cover
# at all; claiming otherwise would be the vacuous-gate failure mode
# gates_can_fail_test.go exists to prevent.
#
# Planted-violation proof: scripts/ci/gates_can_fail_test.go
# "transport-parity/comment-only-arm-fails" — replaces the http case's
# `d.httpPool.CloseOne(ctx, id)` call with a bare comment, in a scratch
# copy, and asserts the gate rejects it.
#
# Usage: bash scripts/ci/check-transport-parity.sh
#        TRANSPORT_PARITY_POOL_GO=<path> bash scripts/ci/check-transport-parity.sh
#          (test-only override — points the gate at a scratch copy of
#          pool.go instead of the real one, so a planted violation
#          never touches the tracked file)

set -euo pipefail

source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib/ci-gate.sh"

GATE="[transport-parity]"
POOL_GO="${TRANSPORT_PARITY_POOL_GO:-core/mcp/dispatch/pool.go}"

if [[ -n "${TRANSPORT_PARITY_POOL_GO:-}" ]]; then
  if [[ ! -f "$POOL_GO" ]]; then
    echo "${GATE} FAIL: TRANSPORT_PARITY_POOL_GO='${POOL_GO}' does not exist." >&2
    exit 1
  fi
else
  ci_require_file "$POOL_GO" "$GATE"
fi

# Extract closeOneByTag's body: from its func line to the matching
# closing brace at column 0 (Go's gofmt puts a top-level func's closing
# `}` at column 0, and closeOneByTag is not a method with a nested
# same-indent brace of its own — verified against the real file).
FUNC_START="$(grep -n '^func (d \*Pool) closeOneByTag' "$POOL_GO" | head -1 | cut -d: -f1)"
if [[ -z "$FUNC_START" ]]; then
  echo "${GATE} FAIL: closeOneByTag not found in ${POOL_GO} — the function was renamed or moved." >&2
  echo "${GATE} Update this gate in the same commit that renames/moves it." >&2
  exit 1
fi
FUNC_END_OFFSET="$(tail -n "+$((FUNC_START + 1))" "$POOL_GO" | grep -n '^}' | head -1 | cut -d: -f1)"
if [[ -z "$FUNC_END_OFFSET" ]]; then
  echo "${GATE} FAIL: could not find closeOneByTag's closing brace in ${POOL_GO}." >&2
  exit 1
fi
FUNC_END=$((FUNC_START + FUNC_END_OFFSET))
BODY="$(sed -n "${FUNC_START},${FUNC_END}p" "$POOL_GO")"

# Derived input set: every `case "<tag>":` line inside the function body
# — not a hardcoded list, so a newly added transport tag is picked up
# automatically the next time this gate runs.
# NOTE ON THE TAB: `\t` inside a grep ERE is NOT portable. BSD grep
# (macOS) matches a literal tab; GNU grep (the Linux CI runners) treats
# `\t` as an escaped ordinary character, i.e. the letter `t`, so
# `^\tcase` looks for `^tcase` and matches NOTHING. That divergence made
# this gate hit its own discovery floor on CI while passing locally, and
# it broke the planted-violation proof rather than the gate's real pass —
# the gate was never vacuous, but only because the floor caught it.
# Use ANSI-C quoting so the pattern carries a real tab byte on both
# platforms, matching the form check-tool-containment-unconditional.sh:63
# already uses.
mapfile -t TAGS < <(echo "$BODY" | grep -oE $'^\tcase "[a-z]+":' | sed -E $'s/^\tcase "([a-z]+)":$/\\1/')

if [[ "${#TAGS[@]}" -eq 0 ]]; then
  echo "${GATE} FAIL: no \`case \"<tag>\":\` lines found in closeOneByTag — the switch shape changed;" >&2
  echo "${GATE} update this gate's extraction pattern in the same commit." >&2
  exit 1
fi

violations=0
for tag in "${TAGS[@]}"; do
  # Extract this one case's block: from its `case "tag":` line to the
  # next `case ` or `default:` line (or end of body).
  #
  # PORTABILITY (2026-09-15, third divergence in this gate): this used to
  # be an awk program fed via `echo "$BODY"`. On the Linux CI runner the
  # `inprocess` case (the switch's last) extracted as empty while macOS
  # extracted it correctly — same tree, opposite verdicts, the exact
  # platform-split shape finding #96 documents. Root cause not worth
  # adjudicating between awk dialects and echo's backslash handling:
  # the extraction is now grep -n line arithmetic + sed over
  # POSIX [[:blank:]] classes, and printf instead of echo, so there is
  # no dialect-divergent construct left to disagree.
  # Every no-match-capable grep below carries `|| true`: under
  # `set -euo pipefail` a zero-match grep otherwise kills the whole
  # script mid-loop with NO output — the last case has no successor, so
  # the next-case lookup legitimately matches nothing on every run.
  # Same class check-broker-topic-consumers.sh already fixed.
  case_start=$(printf '%s\n' "$BODY" | { /usr/bin/grep -nF "case \"$tag\":" || true; } | head -1 | cut -d: -f1)
  if [[ -z "$case_start" ]]; then
    echo "${GATE} FAIL: could not locate case \"$tag\" in closeOneByTag's body (extraction bug, not a parity verdict)." >&2
    violations=1
    continue
  fi
  next_rel=$(printf '%s\n' "$BODY" | tail -n "+$((case_start + 1))" \
    | { /usr/bin/grep -nE '^[[:blank:]]*(case "|default:)' || true; } | head -1 | cut -d: -f1)
  if [[ -n "$next_rel" ]]; then
    block=$(printf '%s\n' "$BODY" | sed -n "$((case_start + 1)),$((case_start + next_rel - 1))p")
  else
    block=$(printf '%s\n' "$BODY" | sed -n "$((case_start + 1)),\$p")
  fi
  # FOURTH divergence in this gate (2026-09-16, and the reason PR #354
  # failed on a tree that passed twice before): `grep -q` exits the
  # moment it matches, so printf can take SIGPIPE mid-write; under
  # pipefail the pipeline then reports 141 EVEN THOUGH grep matched,
  # and the FAIL branch prints a case body that visibly contains the
  # "missing" call. Timing-dependent, hence flaky. A bash substring
  # test has no processes and no pipe — nothing left to race.
  if [[ "$block" != *'.CloseOne(ctx, id)'* ]]; then
    echo "${GATE} FAIL: case \"${tag}\" in closeOneByTag has no real .CloseOne(ctx, id) call —" >&2
    echo "${GATE} it is comment-only, empty, or dispatches something else. This is exactly the" >&2
    echo "${GATE} pre-UNIT-6 http/sse shape: a registered arm that reports success (via the" >&2
    echo "${GATE} function's shared tail) without actually closing anything." >&2
    echo "${GATE}   case body:" >&2
    echo "$block" | sed "s/^/${GATE}     /" >&2
    violations=1
  fi
done

if [[ "$violations" -ne 0 ]]; then
  exit 1
fi

echo "${GATE} clean — all ${#TAGS[@]} transport tag(s) in closeOneByTag (${TAGS[*]}) delegate to a real CloseOne call."
