#!/usr/bin/env bash
# check-bundle-channel-kinds-sync.sh
# (bundle-download-and-verify-01PMZ909 UNIT-8, tasks.md UNIT-8 step 1:
# "over the kinds the registry actually has factories for — DERIVED
# FROM THE BACKEND, not a hardcoded frontend list, so an unimplemented
# kind can never be offered.")
#
# THE DEFECT CLASS
# -----------------
# There is no Bundle_ListChannelKinds RPC — adding one requires
# regenerating frontend/wailsjs/** (`wails generate module`), which per
# this repo's tooling rules executes main.go's binding-introspection
# path against a REAL profile database and is not something a sweep or
# an agent runs casually. BundlesView.vue's channel picker therefore
# hardcodes its CHANNEL_KINDS list rather than querying the backend at
# runtime. That is a real deviation from the spec's "derived from the
# backend" instruction, and this gate is what keeps the deviation from
# becoming exactly the lie it was meant to prevent: an unimplemented (or
# since-removed) channel kind silently offered in the picker, or a real
# channel package with no way for a user to ever select it.
#
# WHAT THIS CHECKS
# ----------------
# The backend's authoritative kind set is every non-test
# `const Kind = "..."` declaration directly under
# core/bundle/channels/*/ (one per channel subpackage, per
# DIRECTIVE_001 — core/bundle/channels/channel.go). The frontend's
# offered set is every `kind: '...'` entry in BundlesView.vue's
# CHANNEL_KINDS array. This gate fails if the two sets are not IDENTICAL
# — a kind on one side and not the other, in either direction.
#
# Planted-violation proof: scripts/ci/gates_can_fail_test.go
# "bundle-channel-kinds-sync/frontend-list-drifts-from-backend".
#
# Usage: bash scripts/ci/check-bundle-channel-kinds-sync.sh

set -euo pipefail

source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib/ci-gate.sh"

GATE="[bundle-channel-kinds-sync]"

CHANNELS_DIR="core/bundle/channels"
VUE_FILE="frontend/src/views/bundles/BundlesView.vue"

ci_require_dir "$CHANNELS_DIR" "$GATE"
ci_require_file "$VUE_FILE" "$GATE"

backend_kinds="$(for f in "$CHANNELS_DIR"/*/*.go; do
  case "$f" in
    *_test.go) continue ;;
  esac
  grep -hoE '^const Kind = "[a-z_]+"' "$f" 2>/dev/null || true
done | sed -E 's/^const Kind = "([a-z_]+)"/\1/' | sort -u)"

frontend_kinds="$(grep -oE "kind: '[a-z_]+'" "$VUE_FILE" | sed -E "s/kind: '([a-z_]+)'/\1/" | sort -u)"

if [[ -z "$backend_kinds" ]]; then
  echo "${GATE} FAIL: no 'const Kind = \"...\"' declarations found under ${CHANNELS_DIR}/*/." >&2
  echo "${GATE} Either every channel package was deleted (say so) or this gate's grep no" >&2
  echo "${GATE} longer matches the DIRECTIVE_001 convention — update it in the same commit." >&2
  exit 1
fi

only_backend="$(comm -23 <(echo "$backend_kinds") <(echo "$frontend_kinds"))"
only_frontend="$(comm -13 <(echo "$backend_kinds") <(echo "$frontend_kinds"))"

fail=0
if [[ -n "$only_backend" ]]; then
  echo "${GATE} FAIL: channel kind(s) registered in the backend but NOT offered by" >&2
  echo "${GATE} ${VUE_FILE}'s CHANNEL_KINDS picker:" >&2
  echo "$only_backend" | sed "s/^/${GATE}   /" >&2
  echo "${GATE} A real, installable channel a user cannot select from the UI." >&2
  fail=1
fi
if [[ -n "$only_frontend" ]]; then
  echo "${GATE} FAIL: channel kind(s) offered by ${VUE_FILE}'s CHANNEL_KINDS picker but" >&2
  echo "${GATE} with NO 'const Kind = \"...\"' declaration under ${CHANNELS_DIR}/*/:" >&2
  echo "$only_frontend" | sed "s/^/${GATE}   /" >&2
  echo "${GATE} An offered kind the backend cannot open — channels.Registry.Open would" >&2
  echo "${GATE} return bundle.ErrChannelUnknown for every install attempt." >&2
  fail=1
fi

if [[ "$fail" -ne 0 ]]; then
  exit 1
fi

echo "${GATE} clean — frontend CHANNEL_KINDS matches the backend's registered channel packages: $(echo "$backend_kinds" | tr '\n' ' ')"
