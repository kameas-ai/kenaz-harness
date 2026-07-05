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

# ---- Fleet capability-keys.ts parity gate ----
# Running go generate ./core/fleet/... regenerates frontend/src/lib/capability-keys.ts
# from the canonical AllCapabilities() slice. If a Go Capability constant is
# added without re-running the generator the committed TS file drifts.
echo "check-codegen: running 'go generate ./core/fleet/...' from ${REPO_ROOT}"
if ! go generate ./core/fleet/...; then
  echo "check-codegen: go generate ./core/fleet/... FAILED" >&2
  exit 2
fi

FLEET_TS_PATTERN='frontend/src/lib/capability-keys.ts'
UNTRACKED_FLEET=$(git ls-files --others --exclude-standard -- ${FLEET_TS_PATTERN})
if [[ -n "${UNTRACKED_FLEET}" ]]; then
  echo "check-codegen: UNTRACKED generated file detected:" >&2
  echo "${UNTRACKED_FLEET}" >&2
  echo "Run 'git add' and commit the generated capability-keys.ts." >&2
  exit 1
fi
# shellcheck disable=SC2086
if ! git diff --exit-code -- ${FLEET_TS_PATTERN}; then
  cat >&2 <<EOF

check-codegen: CAPABILITY-KEYS DRIFT DETECTED.

The committed frontend/src/lib/capability-keys.ts does not match what
'go generate ./core/fleet/...' produces now. This usually means a Go
Capability constant was added or renamed without regenerating the TS file.

Fix: run 'go generate ./core/fleet/...' locally and commit the diff above
alongside the Go change.

EOF
  exit 1
fi

# ---- wailsjs binding source → committed hash gate ----
#
# `wails generate module` requires the Wails toolchain (CGO + macOS/Windows
# SDK for the GUI layer) which is NOT available on the self-hosted kameas-ci-*
# ARM64 pool. Instead we gate drift via a committed SHA-256 hash of the
# binding-source files (core/rpc/bindings.go + core/rpc/bindings_*.go).
# When any of those source files change without a corresponding wailsjs regen
# the stored hash drifts and CI catches it.
#
# To update the hash (after running `wails generate module` locally and
# committing the regenerated frontend/wailsjs/ files):
#   bash scripts/ci/check-codegen.sh --update-wailsjs-hash
#   git add scripts/ci/wailsjs-bindings.sha256
#   git commit -m "chore(frontend): update wailsjs binding source hash"
#
# (p0-wiring-fixes-3TVMG0MX WP09)

WAILSJS_HASH_FILE="${REPO_ROOT}/scripts/ci/wailsjs-bindings.sha256"
WAILSJS_SOURCES="${REPO_ROOT}/core/rpc/bindings.go"
# Collect any split-file bindings_*.go additions. Order MUST be deterministic
# across platforms: shell glob expansion is locale-collated, and macOS vs the
# Linux CI runner order `.` and `_` differently (e.g. bindings_wails.go vs
# bindings_wails_serve.go), which would flip the concatenation and drift the
# hash. Force byte-wise (LC_ALL=C) sorting so the digest is platform-stable.
while IFS= read -r f; do
  [[ -f "${f}" ]] && WAILSJS_SOURCES="${WAILSJS_SOURCES} ${f}"
done < <(LC_ALL=C ls -1 "${REPO_ROOT}"/core/rpc/bindings_*.go 2>/dev/null | LC_ALL=C sort)

# Portable SHA-256: self-hosted Linux runners ship `sha256sum`; macOS dev boxes
# ship `shasum`. Both emit the identical SHA-256 digest, so the committed hash
# stays valid across platforms. (shasum-only broke CI on the Linux ARM64 pool.)
if command -v sha256sum >/dev/null 2>&1; then
  _wailsjs_sha() { sha256sum | awk '{print $1}'; }
else
  _wailsjs_sha() { shasum -a 256 | awk '{print $1}'; }
fi

if [[ "${1:-}" == "--update-wailsjs-hash" ]]; then
  COMPUTED=$(cat ${WAILSJS_SOURCES} | _wailsjs_sha)
  echo "${COMPUTED}" > "${WAILSJS_HASH_FILE}"
  echo "check-codegen: wailsjs binding source hash updated to ${COMPUTED}"
  echo "check-codegen: OK — generated files match"
  exit 0
fi

if [[ -f "${WAILSJS_HASH_FILE}" ]]; then
  COMMITTED_HASH=$(cat "${WAILSJS_HASH_FILE}")
  CURRENT_HASH=$(cat ${WAILSJS_SOURCES} | _wailsjs_sha)
  if [[ "${COMMITTED_HASH}" != "${CURRENT_HASH}" ]]; then
    cat >&2 <<EOF

check-codegen: WAILSJS DRIFT DETECTED.

The binding source (core/rpc/bindings.go) has changed since the last
time the wailsjs files were regenerated. The committed files under
frontend/wailsjs/go/rpc/Bindings.{js,d.ts} and
frontend/wailsjs/go/models.ts may be stale.

Fix:
  1. Run 'wails generate module' on a dev machine (needs the Wails
     toolchain — macOS or Linux with GTK installed).
  2. Commit the updated frontend/wailsjs/ files.
  3. Update the hash: bash scripts/ci/check-codegen.sh --update-wailsjs-hash
  4. Commit scripts/ci/wailsjs-bindings.sha256 alongside the regen.

Committed hash : ${COMMITTED_HASH}
Current hash   : ${CURRENT_HASH}

EOF
    exit 1
  fi
  echo "check-codegen: wailsjs binding source hash OK (${CURRENT_HASH})"
else
  echo "check-codegen: WARN — ${WAILSJS_HASH_FILE} not found; skipping wailsjs hash gate"
fi

echo "check-codegen: OK — generated files match"
