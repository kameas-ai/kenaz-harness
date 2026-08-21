#!/usr/bin/env bash
# check-bundle-verify-ordering.sh — G-1
# (bundle-download-and-verify-01PMZ909 UNIT-9, spec.md §2 / §7 G-1,
# tasks.md UNIT-9 step 1: "the sequencing constraint becomes mechanical.")
#
# THE DEFECT CLASS
# -----------------
# spec §2's sequencing rule is load-bearing and, before this gate, existed
# only as a paragraph a reviewer had to remember: "UNIT-2 and UNIT-3 both
# land before UNIT-4. UNIT-4 is the only unit that puts a verify call on
# the install path." Landing the call site in
# core/rpc/views/bundle/impl.go ahead of either precondition ships an
# install path that either:
#
#   - refuses EVERY valid bundle (§1.5: bundleadapter.go's Envelope
#     carries a nil Signature, so ValidateEnvelopeShape — runVerify step
#     1 — rejects before the algorithm registry at step 7 is ever
#     reached; every result is OK=false, Reason="signature_invalid"), or
#
#   - refuses every bundle with a false accusation (§1.7 F-1: with the
#     anchor store still hardcoded to an empty in-memory instance, a
#     CORRECTLY signed bundle still gets RejAnchorMissing on every boot —
#     worse than no verification, because it's a false "not verified"
#     rather than an honest gap).
#
# WHAT THIS CHECKS
# ----------------
# If core/rpc/views/bundle/impl.go references VerifyManifestSignatures
# (i.e. the install path has wired the verify call), this gate fails
# unless BOTH preconditions hold:
#
#   1. core/trust/bundleadapter.go does NOT still build the envelope with
#      `Signature: nil,` (UNIT-2 must have wired real signature bytes).
#   2. core/trust/trust.go's Config struct declares an anchor-store field
#      (UNIT-3's persistence seam — grep for `Anchors ` inside the
#      `type Config struct` block).
#
# A two-grep gate encoding the one ordering mistake that would ship a
# verifier refusing every valid bundle.
#
# Planted-violation proof: scripts/ci/gates_can_fail_test.go
# "bundle-verify-ordering/signature-nil-with-callsite-present" —
# re-inserts `Signature: nil,` into bundleadapter.go with the call site
# present in impl.go, and asserts the gate rejects it.
#
# Usage: bash scripts/ci/check-bundle-verify-ordering.sh

set -euo pipefail

source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib/ci-gate.sh"

GATE="[bundle-verify-ordering]"

IMPL_FILE="core/rpc/views/bundle/impl.go"
ADAPTER_FILE="core/trust/bundleadapter.go"
TRUST_FILE="core/trust/trust.go"

ci_require_file "$IMPL_FILE" "$GATE"
ci_require_file "$ADAPTER_FILE" "$GATE"
ci_require_file "$TRUST_FILE" "$GATE"

# The call site may be absent (verification not yet wired at all) — that
# is not this gate's concern; it only fires once the call site exists.
if ! grep -q "VerifyManifestSignatures" "$IMPL_FILE"; then
  echo "${GATE} clean — ${IMPL_FILE} does not call VerifyManifestSignatures yet."
  exit 0
fi

fail=0

# Precondition 1 (UNIT-2): the adapter must not still pass a nil
# signature into the envelope. Match the exact defect shape from spec
# §1.5 — a `Signature:` field literal set to `nil`.
if grep -Eq 'Signature:[[:space:]]*nil,' "$ADAPTER_FILE"; then
  echo "${GATE} FAIL: ${IMPL_FILE} calls VerifyManifestSignatures, but" >&2
  echo "${GATE} ${ADAPTER_FILE} still builds the envelope with 'Signature: nil,'" >&2
  echo "${GATE} (spec §1.5). Every Verify call rejects at step 1" >&2
  echo "${GATE} (ValidateEnvelopeShape) before the algorithm registry is ever" >&2
  echo "${GATE} reached — this ships an install path that refuses every valid" >&2
  echo "${GATE} bundle. UNIT-2 must land (carry real signature bytes into the" >&2
  echo "${GATE} envelope) before the call site does." >&2
  fail=1
fi

# Precondition 2 (UNIT-3): trust.Config must declare an anchor-store
# field. Extract the Config struct body and look for a field whose type
# is AnchorStore (spec §5.3's seam: `Anchors AnchorStore`).
config_block="$(awk '/^type Config struct/{flag=1} flag{print} flag && /^}/{exit}' "$TRUST_FILE")"
if ! grep -q "AnchorStore" <<<"$config_block"; then
  echo "${GATE} FAIL: ${IMPL_FILE} calls VerifyManifestSignatures, but" >&2
  echo "${GATE} ${TRUST_FILE}'s Config struct has no AnchorStore-typed field" >&2
  echo "${GATE} (spec §1.7 F-1 / §5.3). Every engine still gets the hardcoded" >&2
  echo "${GATE} in-memory, per-instance anchor store, so a correctly signed" >&2
  echo "${GATE} bundle gets RejAnchorMissing on every boot — a false" >&2
  echo "${GATE} accusation, worse than no verification at all. UNIT-3 must" >&2
  echo "${GATE} land (a persisted AnchorStore + the Config seam) before the" >&2
  echo "${GATE} call site does." >&2
  fail=1
fi

if [[ "$fail" -ne 0 ]]; then
  exit 1
fi

echo "${GATE} clean — verify call site present, both UNIT-2 and UNIT-3 preconditions hold."
