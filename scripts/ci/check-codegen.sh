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
# binding SURFACE. When it changes without a corresponding wailsjs regen the
# stored hash drifts and CI catches it.
#
# To update the hash (after running `wails generate module` locally and
# committing the regenerated frontend/wailsjs/ files):
#   bash scripts/ci/check-codegen.sh --update-wailsjs-hash
#   git add scripts/ci/wailsjs-bindings.sha256
#   git commit -m "chore(frontend): update wailsjs binding source hash"
#
# (p0-wiring-fixes-3TVMG0MX WP09)
#
# NOTE on the 01PMGX01 WP17 baseline bump: the committed hash was
# recomputed in that commit because the INPUT DEFINITION widened (see
# below), not because anything was regenerated. frontend/wailsjs/ is
# byte-identical across that change.
#
# ---- WHAT "SURFACE" MEANS, AND WHY IT IS NOT JUST bindings.go ----
#
# Until 01PMGX01 WP17 the hash input was ONLY core/rpc/bindings.go (plus any
# bindings_*.go). That is the method surface — the names and signatures. It is
# not what `wails generate module` reads.
#
# The generator also walks the TYPES those signatures mention and emits one
# TypeScript class per struct into frontend/wailsjs/go/models.ts (57 namespaces
# at time of writing). So editing a bound struct in its own package changed
# models.ts and did NOT change bindings.go — the hash held, CI passed, and the
# committed models.ts silently went stale. That is exactly how
# `elicitation.Question` drifted: the struct lives in core/elicitation, nowhere
# near core/rpc/bindings.go.
#
# The hash input is therefore bindings.go + bindings_*.go PLUS the declaration
# block of every `pkg.Type` named in a Bindings method signature, resolved
# through bindings.go's own import block. Precision is deliberate: we hash the
# `type X struct { ... }` BLOCK, not the whole file it lives in, so adding an
# unexported helper next to a bound type does not flip the hash. A gate that
# cries wolf gets `--update-wailsjs-hash` run reflexively, which is worse than
# no gate.
#
# LIMITS, stated so nobody mistakes this for completeness:
#
#   1. ONE LEVEL DEEP. A struct nested inside a bound type, declared in a
#      DIFFERENT file, is not followed. In practice nested types usually sit
#      beside their parent and are picked up by the same block scan, but that
#      is a tendency, not a guarantee.
#   2. Text-level, not type-checked. It matches `type <Name> ` at column 0 in
#      the package directory named by the import alias. A type declared inside
#      a function, or via a type alias chain, is missed.
#   3. Only structs reachable from a `func (b *Bindings)` signature line.
#      Types reached exclusively through an interface's method set are not.
#
# The real fix is running the generator in CI, which needs a self-hosted Linux
# image with the Wails toolchain (tracked in CLAUDE.md's runner-policy notes).
# Until that exists this is a strictly better approximation than hashing the
# method list alone, and its blind spots are the three above.

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

# ---- bound-type declaration blocks (see "WHAT SURFACE MEANS" above) ----
#
# Driven FROM the committed models.ts, not from bindings.go's imports.
# models.ts is the generator's own record of every type it emitted:
#
#   export namespace elicitation {
#     export class Question { ... }
#
# So for each (namespace, class) pair we locate package directories whose
# basename is the namespace and extract that package's `type <Class> ...`
# declaration block. Every type the generator actually emitted is covered,
# at any nesting depth, without parsing import aliases — which is what
# matters, since the drift that motivated this
# (`elicitation.Question`, reached only as a FIELD of a bound type) is
# invisible to a one-level walk of bindings.go's signatures.
#
# Ambiguous basenames (e.g. `mcp` = core/mcp and core/rpc/views/mcp) are
# resolved by including the block from every matching directory. Slightly
# over-inclusive, fully deterministic.
#
# Everything is LC_ALL=C sorted so the digest is byte-identical on macOS
# and on the Linux ARM64 CI pool.
WAILSJS_TYPES_FILE="$(mktemp)"
trap 'rm -f "${WAILSJS_TYPES_FILE}"' EXIT

WAILSJS_MODELS="${REPO_ROOT}/frontend/wailsjs/go/models.ts"

_wailsjs_collect_types() {
  [[ -f "${WAILSJS_MODELS}" ]] || return 0

  local pkgindex
  pkgindex="$(mktemp)"
  # shellcheck disable=SC2064
  trap "rm -f '${pkgindex}'" RETURN

  # basename -> directory index over the Go tree (one line per package dir
  # that contains at least one non-test .go file).
  while IFS= read -r d; do
    echo "${d##*/} ${d}"
  done < <(LC_ALL=C find "${REPO_ROOT}/core" "${REPO_ROOT}/cmd" -type d 2>/dev/null | LC_ALL=C sort) \
    > "${pkgindex}"

  # (namespace, class) pairs, in models.ts order, normalised and sorted.
  local ns cls line
  ns=""
  while IFS= read -r line; do
    case "${line}" in
      "export namespace "*)
        ns=$(printf '%s' "${line}" | sed -E 's/^export namespace[[:space:]]+([A-Za-z0-9_]+).*/\1/')
        ;;
      *"export class "*)
        [[ -z "${ns}" ]] && continue
        cls=$(printf '%s' "${line}" | sed -E 's/.*export class[[:space:]]+([A-Za-z0-9_]+).*/\1/')
        echo "${ns} ${cls}"
        ;;
    esac
  done < "${WAILSJS_MODELS}" | LC_ALL=C sort -u | while read -r ns cls; do
    [[ -z "${ns}" || -z "${cls}" ]] && continue
    while IFS= read -r dir; do
      [[ -d "${dir}" ]] || continue
      while IFS= read -r gofile; do
        # The declaration block for `type <cls> ...`: from the header line
        # to the closing brace at column 0, or the header line alone for a
        # single-line defined type / alias.
        awk -v t="${cls}" '
          index($0, "type " t " ") == 1 { inblock = 1; print; if ($0 !~ /\{[ \t]*$/) inblock = 0; next }
          inblock { print; if ($0 == "}") inblock = 0 }
        ' "${gofile}"
      done < <(LC_ALL=C find "${dir}" -maxdepth 1 -name '*.go' ! -name '*_test.go' 2>/dev/null | LC_ALL=C sort)
    done < <(awk -v n="${ns}" '$1==n {print $2}' "${pkgindex}" | LC_ALL=C sort)
  done
}

_wailsjs_collect_types > "${WAILSJS_TYPES_FILE}"

# Portable SHA-256: self-hosted Linux runners ship `sha256sum`; macOS dev boxes
# ship `shasum`. Both emit the identical SHA-256 digest, so the committed hash
# stays valid across platforms. (shasum-only broke CI on the Linux ARM64 pool.)
if command -v sha256sum >/dev/null 2>&1; then
  _wailsjs_sha() { sha256sum | awk '{print $1}'; }
else
  _wailsjs_sha() { shasum -a 256 | awk '{print $1}'; }
fi

if [[ "${1:-}" == "--update-wailsjs-hash" ]]; then
  COMPUTED=$(cat ${WAILSJS_SOURCES} "${WAILSJS_TYPES_FILE}" | _wailsjs_sha)
  echo "${COMPUTED}" > "${WAILSJS_HASH_FILE}"
  echo "check-codegen: wailsjs binding source hash updated to ${COMPUTED}"
  echo "check-codegen: OK — generated files match"
  exit 0
fi

if [[ -f "${WAILSJS_HASH_FILE}" ]]; then
  COMMITTED_HASH=$(cat "${WAILSJS_HASH_FILE}")
  CURRENT_HASH=$(cat ${WAILSJS_SOURCES} "${WAILSJS_TYPES_FILE}" | _wailsjs_sha)
  if [[ "${COMMITTED_HASH}" != "${CURRENT_HASH}" ]]; then
    cat >&2 <<EOF

check-codegen: WAILSJS DRIFT DETECTED.

The binding SURFACE has changed since the last time the wailsjs files
were regenerated. That means either:

  - a Bindings method was added/removed/re-signed
    (core/rpc/bindings.go, core/rpc/bindings_*.go), or
  - a BOUND TYPE's declaration changed — any struct that
    frontend/wailsjs/go/models.ts emits a class for, wherever it lives.
    The second case is the one that used to slip through: the type can
    sit far from core/rpc (elicitation.Question is in core/elicitation)
    and changing it silently staled models.ts.

The committed files under frontend/wailsjs/go/rpc/Bindings.{js,d.ts} and
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
  # Previously this printed a WARN and fell through to "OK — generated files
  # match", exit 0. Verified: `rm scripts/ci/wailsjs-bindings.sha256` plus a
  # real edit to core/rpc/bindings.go passed the gate. Deleting the baseline
  # is how you silence a hash gate, so deleting the baseline has to be the
  # thing that fails it — same reasoning that removed the `if [ -f <script> ]`
  # existence guard around the bundle-size gate in #279.
  cat >&2 <<EOF

check-codegen: FAIL — ${WAILSJS_HASH_FILE} not found.

This file is the wailsjs drift gate's baseline. Without it the gate has
nothing to compare against and would pass unconditionally, which is
indistinguishable from having no gate at all.

If the hash file was deleted by accident, restore it from git. If the
wailsjs binding surface was intentionally regenerated, recreate it:

  bash scripts/ci/check-codegen.sh --update-wailsjs-hash
  git add scripts/ci/wailsjs-bindings.sha256

If the wailsjs binding layer is being retired, delete this whole block in
the same commit that deletes frontend/wailsjs/ — deliberately, in review.

EOF
  exit 1
fi

echo "check-codegen: OK — generated files match"
