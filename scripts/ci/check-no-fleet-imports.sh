#!/usr/bin/env bash
# check-no-fleet-imports.sh — fleet-auth-foundation-01NDFSEX08 WP07
#
# Verifies the OSS-first contract: only core/rpc/views/settings/ is allowed
# to import core/fleet/. All other packages must be fleet-free.
#
# If core/fleet/ has been removed entirely (fork case), the script also passes.
#
# Usage: bash scripts/ci/check-no-fleet-imports.sh

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$REPO_ROOT"

MODULE="$(go list -m)"
FLEET_PKG="${MODULE}/core/fleet"

echo "[no-fleet-imports] fleet package: ${FLEET_PKG}"

# If core/fleet/ doesn't exist (fork case), nothing to check.
if [ ! -d "core/fleet" ]; then
  echo "[no-fleet-imports] core/fleet/ not present — fork case: PASS"
  exit 0
fi

# Allowlisted packages that ARE permitted to import core/fleet/.
# - core/rpc                       — chassis wiring: NewAPI binds the
#                                    fleet.Client into Settings + middleware
#                                    + the dedicated fleet view at startup.
# - core/rpc/views/settings        — original boundary (auth + capability + config-pull RPCs).
# - core/rpc/views/fleet           — dedicated fleet view (fleet-otel-archival WP07).
# - core/rpc/middleware            — lockdown middleware (fleet-emergency-lockdown WP04).
ALLOWLIST=(
  "${MODULE}/core/rpc"
  "${MODULE}/core/rpc/views/settings"
  "${MODULE}/core/rpc/views/fleet"
  "${MODULE}/core/rpc/middleware"
)

# Build the list of all non-fleet packages in the repo.
# Use || true so grep -v with no matches doesn't abort the script.
ALL_PKGS=$(go list -tags ignore ./core/... 2>/dev/null | grep -v "^${MODULE}/core/fleet" || true)

if [ -z "$ALL_PKGS" ]; then
  # Fallback: list without tags filter. Some envs require CGO for full build.
  ALL_PKGS=$(go list ./core/... 2>/dev/null | grep -v "^${MODULE}/core/fleet" || true)
fi

VIOLATIONS=()

for pkg in $ALL_PKGS; do
  # Skip allowlisted packages.
  allowed=false
  for a in "${ALLOWLIST[@]}"; do
    if [[ "$pkg" == "$a" || "$pkg" == "${a}/"* ]]; then
      allowed=true
      break
    fi
  done
  if $allowed; then
    continue
  fi

  # Check if this package imports core/fleet.
  imports=$(go list -f '{{join .Imports " "}}' "$pkg" 2>/dev/null || true)
  if echo "$imports" | grep -q "${FLEET_PKG}"; then
    VIOLATIONS+=("$pkg")
  fi
done

if [ ${#VIOLATIONS[@]} -eq 0 ]; then
  echo "[no-fleet-imports] clean — no unauthorized fleet imports found: PASS"
  exit 0
fi

echo "[no-fleet-imports] FAIL — the following packages import core/fleet/ but are not in the allowlist:"
for v in "${VIOLATIONS[@]}"; do
  echo "  $v"
done
echo ""
echo "Only ${MODULE}/core/rpc/views/settings/ is permitted to import core/fleet/."
exit 1
