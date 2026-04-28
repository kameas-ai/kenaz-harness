---
work_package_id: "WP07"
title: "Key rotation with overlap window and grace-period fallback"
dependencies:
  - "WP06"
planning_base_branch: "main"
merge_target_branch: "main"
branch_strategy: "Planning artifacts were generated on main; completed changes must merge back into main."
subtasks:
  - "T001"
  - "T002"
  - "T003"
  - "T004"
phase: "Phase 6 - Key rotation"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP07 – Key rotation with overlap window and grace-period fallback

## Goal

Implement `core/trust/rotation.go` providing `BeginRotation` and `CompleteRotation` with a configurable overlap window (FR-005) and a grace-period flag on accepted-but-stale verifications (FR-013). Operator-facing acceptance scenarios from User Story 3 must pass: peers on either old or new key verify during the window; expired previous keys reject with `RejKeyExpired`; every grace-period acceptance is recorded so dashboards can show "N peers still on old key."

## Spec references

- FR-005 (key rotation with overlap), FR-013 (grace-period fallback)
- FR-017 (`RejKeyExpired`), FR-011 (audit), C-005 (SOC 2 readiness)
- SC-003 (24-hour overlap rotation completes with zero failed calls)
- User Story 3 acceptance scenarios

## Plan references

- §4.4 (rotation / overlap mechanics): `BeginRotation(anchorID, newKey, overlap)` writes to `trust_anchor_history` and stamps `previous_key`, `overlap_ends`. `CompleteRotation` purges or auto-purges on overlap lapse.
- §5.1 columns: `previous_key NULL`, `overlap_ends NULL` on `trust_anchors`.
- §5.2 audit kind `trust/key-rotated`.

## Subtasks

- **T001** — Implement `core/trust/rotation.go` `BeginRotation(ctx, anchorID, newKey, overlap)`: validates the new key (algorithm + parseable), writes to `trust_anchors` (`public_key=newKey`, `previous_key=oldKey`, `overlap_ends=now+overlap`), appends a `trust_anchor_history` row, emits `trust/key-rotated` audit (per plan §5.2). Reject if anchor mid-rotation already.
- **T002** — Implement `CompleteRotation(ctx, anchorID)`: purges `previous_key` and `overlap_ends`. Add a background sweep (or lazy-on-read check) so once `now > overlap_ends`, the previous key is automatically retired without an explicit operator call. Emit `trust/key-rotated` with `phase=completed` field.
- **T003** — Update `verify.go` step 8 (rotation overlap check from WP03): when signature verifies against `previous_key` AND `now <= overlap_ends`, set `CacheState=grace` and emit `trust/verification-accepted` with `cache_state=grace`. When `now > overlap_ends` and signature verifies only against `previous_key`, return `RejKeyExpired`.
- **T004** — Add black-box tests covering User Story 3 acceptance scenarios: (a) verifier accepts new key after rotation; (b) verifier accepts previous key during overlap, audit shows `cache_state=grace`; (c) verifier rejects previous key after `overlap_ends` with `RejKeyExpired`; (d) `BeginRotation` invoked twice on the same anchor returns a typed error rather than corrupting state.

## Acceptance criteria

- Rotation state transitions are atomic at the SQLite layer (transaction wraps anchor update + history append).
- `RejKeyExpired` is distinct from `RejAnchorMissing` (plan §4.2 step 4 vs §4.4 expiry).
- Every rotation event and every grace-period acceptance produces exactly one audit entry (FR-011, NFR-005).
- Dashboards / `harness trust status` can compute "N peers still on old key" by counting `trust/verification-accepted` events with `cache_state=grace` per anchor (operator value from §4.4).
- SC-003 evidence: an integration test simulating a 24-hour overlap with peers on both keys completes with zero failed verifications attributable to rotation.
- Charter testing: tests use a real on-disk event log.

## Files to create/modify

- Modify: `core/trust/rotation.go` (was a stub from WP01)
- Modify: `core/trust/verify.go` step 8 (rotation overlap check now hits the real anchor store)
- Modify: `core/trust/anchor.go` (atomic rotation update helpers)
- Tests: `core/trust/rotation_test.go`, `core/trust/rotation_integration_test.go`

## Definition of done

- All four subtasks complete.
- ≥ 80% coverage on `rotation.go`.
- Black-box tests cover all four User Story 3 acceptance scenarios.
- PR description includes SC-003 simulated-rotation evidence.
- Atomicity verified by a chaos test that interrupts the SQLite transaction mid-rotation and asserts state is either fully pre- or fully post-rotation, never half.
