---
work_package_id: "WP05"
title: "Provenance verification via a2a-signed-cards-trust API"
dependencies:
  - "WP01"
  - "a2a-signed-cards-trust-01KQ18P9"
planning_base_branch: "main"
merge_target_branch: "main"
branch_strategy: "Planning artifacts were generated on main; completed changes must merge back into main."
subtasks:
  - "T001"
  - "T002"
  - "T003"
  - "T004"
phase: "Phase 5 - Provenance verification"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP05 – Provenance verification (delegate to trust API)

## Goal

Add a per-layer provenance verifier inside `core/context/` that calls the `a2a-signed-cards-trust` verification API exactly once per pack, maps its typed-error taxonomy 1:1 (no new error codes), and produces verification records consumed downstream by the audit emitter and the fail-closed policy. Per C-003 we never reimplement signing, key management, or trust anchors.

## Spec references

- FR-003 (Per-layer provenance, signed via `a2a-signed-cards-trust`)
- NFR-003 (100 % of active layers have verified provenance recorded at injection)
- SC-003 (Broken provenance rejected 100 %; zero bytes injected)
- C-003 (Provenance reuses signed-cards primitive — never reimplemented)
- Acceptance scenarios US1.3, US4.1–4.3 (verification success, tampering, fail-closed)

## Plan references

- §4.2 (Provenance verifier — single call into `core/trust.Verifier.Verify`)
- §3 (`ProvenanceRecord` carrying anchor id, algorithm, hash, cache state)
- §6 (Integration with `a2a-signed-cards-trust-01KQ18P9` — error taxonomy 1:1 from FR-017)
- Risk R8 (intermediate-key rotation grace handled transparently by trust layer)

## Subtasks

- T001 Define `core/context/verify.go` (or sub-package) with a thin `Verifier` adapter that consumes `core/trust.Verifier`.
- T002 Per-pack flow: load detached signature envelope from the pack's `signatures/pack.sig`, build the verification payload, call `core/trust.Verifier.Verify(payload, envelope, policy)` once.
- T003 Map every error from the trust FR-017 taxonomy to a typed `ProvenanceError` carrying the original code unchanged (signature invalid, algorithm not permitted, anchor missing, anchor removed, key revoked, key expired, identity collision, clock skew, precedence ambiguity).
- T004 Produce `ProvenanceRecord{anchor_id, algorithm, content_hash, cache_state, grace_state}` for every verified pack; failed verifications produce a typed `pack_rejected` payload candidate (audit emission lands in WP10).

## Acceptance criteria

- A signed pack from a trusted anchor verifies and yields a `ProvenanceRecord`.
- A tampered pack fails verification with the exact trust-layer error code mapped through unchanged (validates SC-003 and acceptance scenario US4.2).
- Zero imports of any signing-backend SDK from `core/context/` — only `core/trust/` API surface.
- Edge case: pack signed under intermediate-key grace period surfaces grace state in the verification record (Risk R8).

## Files to create/modify

- `core/context/verify.go`
- `core/context/types_provenance.go` (or extend existing types file)
- `core/context/verify_test.go`
- Test fixtures: signed pack, tampered pack, wrong-anchor pack

## Definition of done

- Tests cover all eight (or current count) trust-taxonomy failure modes.
- No new error codes introduced — every failure maps 1:1 from trust API.
- WP merged to main via squash-merge PR.
