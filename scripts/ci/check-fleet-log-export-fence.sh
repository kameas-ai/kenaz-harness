#!/usr/bin/env bash
# check-fleet-log-export-fence.sh
#
# Guards the fleet log lane's egress fence so it cannot be removed quietly.
#
# Background: the harness once wired its whole slog stream into an OTel
# LoggerProvider exporting to Fleet's /v1/logs. Application log bodies are
# content-bearing (file paths, error strings, identifiers), nothing in any
# Kameas repo tags log records with kameas.event.kind, and the receiver
# discards untagged records — but only AFTER they have crossed the network and
# terminated TLS on Kameas infrastructure. Constitution §IX does not permit
# that, and "the far end drops it" is not a safety property.
#
# The fence has three parts. Each is checked here.
#
#   1. A compiled CEILING of exportable kinds (core/fleet/log_event_kind.go +
#      the vendored core/fleet/schema/). Not remotely widenable — Fleet can
#      narrow via opt-ins, never widen (constitution §XII condition 6:
#      authentication is not the boundary).
#   2. Regression tests that assert on bytes leaving the process.
#   3. No OTLP *log* exporter is constructed anywhere without an explicit
#      opt-out annotation, so re-opening the lane is a visible act in review.
#
# Usage: bash scripts/ci/check-fleet-log-export-fence.sh

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$REPO_ROOT"

fail=0

# The fork case: a checkout with core/fleet/ removed has no lane to fence.
if [[ ! -d core/fleet ]]; then
  echo "[log-fence] core/fleet/ absent (fork case) — nothing to check."
  exit 0
fi

# ── 1. the ceiling exists ────────────────────────────────────────────────────

for f in \
  core/fleet/log_event_kind.go \
  core/fleet/schema/kinds.json \
  core/fleet/schema/source.json \
  core/fleet/log_event_kind_parity_test.go
do
  if [[ ! -f "$f" ]]; then
    echo "[log-fence] FAIL: $f is missing — the compiled kind ceiling or its anti-drift gate was removed." >&2
    fail=1
  fi
done

# The intersection must be written out, not emergent.
if [[ -f core/fleet/log_event_kind.go ]] && ! grep -q 'func LogKindsAdmittedBy' core/fleet/log_event_kind.go; then
  echo "[log-fence] FAIL: LogKindsAdmittedBy (ceiling ∩ Fleet opt-ins) is gone from core/fleet/log_event_kind.go." >&2
  fail=1
fi

# ── 2. the regression tests exist ────────────────────────────────────────────

REQUIRED_TESTS=(
  TestFleetLogLane_PlainSlogLineNeverLeavesTheProcess
  TestKindGatedLogExporter_AdmitsOnlyAllowlistedKinds
  TestKindGatedLogExporter_AllDroppedMeansNoRequest
  TestFleetCannotWidenTheCompiledCeiling
  TestNoOptInSnapshotAdmitsNothing
  TestVendoredKindSchemaMatchesFleetSource
)

for t in "${REQUIRED_TESTS[@]}"; do
  if ! grep -rq "func ${t}(" core/fleet/; then
    echo "[log-fence] FAIL: required regression test ${t} not found under core/fleet/." >&2
    echo "            This test is the fence's proof. Do not delete it; if it must be" >&2
    echo "            renamed, rename it here in the same commit." >&2
    fail=1
  fi
done

# ── 3. no un-annotated OTLP log exporter ─────────────────────────────────────
#
# otlploghttp/otlploggrpc construct the network path that ships log records.
# Re-introducing one means re-opening the lane, which requires solving the
# missing-kameas.user.id resource problem first (Fleet 401s the whole batch
# otherwise) AND having something that emits kind-tagged records. Annotate the
# line with `fleet-log-fence-allow: <reason>` to opt out deliberately.

# Match the CONSTRUCTOR only — otlploghttp.New( / otlploggrpc.New( — and skip
# comment lines, so prose about the fence does not trip its own gate.
matches=$(grep -rnE '^[^/]*otlplog(http|grpc)\.New\(' --include='*.go' core/fleet/ 2>/dev/null || true)
if [[ -n "$matches" ]]; then
  while IFS= read -r line; do
    [[ -z "$line" ]] && continue
    file="${line%%:*}"
    rest="${line#*:}"
    lineno="${rest%%:*}"
    # Allow if the line itself, or the two lines directly above it, carries
    # the annotation (import blocks put the comment above the import). Keep
    # the window tight: a wide window lets unrelated prose about the fence
    # accidentally authorise a real call site.
    start=$(( lineno > 2 ? lineno - 2 : 1 ))
    if sed -n "${start},${lineno}p" "$file" | grep -q 'fleet-log-fence-allow:'; then
      continue
    fi
    echo "[log-fence] FAIL: un-annotated OTLP log exporter reference: $line" >&2
    echo "            Shipping harness log records to Fleet is off by design." >&2
    echo "            If you mean to re-enable it, add a same-block comment:" >&2
    echo "              // fleet-log-fence-allow: <why, and how the resource + kind gaps are solved>" >&2
    fail=1
  done <<< "$matches"
fi

if [[ $fail -ne 0 ]]; then
  echo "[log-fence] fleet log-export fence check FAILED." >&2
  exit 1
fi

echo "[log-fence] clean — the fleet log-export fence is intact."
