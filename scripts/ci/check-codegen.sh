#!/usr/bin/env bash
# check-codegen.sh — fail CI when committed *_gen.go files drift from
# what `go generate ./...` would produce right now.
#
# Mission: agent-kernel-graph-node-catalog-01KQ7JDZ, WP02
# FRs: FR-009 (idempotency), NFR-004 (CI gate).
#
# Usage:
#   scripts/ci/check-codegen.sh
#
# Exit codes:
#   0 — generated files match what go generate produces
#   1 — drift detected; the diff is printed so the maintainer can
#       commit the regenerated files
#   2 — go generate itself failed (build error, missing manifest, etc.)
#
# The script is intentionally pure-shell so it runs identically on
# developer laptops and CI. We assume `go` is on PATH; everything
# else is stdlib.

set -euo pipefail

# Resolve repo root (works whether the script is invoked from the
# repo root or from inside scripts/ci/).
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
cd "${REPO_ROOT}"

echo "check-codegen: running 'go generate ./core/agentgraph/...' from ${REPO_ROOT}"
if ! go generate ./core/agentgraph/...; then
  echo "check-codegen: go generate FAILED" >&2
  exit 2
fi

# git diff --exit-code returns 0 when there is no diff, 1 when there is.
# We narrow the diff to the generated files so unrelated working-tree
# changes do not pollute the gate. Also flag untracked _gen.go files
# (they would be invisible to a plain `git diff`).
#
# The three generated files that must stay in sync:
#   - core/agentgraph/attrs_gen.go   (per-kind *Attrs structs)
#   - core/agentgraph/wire_gen.go    (NodeKind* constants, decoders)
#   - core/agentgraph/ports_gen.go   (typed port structs + accessors)
GENERATED_PATTERN='core/agentgraph/*_gen.go'
UNTRACKED_GEN=$(git ls-files --others --exclude-standard -- ${GENERATED_PATTERN})
if [[ -n "${UNTRACKED_GEN}" ]]; then
  echo "check-codegen: UNTRACKED generated files detected:" >&2
  echo "${UNTRACKED_GEN}" >&2
  echo "Run 'git add' on the listed files (and the manifest that triggered them)." >&2
  exit 1
fi
# shellcheck disable=SC2086
if ! git diff --exit-code -- ${GENERATED_PATTERN}; then
  cat >&2 <<EOF

check-codegen: DRIFT DETECTED.

The committed *_gen.go files do not match what 'go generate ./...'
would produce now. This usually means:

  1. A manifest under core/agentgraph/nodes/manifests/ was edited
     without re-running 'go generate ./...'.
  2. The generator template at core/agentgraph/nodes/cmd/gen/ was
     updated without committing the regenerated output.

The gate covers three generated files:
  - core/agentgraph/attrs_gen.go  (per-kind *Attrs structs)
  - core/agentgraph/wire_gen.go   (NodeKind* constants, decoders)
  - core/agentgraph/ports_gen.go  (typed port structs + accessors)

Fix: run 'go generate ./core/agentgraph/...' locally and commit the
diff above as part of the same change that touched the manifest /
generator.

EOF
  exit 1
fi

echo "check-codegen: OK — generated files match"
