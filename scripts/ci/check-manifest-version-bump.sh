#!/usr/bin/env bash
# check-manifest-version-bump.sh — fail CI when a manifest fingerprint
# changed without a corresponding manifest_version bump.
#
# Algorithm:
#   1. Run go generate to get the current manifest_versions_gen.go.
#   2. Extract (kind, fingerprint, version) triples from the current file.
#   3. Extract (kind, fingerprint, version) triples from the previous
#      committed file (HEAD~1, or empty if the file did not exist).
#   4. For each kind whose fingerprint changed, verify the version also
#      changed. Fail if any kind's fingerprint changed but version did not.
#
# Mission: manifest-versioning-01NDFSEX02, WP05.
#
# Exit codes:
#   0 — OK
#   1 — violation: fingerprint changed without version bump
#   2 — go generate failed
#
# Compatibility: bash 3+ (no associative arrays; uses temp files + join).

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
cd "${REPO_ROOT}"

GEN_FILE="core/agentgraph/manifest_versions_gen.go"

echo "check-manifest-version-bump: regenerating ${GEN_FILE}..."
if ! go generate ./core/agentgraph/...; then
  echo "check-manifest-version-bump: go generate FAILED" >&2
  exit 2
fi

TMPDIR_LOCAL=$(mktemp -d)
trap 'rm -rf "${TMPDIR_LOCAL}"' EXIT

# ---- helpers ----

# parse_gen_file <input-file> → temp file with lines "kind:fp:ver"
parse_gen_file() {
  local src="$1"
  local out="$2"
  python3 - "${src}" "${out}" <<'PYEOF'
import sys
import re

src = sys.argv[1]
out = sys.argv[2]

try:
    with open(src) as f:
        content = f.read()
except FileNotFoundError:
    # File doesn't exist — write empty output.
    open(out, 'w').close()
    sys.exit(0)

# Extract ManifestVersion lines: GoName = "semver"
# e.g.  ActivityManifestVersion = "1.0.0"
versions = {}
for m in re.finditer(r'\b([A-Za-z]+)ManifestVersion\s*=\s*"([^"]+)"', content):
    name, ver = m.group(1), m.group(2)
    versions[name] = ver

# Extract ManifestFingerprint lines: GoName = "hexdigest"
# e.g.  ActivityManifestFingerprint = "abc123..."
# Exclude lines that contain "ManifestFingerprint" in the name to avoid
# double-matching the constant name itself in comments.
fingerprints = {}
for m in re.finditer(r'\b([A-Za-z]+)ManifestFingerprint\s*=\s*"([0-9a-f]{64})"', content):
    name, fp = m.group(1), m.group(2)
    fingerprints[name] = fp

lines = []
for name in sorted(fingerprints.keys()):
    fp = fingerprints[name]
    ver = versions.get(name, "")
    lines.append(f"{name}:{fp}:{ver}")

with open(out, 'w') as f:
    f.write('\n'.join(lines) + ('\n' if lines else ''))
PYEOF
}

# ---- current state ----
CUR_FILE="${TMPDIR_LOCAL}/cur.txt"
parse_gen_file "${GEN_FILE}" "${CUR_FILE}"

# ---- previous state (HEAD~1) ----
PREV_FILE="${TMPDIR_LOCAL}/prev.txt"
PREV_GEN="${TMPDIR_LOCAL}/prev_gen.go"

# FLOOR GUARD. This gate's entire signal is the DIFF against HEAD~1, so a
# baseline it cannot read is not "an empty baseline" -- it is no signal at
# all. With an empty PREV every kind classifies as "new" rather than
# "changed", nothing trips the unbumped-version check, and the gate exits 0
# while inspecting nothing. That is the failure this repo keeps finding
# (docs/unwired-ledger.md, finding #59: a check that passes on something
# adjacent to the property it claims to verify).
#
# It is not hypothetical. actions/checkout defaults to fetch-depth: 1, where
# HEAD~1 does not exist at all. The gate runs in pr.yml's lint-go job, which
# sets fetch-depth: 0 -- but gates_can_fail_test.go exercises it from the
# test-go job, which did not, and the planted-violation proof duly failed
# with "the gate cannot see an unbumped version". That proof is how this was
# found; the else-branch below had been silently swallowing the condition.
#
# So distinguish the two cases that reach here:
#   - HEAD~1 does not RESOLVE  -> shallow clone or root commit. Abort: the
#     gate cannot do its job and must say so rather than pass.
#   - HEAD~1 resolves but the FILE is absent -> genuinely new file, and an
#     empty baseline is the correct reading.
if ! git rev-parse --verify --quiet "HEAD~1" >/dev/null; then
  echo "[manifest-version-bump] FAIL: cannot resolve HEAD~1, so there is no baseline to diff against."
  echo "[manifest-version-bump] This gate compares ${GEN_FILE} against its HEAD~1 copy; without real"
  echo "[manifest-version-bump] history it would inspect nothing and exit 0. Refusing to report clean."
  echo "[manifest-version-bump] In CI: set 'fetch-depth: 0' on the actions/checkout step for this job."
  echo "[manifest-version-bump] Locally: run from a full clone, not a shallow or single-commit one."
  exit 1
fi

if git cat-file -e "HEAD~1:${GEN_FILE}" 2>/dev/null; then
  git show "HEAD~1:${GEN_FILE}" > "${PREV_GEN}"
else
  # HEAD~1 exists but did not contain the file: genuinely new, empty is right.
  echo "[manifest-version-bump] ${GEN_FILE} absent at HEAD~1 — treating baseline as empty (new file)."
  > "${PREV_GEN}"
fi
parse_gen_file "${PREV_GEN}" "${PREV_FILE}"

# ---- comparison via Python (avoids bash associative arrays) ----
RESULT=$(python3 - "${CUR_FILE}" "${PREV_FILE}" <<'PYEOF'
import sys

def load(path):
    """Returns dict name -> (fp, ver)."""
    d = {}
    with open(path) as f:
        for line in f:
            line = line.strip()
            if not line:
                continue
            parts = line.split(':', 2)
            if len(parts) == 3:
                d[parts[0]] = (parts[1], parts[2])
    return d

cur = load(sys.argv[1])
prev = load(sys.argv[2])

violations = []
infos = []

for name, (cur_fp, cur_ver) in sorted(cur.items()):
    if name not in prev:
        infos.append(f"new kind {name} (fp={cur_fp[:12]}…)")
        continue
    prev_fp, prev_ver = prev[name]
    if cur_fp == prev_fp:
        continue  # unchanged
    # Fingerprint changed — version must also change.
    if cur_ver == prev_ver:
        violations.append(
            f"{name}: fingerprint changed ({prev_fp[:12]}… → {cur_fp[:12]}…) "
            f"but manifest_version unchanged ({cur_ver})"
        )
    else:
        infos.append(
            f"OK kind={name} fp changed + version bumped ({prev_ver} → {cur_ver})"
        )

for info in infos:
    print(f"INFO:{info}")
for v in violations:
    print(f"VIOLATION:{v}")
PYEOF
)

VIOLATIONS_FOUND=0
while IFS=: read -r tag msg; do
  case "${tag}" in
    INFO)
      echo "check-manifest-version-bump: ${msg}"
      ;;
    VIOLATION)
      echo "check-manifest-version-bump: VIOLATION — ${msg}" >&2
      VIOLATIONS_FOUND=1
      ;;
  esac
done <<< "${RESULT}"

if [[ "${VIOLATIONS_FOUND}" -ne 0 ]]; then
  cat >&2 <<'EOF'

Fix: edit the manifest's manifest_version: field to a new semver
(e.g. "1.0.0" → "1.1.0") and re-run 'go generate ./core/agentgraph/...'
to bake the new fingerprint + version into manifest_versions_gen.go.

EOF
  exit 1
fi

echo "check-manifest-version-bump: OK"
