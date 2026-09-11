#!/usr/bin/env bash
# check-no-fleet-imports.sh — fleet-auth-foundation-01NDFSEX08 WP07
#
# Verifies the OSS-first contract: only the packages named in the
# EXACT_ALLOWLIST / PREFIX_ALLOWLIST below are allowed to import
# core/fleet/. All other packages must be fleet-free.
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

# EXACT_ALLOWLIST packages are matched by full equality ONLY — never as a
# path prefix. core/rpc is the chassis: NewAPI binds the fleet.Client into
# Settings + middleware + the dedicated fleet view at startup, so the
# top-level package itself imports core/fleet. It must NOT exempt its own
# subpackages (core/rpc/views/*, core/rpc/middleware, ...) — that was
# finding #72: a prefix-wildcard matcher applied to this entry silently
# exempted every package under core/rpc/, and eleven packages relied on the
# hole. Each subpackage that legitimately needs core/fleet is now listed
# explicitly below with its own dated justification.
EXACT_ALLOWLIST=(
  "${MODULE}/core/rpc"
)

# PREFIX_ALLOWLIST packages are matched by full equality OR as a path
# prefix (i.e. "pkg" or "pkg/anything"). None of the entries below
# currently have subpackages, so prefix vs. exact is not observable today —
# it is kept prefix-capable only so a package that later grows a
# subpackage under it doesn't silently reopen this hole; new entries still
# require a dated justification per rule A-0.
#
# - core/rpc/views/settings — original boundary (auth + capability + config-pull RPCs).
# - core/rpc/views/fleet    — dedicated fleet view (fleet-otel-archival WP07).
# - core/rpc/middleware     — lockdown middleware (fleet-emergency-lockdown WP04):
#                             fleet.LockdownActive/LockdownBypassed/ErrLockdownActive.
# - core/mcp/builtin/sites  — fleet-sites MCP server: fleet-facing by definition;
#                             remove the directory to fork fleet-free.
# - core/rpc/views/catalog    (2026-09-11, finding #72) — API implements
#   CatalogAPI backed by *fleet.Client; browses/installs the fleet catalog
#   (CatalogItemKind, CatalogFilter, InstalledItems, ErrFleetDisabled).
#   Fleet-facing by construction.
# - core/rpc/views/cedar      (2026-09-11, finding #72) — Cedar_PublishToTeam
#   delegates to *fleet.Client.PublishCedarRule with a fleet.AuditEmitter
#   bridge. The package's only mutating RPC exists to publish policy to the
#   fleet team backend.
# - core/rpc/views/compliance (2026-09-11, finding #72) — API wraps
#   *fleet.AuditArchiver / *fleet.AuditRetentionSweeper (fleet-audit-archival
#   01NDFSEX13 WP05); degrades to ErrComplianceNotEnabled when fleet is nil.
# - core/rpc/views/contexts   (2026-09-11, finding #72) — publish/promote
#   RPCs push through *fleet.ContextGraphSyncer and fleet.ContextNodeEntry /
#   fleet.ContextPushConflict for team/org context sync; ErrFleetDisabled
#   when unwired.
# - core/rpc/views/sites      (2026-09-11, finding #72) — SitesImpl wraps
#   *fleet.Client (SiteRecord, DeploymentRecord, CapSitesHosting,
#   LoadTokens); doc comment already calls out the OSS boundary explicitly.
# - core/rpc/views/slashcmd   (2026-09-11, finding #72) — skill
#   publish/install/uninstall delegate to fleet.PublishSkill /
#   fleet.InstallSkill via *fleet.Client + *fleet.DeviceSigner.
# - core/rpc/views/sync       (2026-09-11, finding #72) — API implements
#   SyncAPI backed directly by *fleet.Syncer / *fleet.SecretPromptQueue;
#   the package's entire purpose is fleet sync.
PREFIX_ALLOWLIST=(
  "${MODULE}/core/rpc/views/settings"
  "${MODULE}/core/rpc/views/fleet"
  "${MODULE}/core/rpc/middleware"
  "${MODULE}/core/mcp/builtin/sites"
  "${MODULE}/core/rpc/views/catalog"
  "${MODULE}/core/rpc/views/cedar"
  "${MODULE}/core/rpc/views/compliance"
  "${MODULE}/core/rpc/views/contexts"
  "${MODULE}/core/rpc/views/sites"
  "${MODULE}/core/rpc/views/slashcmd"
  "${MODULE}/core/rpc/views/sync"
)

# Build the list of all non-fleet packages in the repo.
# Use || true so grep -v with no matches doesn't abort the script.
ALL_PKGS=$(go list -tags ignore ./core/... 2>/dev/null | grep -v "^${MODULE}/core/fleet" || true)

if [ -z "$ALL_PKGS" ]; then
  # Fallback: list without tags filter. Some envs require CGO for full build.
  ALL_PKGS=$(go list ./core/... 2>/dev/null | grep -v "^${MODULE}/core/fleet" || true)
fi

# If BOTH go list invocations came back empty we have no package set to check.
# The `2>/dev/null || true` above exists so a partial load error doesn't abort
# the script — but it also swallowed a total failure, and an empty package
# list walked straight past the loop to "clean — no unauthorized fleet imports
# found: PASS". Verified: with `go list` stubbed to exit 1 and a real
# `import _ ".../core/fleet"` planted in core/sessions, the old script printed
# PASS and exited 0.
#
# core/fleet/ exists (checked above), so core/ is a real Go tree and an empty
# listing means the toolchain failed, not that there is nothing to inspect.
if [ -z "$ALL_PKGS" ]; then
  echo "[no-fleet-imports] FAIL: 'go list ./core/...' returned no packages." >&2
  echo "[no-fleet-imports] core/fleet/ exists, so this is a toolchain failure (missing" >&2
  echo "[no-fleet-imports] module cache? build error?), not an empty tree. Re-run" >&2
  echo "[no-fleet-imports] 'go list ./core/...' to see the real error. Refusing to report" >&2
  echo "[no-fleet-imports] a clean result from an empty package set." >&2
  go list ./core/... >/dev/null || true
  exit 1
fi

VIOLATIONS=()

for pkg in $ALL_PKGS; do
  # Skip allowlisted packages.
  allowed=false
  for a in "${EXACT_ALLOWLIST[@]}"; do
    if [[ "$pkg" == "$a" ]]; then
      allowed=true
      break
    fi
  done
  if ! $allowed; then
    for a in "${PREFIX_ALLOWLIST[@]}"; do
      if [[ "$pkg" == "$a" || "$pkg" == "${a}/"* ]]; then
        allowed=true
        break
      fi
    done
  fi
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
echo "Only the following packages are permitted to import core/fleet/:"
for a in "${EXACT_ALLOWLIST[@]}"; do
  echo "  $a (exact match only)"
done
for a in "${PREFIX_ALLOWLIST[@]}"; do
  echo "  $a"
done
exit 1
