#!/usr/bin/env bash
# check-declared-output-ports.sh — I16: every manifest-declared output
# port on a CONCRETE node kind must have a Go writer somewhere in
# core/agentgraph's executors (approval-node-01PMZC12 UNIT-9, G-b).
#
# check-output-ports.sh (I-existing) walks WRITE -> READER: every
# Outputs["k"] a Go executor writes must be read by something. This
# script walks the reverse direction, which nothing else covers:
# DECLARED -> WRITER. A manifest can promise a port
# (`outputs: [{name: rejected, ...}]`) that no executor ever writes —
# the exact shape approval.yaml's `rejected` port was in until UNIT-4:
# it type-checked in the editor, the canvas drew the edge, and nothing
# ever traversed it. docs/dead-code-audit-2026-08-18.md:775 calls this
# class out explicitly and names it as one of only two instances
# repo-wide at the time of that audit.
#
# Scope: CONCRETE node kinds only — manifests with an `id:` and an
# `executor:` field. The `_archetype.*.yaml` files (compute/control/
# read/tool/write/state/marker) declare a port surface too
# (e.g. _archetype.compute.yaml's `output`), but archetypes are never
# instantiated on their own (`callable: false`, no `executor:`), so
# there is no Go executor for a bare archetype to write from in the
# first place — flagging them would be a repo-wide false-positive
# generator on day one.
#
# False-positive source this script must NOT trip on (spec.md G-b):
# ports_gen.go's generated `ToPortValues()` method writes every
# declared output port of a kind unconditionally
# (`pv["rejected"] = o.Rejected`), regardless of whether the executor
# itself ever populates the field. That is codegen plumbing, not
# evidence of a real writer — ports_gen.go is excluded from the writer
# scan entirely, the same way check-output-ports.sh's own methodology
# note (header) warns a naive Go-only grep over-reports ~3x.
#
# The writer scan matches "<ident>[\"<port>\"] =" for ANY identifier,
# not only the literal `Outputs`  — e.g. tool_dispatch's
# `assistant_text` is written as `out["assistant_text"] = m.Content`
# inside passthroughAssistant(in, out PortValues), where `out` is
# `res.Outputs` passed under a helper-local name. Requiring the exact
# receiver name `Outputs` produced a false positive here on the first
# real run against this repo — matching check-output-ports.sh's own
# documented risk asymmetry (its header): "a live port wrongly flagged
# as dead is a worse outcome" than under-reporting, which this script
# inherits for the reverse direction.
#
# Exit codes:
#   0 — every declared output port on a concrete kind has a writer, or
#       is allowlisted with a dated justification
#   2 — at least one declared port has no writer and no allowlist entry
#
# Allowlist: scripts/ci/allowlists/i16-declared-unwritten-output-ports.txt
# (one `<kind>.<port>` per line; '#' starts a comment). Allowlists
# shrink monotonically — nothing gets added without a date and an
# owner naming the blocker.

set -euo pipefail

# ci-gate.sh resolves the repo root from THIS FILE's own location and
# cd's there — robust from any cwd, including one entirely outside the
# git tree (TestGates_VerdictIsIndependentOfWorkingDirectory's
# t.TempDir() case). `git rev-parse --show-toplevel`, the pattern
# check-output-ports.sh (this gate's write->read sibling) uses, is NOT
# cwd-independent in that sense — see check-broker-topic-consumers.sh's
# header for the same note against the same pattern. New gates use this
# library rather than repeat that gap.
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib/ci-gate.sh"

GATE="[declared-output-ports]"

MANIFEST_DIR="core/agentgraph/nodes/manifests"
AG_DIR="core/agentgraph"
ALLOWLIST="scripts/ci/allowlists/i16-declared-unwritten-output-ports.txt"

ci_require_dir "$MANIFEST_DIR" "$GATE"
ci_require_dir "$AG_DIR" "$GATE"

# Non-test, non-generated Go source under core/agentgraph — the
# executors. ports_gen.go is EXCLUDED (see the false-positive note
# above); attrs_gen.go / wire_gen.go / manifest_versions_gen.go carry
# no Outputs[...] writes and are excluded for the same reason
# check-output-ports.sh restricts its own scan to hand-written files.
mapfile -t GO_FILES < <(find "$AG_DIR" -maxdepth 1 -name '*.go' \
  ! -name '*_test.go' ! -name 'ports_gen.go' | sort)

if [[ ${#GO_FILES[@]} -eq 0 ]]; then
  echo "[declared-output-ports] no executor Go files found under $AG_DIR — nothing to check (unexpected)." >&2
  exit 2
fi

# Load the allowlist (kind.port per line, '#' comments, blank lines OK).
declare -A ALLOWED=()
if [[ -f "$ALLOWLIST" ]]; then
  while IFS= read -r line; do
    line="${line%%#*}"
    line="$(echo "$line" | tr -d '[:space:]')"
    [[ -z "$line" ]] && continue
    ALLOWED["$line"]=1
  done < "$ALLOWLIST"
fi

fail=0
checked=0

for manifest in "$MANIFEST_DIR"/*.yaml; do
  base="$(basename "$manifest")"
  # Archetypes: filename convention AND no `executor:` field, checked
  # both ways so a rename of one without the other cannot silently
  # exempt a real kind.
  if [[ "$base" == _archetype.* ]]; then
    continue
  fi
  if ! grep -q '^executor:' "$manifest"; then
    continue
  fi
  kind="$(grep -m1 '^id:' "$manifest" | awk '{print $2}')"
  if [[ -z "$kind" ]]; then
    echo "[declared-output-ports] $manifest has an executor: but no id: — cannot attribute its ports; fix the manifest." >&2
    fail=1
    continue
  fi

  # Extract the `outputs:` block under `ports:` and pull every `name:`
  # value out of it. Mirrors check-output-ports.sh's own approach of
  # reading the manifest text directly rather than depending on
  # codegen output, so this gate does not need the generated files to
  # be in sync with the manifests to run.
  mapfile -t PORT_NAMES < <(awk '
    /^ports:/ { in_ports=1; next }
    in_ports && /^[a-z_]+:/ && !/^  /  { in_ports=0 }
    in_ports && /^  outputs:/ { in_outputs=1; next }
    in_ports && in_outputs && /^  [a-z_]+:/ && !/^    / { in_outputs=0 }
    in_ports && in_outputs { print }
  ' "$manifest" | grep -oE 'name:[[:space:]]*[a-zA-Z_][a-zA-Z0-9_]*' | sed -E 's/name:[[:space:]]*//')

  for port in "${PORT_NAMES[@]}"; do
    checked=$((checked + 1))
    key="${kind}.${port}"

    if grep -qE "[A-Za-z_][A-Za-z0-9_]*\[\"${port}\"\][[:space:]]*=" "${GO_FILES[@]}" 2>/dev/null; then
      continue
    fi

    if [[ -n "${ALLOWED[$key]:-}" ]]; then
      continue
    fi

    fail=1
    echo "" >&2
    echo "[declared-output-ports] FAIL: kind \"${kind}\" declares output port \"${port}\" ($manifest) with no Go writer (Outputs[\"${port}\"] = ...) anywhere in ${AG_DIR}'s non-test, non-generated executors." >&2
    echo "  Fix: write the port from the executor, or add \"${key}\" to ${ALLOWLIST} with a dated justification naming the blocker and owner." >&2
  done
done

if [[ $fail -ne 0 ]]; then
  echo "" >&2
  echo "[declared-output-ports] FAIL — see offending ports above." >&2
  exit 2
fi

echo "[declared-output-ports] clean — every declared output port on a concrete kind has a writer or an allowlist entry (${checked} ports checked)."
exit 0
