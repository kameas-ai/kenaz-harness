#!/usr/bin/env bash
# check-recipe-token-substitution.sh — G-2
# (connector-lifecycle-truth-01PMZ303 UNIT-15, tasks.md §UNIT-15, spec.md §9)
#
# THE DEFECT CLASS
# -----------------
# core/mcp/recipes/registry.json shipped 14 recipes with a
# "${KAMEAS_<PROVIDER>_OAUTH_CLIENT_ID}" placeholder in auth.client_id,
# and front's "${KAMEAS_FRONT_OAUTH_CLIENT_SECRET}" in auth.client_secret
# — for years, with ZERO production code that ever substituted either
# field. Every other JSON path (spec.URL, spec.HeadersTemplate, Command,
# ArgsTemplate) already had a real Substitute* call site; Auth was the
# one recipe field a full-repo grep for "Substitute" would show untouched
# — and nothing checked for that gap, because the tokens themselves
# parsed fine and rendered fine right up until SignInRecipe rejected with
# a client-id-shaped error at runtime, on install, for every affected
# recipe.
#
# WHAT THIS CHECKS
# ----------------
# 1. DISCOVERY (derived, not declared): a Python pass over
#    core/mcp/recipes/registry.json and shipped.json walks every recipe
#    and records which of a fixed set of JSON paths (auth.client_id,
#    auth.client_secret, url, headers_template.*, command[],
#    args_template[], config_options[].default[], env_keys[].display)
#    carries at least one literal "${...}" token, ANYWHERE across either
#    catalog. This is the "derived from data" half — a JSON path with no
#    token today needs no manifest entry, and a future recipe that adds
#    one to a previously-token-free path makes this gate start requiring
#    coverage for it automatically.
#
# 2. MANIFEST CROSS-CHECK: every discovered path must appear in
#    scripts/ci/allowlists/g2-recipe-substituted-paths.txt, and at least
#    one of that path's listed grep patterns must match somewhere in
#    non-test Go code under core/. A path with a token and no manifest
#    entry fails by name. A manifest entry whose pattern no longer
#    matches ALSO fails — this is the half that keeps the manifest from
#    becoming an opt-out list: deleting the substitution call site
#    without updating this file is caught.
#
# WHAT THIS DOES NOT CHECK
# -------------------------
# Whether the substitution actually RUNS on the live path a recipe uses
# at spawn/sign-in time (as opposed to existing somewhere in the repo) —
# that is what the Go integration tests (AC-003, AC-003b) are for. This
# gate is a static, catalog-vs-grep cross-check; a function that exists
# but is dead code would still satisfy it. It would NOT have caught
# oauth-clientid-not-substituted by watching call graphs — it catches it
# by noticing NO call site existed at all, which is the shape the actual
# defect had.
#
# Planted-violation proof: scripts/ci/gates_can_fail_test.go
# "recipe-token-substitution/unmanifested-token-fails" (a scratch catalog
# copy with a new "${KAMEAS_TEST_SECRET}" token on a path with no
# manifest entry) and "recipe-token-substitution/stale-manifest-entry-
# fails" (a manifest entry whose call site was deleted from a scratch Go
# tree).
#
# Usage: bash scripts/ci/check-recipe-token-substitution.sh
#   Test-only overrides (point the gate at scratch copies so a planted
#   violation never touches tracked files):
#     G2_REGISTRY_JSON, G2_SHIPPED_JSON  — catalog files
#     G2_MANIFEST                        — the substituted-paths manifest
#     G2_SCAN_DIR                        — the Go source root to grep for
#                                           call sites (default: core)

set -euo pipefail

source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib/ci-gate.sh"

GATE="[recipe-token-substitution]"

REGISTRY_JSON="${G2_REGISTRY_JSON:-core/mcp/recipes/registry.json}"
SHIPPED_JSON="${G2_SHIPPED_JSON:-core/mcp/recipes/shipped.json}"
MANIFEST="${G2_MANIFEST:-scripts/ci/allowlists/g2-recipe-substituted-paths.txt}"
SCAN_DIR="${G2_SCAN_DIR:-core}"

for f in "$REGISTRY_JSON" "$SHIPPED_JSON" "$MANIFEST"; do
  if [[ -n "${G2_REGISTRY_JSON:-}${G2_SHIPPED_JSON:-}${G2_MANIFEST:-}" ]]; then
    [[ -f "$f" ]] || { echo "${GATE} FAIL: '$f' does not exist." >&2; exit 1; }
  else
    ci_require_file "$f" "$GATE"
  fi
done
ci_require_dir "$SCAN_DIR" "$GATE"

# --- Discovery pass: which JSON paths carry a "${...}" token, in either
# catalog, anywhere. Printed one path per line, sorted+deduped.
DISCOVERED="$(python3 - "$REGISTRY_JSON" "$SHIPPED_JSON" <<'PYEOF'
import json, sys

def has_token(s):
    return isinstance(s, str) and "${" in s

paths = set()
for fname in sys.argv[1:]:
    with open(fname) as fh:
        data = json.load(fh)
    for r in data.get("recipes", []):
        auth = r.get("auth") or {}
        if has_token(auth.get("client_id")):
            paths.add("auth.client_id")
        if has_token(auth.get("client_secret")):
            paths.add("auth.client_secret")
        if has_token(r.get("url")):
            paths.add("url")
        for v in (r.get("headers_template") or {}).values():
            if has_token(v):
                paths.add("headers_template.*")
        for c in (r.get("command") or []):
            if has_token(c):
                paths.add("command[]")
        for c in (r.get("args_template") or []):
            if has_token(c):
                paths.add("args_template[]")
        for co in (r.get("config_options") or []):
            default = co.get("default")
            values = default if isinstance(default, list) else [default]
            for d in values:
                if has_token(d):
                    paths.add("config_options[].default[]")
        for ek in (r.get("env_keys") or []):
            if has_token(ek.get("display")):
                paths.add("env_keys[].display")

for p in sorted(paths):
    print(p)
PYEOF
)"

if [[ -z "$DISCOVERED" ]]; then
  echo "${GATE} FAIL: discovery found zero JSON paths carrying a \${...} token in" >&2
  echo "${GATE} ${REGISTRY_JSON} or ${SHIPPED_JSON}. That is almost certainly a parse" >&2
  echo "${GATE} failure (both catalogs declare tokens today), not a genuinely clean tree —" >&2
  echo "${GATE} a gate reporting success because it found nothing to check is the exact" >&2
  echo "${GATE} failure mode this ritual exists to prevent. Investigate before trusting" >&2
  echo "${GATE} a 'clean' verdict here." >&2
  exit 1
fi

violations=0

path_has_coverage() {
  local path="$1"
  local line pattern
  while IFS='|' read -r mpath pattern; do
    [[ "$mpath" == \#* || -z "$mpath" ]] && continue
    if [[ "$mpath" == "$path" ]]; then
      if grep -rqE "$pattern" "$SCAN_DIR" --include='*.go' 2>/dev/null; then
        return 0
      fi
    fi
  done < "$MANIFEST"
  return 1
}

manifest_has_path() {
  local path="$1"
  grep -qF "${path}|" "$MANIFEST"
}

while IFS= read -r path; do
  [[ -z "$path" ]] && continue
  if ! manifest_has_path "$path"; then
    echo "${GATE} FAIL: JSON path '${path}' carries a \${...} token in ${REGISTRY_JSON} or" >&2
    echo "${GATE} ${SHIPPED_JSON}, but has NO entry in ${MANIFEST}." >&2
    echo "${GATE} Either a production Substitute* call site exists and the manifest should" >&2
    echo "${GATE} name it, or the path needs a real fix (this is exactly how" >&2
    echo "${GATE} auth.client_id/auth.client_secret shipped unsubstituted for years)." >&2
    violations=1
    continue
  fi
  if ! path_has_coverage "$path"; then
    echo "${GATE} FAIL: JSON path '${path}' has a manifest entry, but NONE of its listed" >&2
    echo "${GATE} grep patterns match any non-test .go file under ${SCAN_DIR}/. The call" >&2
    echo "${GATE} site named in the manifest was likely deleted or renamed without" >&2
    echo "${GATE} updating ${MANIFEST} in the same commit." >&2
    violations=1
  fi
done <<< "$DISCOVERED"

if [[ "$violations" -ne 0 ]]; then
  exit 1
fi

n_paths="$(echo "$DISCOVERED" | grep -c . || true)"
echo "${GATE} clean — all ${n_paths} JSON path(s) carrying a \${...} token have a manifest entry backed by a real call site."
