#!/usr/bin/env bash
# check-install-provider-coverage.sh — install-framework-01DOGF0B WP03.
#
# The badge-only catalog install (docs/unwired-ledger.md, 2026-10-04) was
# "an install that writes a directory nothing reads". WP02 answered the
# gate question: not gateable as a filesystem path, but symbol-gateable
# once the provider contract exists. This is that gate. It pairs, in both
# directions:
#
#   1. every core/fleet CatalogItemKind value  ->  an install.Kind value
#      (a fleet catalog kind with no install kind is a kind the surface
#      could advertise with no install path at all);
#   2. every install.Kind  ->  EITHER a production registration
#      `<fw>.Register(install.Kind<Name>, ...)` under core/rpc/ (non-test)
#      AND a consumer test `func TestInstallProvider_<Name>_ConsumerSeesInstall`
#      somewhere under core/ (FR-1: each provider has an integration test
#      asserting the consumer sees the capability after install),
#      OR a dated allowlist line naming its blocker and owner;
#   3. every allowlist line  ->  a real, still-unregistered kind (the
#      allowlist shrinks monotonically; a kind that gained a provider must
#      leave it in the same commit).
#
# Discovery floors: >= 5 install kinds and >= 4 catalog kinds, or the scan
# is broken (a rename, a moved file) rather than the tree being clean.
#
# Planted-violation proofs: scripts/ci/gates_can_fail_test.go,
# install-provider-coverage/*.
set -euo pipefail
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib/ci-gate.sh"

GATE="[install-provider-coverage]"
KIND_FILE="core/install/provider.go"
CATALOG_FILE="core/fleet/catalog.go"
WIRING_DIR="core/rpc"
ALLOW_FILE="scripts/ci/allowlists/install-provider-coverage.txt"

ci_require_file "$KIND_FILE" "$GATE"
ci_require_file "$CATALOG_FILE" "$GATE"
ci_require_dir "$WIRING_DIR" "$GATE"
ci_require_file "$ALLOW_FILE" "$GATE"

fail=0
violation() {
  echo "${GATE} FAIL: $*" >&2
  fail=1
}

# ── discovery ────────────────────────────────────────────────────────────
# "KindMCPRecipe mcp_recipe" per line.
kinds=$(grep -E '^[[:space:]]*(const[[:space:]]+)?Kind[A-Za-z0-9]+[[:space:]]+Kind[[:space:]]*=[[:space:]]*"[a-z_]+"' "$KIND_FILE" \
  | sed -E 's/^[[:space:]]*(const[[:space:]]+)?(Kind[A-Za-z0-9]+)[[:space:]]+Kind[[:space:]]*=[[:space:]]*"([a-z_]+)".*/\2 \3/' || true)
kind_count=$(printf '%s\n' "$kinds" | grep -c . || true)
if [[ "$kind_count" -lt 5 ]]; then
  echo "${GATE} FAIL: discovered ${kind_count} install kinds in ${KIND_FILE}, expected >= 5." >&2
  echo "${GATE} The scan is broken (renamed type? moved constants?), not the tree clean." >&2
  exit 1
fi

catalog_values=$(grep -E '^[[:space:]]*(const[[:space:]]+)?CatalogKind[A-Za-z0-9]+[[:space:]]+CatalogItemKind[[:space:]]*=[[:space:]]*"[a-z_]+"' "$CATALOG_FILE" \
  | sed -E 's/.*"([a-z_]+)".*/\1/' || true)
catalog_count=$(printf '%s\n' "$catalog_values" | grep -c . || true)
if [[ "$catalog_count" -lt 4 ]]; then
  echo "${GATE} FAIL: discovered ${catalog_count} CatalogItemKind values in ${CATALOG_FILE}, expected >= 4." >&2
  exit 1
fi

# Production registrations: non-test .go files under core/rpc/.
registered=$(grep -rhoE '\.Register\(install\.Kind[A-Za-z0-9]+' "$WIRING_DIR" --include='*.go' --exclude='*_test.go' \
  | sed -E 's/.*install\.(Kind[A-Za-z0-9]+)/\1/' | sort -u || true)

# Allowlist: "<value>  # <YYYY-MM-DD> blocker: ... owner: ..."
allow_values=$(grep -vE '^[[:space:]]*(#|$)' "$ALLOW_FILE" | awk '{print $1}' || true)

kind_values=$(printf '%s\n' "$kinds" | awk '{print $2}')

# ── 1. catalog kind -> install kind ─────────────────────────────────────
while IFS= read -r cv; do
  [[ -z "$cv" ]] && continue
  if ! printf '%s\n' "$kind_values" | grep -qx "$cv"; then
    violation "core/fleet CatalogItemKind \"${cv}\" has no install.Kind in ${KIND_FILE} — the catalog can advertise it with no install path."
  fi
done <<< "$catalog_values"

# ── 2. install kind -> registration + consumer test, or allowlist ───────
while IFS= read -r line; do
  [[ -z "$line" ]] && continue
  name="${line%% *}"
  value="${line##* }"
  short="${name#Kind}"
  if printf '%s\n' "$registered" | grep -qx "$name"; then
    if ! grep -rqE "func TestInstallProvider_${short}_ConsumerSeesInstall\(" core --include='*_test.go'; then
      violation "install.${name} (\"${value}\") is registered in ${WIRING_DIR}/ but has no consumer test — add func TestInstallProvider_${short}_ConsumerSeesInstall asserting the runtime consumer lists the capability after install (FR-1)."
    fi
    if printf '%s\n' "$allow_values" | grep -qx "$value"; then
      violation "\"${value}\" is registered AND still allowlisted in ${ALLOW_FILE} — remove its line (allowlists shrink monotonically)."
    fi
  else
    entry=$(grep -E "^${value}[[:space:]]" "$ALLOW_FILE" || true)
    if [[ -z "$entry" ]]; then
      violation "install.${name} (\"${value}\") has no registered provider in ${WIRING_DIR}/ and no allowlist entry — register a provider or add a dated line to ${ALLOW_FILE} naming the blocker and owner."
    elif ! printf '%s\n' "$entry" | grep -qE '#[[:space:]]*[0-9]{4}-[0-9]{2}-[0-9]{2}.*blocker:.*owner:'; then
      violation "allowlist entry for \"${value}\" must read '<kind>  # YYYY-MM-DD blocker: ... owner: ...' — got: ${entry}"
    fi
  fi
done <<< "$kinds"

# ── 3. allowlist -> real kind ───────────────────────────────────────────
while IFS= read -r av; do
  [[ -z "$av" ]] && continue
  if ! printf '%s\n' "$kind_values" | grep -qx "$av"; then
    violation "allowlist entry \"${av}\" in ${ALLOW_FILE} is not an install.Kind — stale line."
  fi
done <<< "$allow_values"

if [[ "$fail" -ne 0 ]]; then
  exit 1
fi
reg_count=$(printf '%s\n' "$registered" | grep -c . || true)
echo "${GATE} clean — ${kind_count} install kinds (${reg_count} registered with consumer tests, rest allowlisted), ${catalog_count} catalog kinds mapped."
