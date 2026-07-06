#!/usr/bin/env bash
# Phase A — Sites control-plane HTTP contract verification (no live hosting infra needed).
# Prereqs (provided by the fleet admin CLI once a test org exists):
#   FLEET_API   e.g. https://api.dev.fleet.kameas.ai   (from /config.json api_base_url)
#   TOKEN       a valid access token for an ENTERPRISE-tier org (CapSitesHosting)
#   ORG_ID      the org UUID (for the ungated PUT .../orgs/{id}/slug)
#   ORG_SLUG    desired org slug (default: e2e-<rand>)
# Writes PASS/FAIL evidence to ../results/WP00.md, WP02.md (auth), WP05.md (env API half).
# Idempotent: creates a throwaway site "e2e-contract-<rand>" and deletes it at the end.
set -uo pipefail
: "${FLEET_API:?set FLEET_API (e.g. https://api.dev.fleet.kameas.ai)}"
: "${TOKEN:?set TOKEN (enterprise-org access token)}"
: "${ORG_ID:?set ORG_ID}"
RES="$(cd "$(dirname "$0")" && pwd)/results"
RAND="${RANDOM}${RANDOM}"
ORG_SLUG="${ORG_SLUG:-e2e-$RAND}"
SITE_SLUG="e2e-contract-$RAND"
PASS=0; FAIL=0
TMP=$(mktemp -d)
au=(-H "Authorization: Bearer $TOKEN")

# check NAME EXPECTED_HTTP METHOD PATH [json-body] [grep-in-body]
check() {
  local name="$1" want="$2" method="$3" path="$4" body="${5:-}" needle="${6:-}"
  local args=(-s -o "$TMP/b" -w '%{http_code}' -X "$method" "${au[@]}" --max-time 30)
  [ -n "$body" ] && args+=(-H "Content-Type: application/json" --data "$body")
  local code; code=$(curl "${args[@]}" "$FLEET_API$path" 2>/dev/null)
  local ok="FAIL"; local why=""
  if [ "$code" = "$want" ]; then
    if [ -z "$needle" ] || grep -q "$needle" "$TMP/b"; then ok="PASS"; else why="(body missing '$needle')"; fi
  else why="(got $code want $want)"; fi
  [ "$ok" = PASS ] && PASS=$((PASS+1)) || FAIL=$((FAIL+1))
  printf '  [%s] %-46s %s %s -> %s %s\n' "$ok" "$name" "$method" "$path" "$code" "$why"
  echo "| $name | $method $path | want $want | got $code | $ok $why |" >> "$TMP/rows"
  # echo body for create so caller can capture id
  cat "$TMP/b" > "$TMP/last"
}

echo "== Phase A contract vs $FLEET_API (org $ORG_ID) =="
: > "$TMP/rows"

# --- WP00 / capability + auth ---
echo "-- WP00: capability gate + auth --"
check "sites list (entitled org -> 200)"        200 GET  "/api/v1/sites"
# unauth control
uc=$(curl -s -o /dev/null -w '%{http_code}' -X GET "$FLEET_API/api/v1/sites" --max-time 20)
[ "$uc" = 401 ] && { echo "  [PASS] unauth list -> 401"; PASS=$((PASS+1)); echo "| unauth list | GET /api/v1/sites (no token) | want 401 | got $uc | PASS |" >>"$TMP/rows"; } \
                 || { echo "  [FAIL] unauth list -> $uc (want 401)"; FAIL=$((FAIL+1)); echo "| unauth list | GET /api/v1/sites (no token) | want 401 | got $uc | FAIL |" >>"$TMP/rows"; }

# --- slug setup (ungated) ---
echo "-- slug setup --"
check "set org slug (ungated PUT)"              200 PUT  "/api/v1/orgs/$ORG_ID/slug" "{\"slug\":\"$ORG_SLUG\"}"

# --- WP-CRUD contract + error codes ---
echo "-- site CRUD + error codes --"
check "invalid slug rejected"                   400 POST "/api/v1/sites" "{\"slug\":\"Bad Slug!\",\"kind\":\"static\"}" "invalid_slug"
check "create static site (201)"                201 POST "/api/v1/sites" "{\"slug\":\"$SITE_SLUG\",\"kind\":\"static\"}"
SITE_ID=$(python3 -c "import sys,json; print(json.load(open('$TMP/last')).get('id',''))" 2>/dev/null)
echo "     -> site id: ${SITE_ID:-<none>}"
check "duplicate slug -> conflict"              409 POST "/api/v1/sites" "{\"slug\":\"$SITE_SLUG\",\"kind\":\"static\"}" "slug_conflict"
if [ -n "$SITE_ID" ]; then
  check "get site by id (200)"                  200 GET  "/api/v1/sites/$SITE_ID"
  check "get missing site -> 404"               404 GET  "/api/v1/sites/does-not-exist-$RAND"
  # --- WP05 env API half ---
  echo "-- WP05: env-var API round-trip (shape) --"
  check "put env var (200)"                     200 PUT  "/api/v1/sites/$SITE_ID/env" "{\"vars\":{\"ECHO_VAR\":\"e2e-test-value\"}}"
  check "get env lists ECHO_VAR"                200 GET  "/api/v1/sites/$SITE_ID/env" "" "ECHO_VAR"
  # cleanup
  echo "-- cleanup --"
  check "delete site (200/204)"                 200 DELETE "/api/v1/sites/$SITE_ID"
fi

echo ""
echo "== RESULT: $PASS passed, $FAIL failed =="
# emit a machine-readable rows file the result-writer can fold in
cp "$TMP/rows" "$RES/.phase-a-rows.md" 2>/dev/null || true
rm -rf "$TMP"
[ "$FAIL" -eq 0 ]
