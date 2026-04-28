---
work_package_id: "WP06"
title: "Anchor store (SQLite tables) and trust audit emission into event log"
dependencies:
  - "WP01"
  - "WP03"
planning_base_branch: "main"
merge_target_branch: "main"
branch_strategy: "Planning artifacts were generated on main; completed changes must merge back into main."
subtasks:
  - "T001"
  - "T002"
  - "T003"
  - "T004"
  - "T005"
phase: "Phase 5 - Anchor store and audit"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP06 – Anchor store (SQLite tables) and trust audit emission

## Goal

Replace the in-memory anchor and identity-collision fakes with the persistent SQLite-backed anchor store using `core/storage/` (storage-foundations). Implement `core/trust/anchor.go` (CRUD + precedence resolver) and `core/trust/audit.go` (the only place trust events are emitted into `core/event/`). Audit is the bridge that lets every verification, anchor change, rotation, revocation, and backend-health flip produce exactly one append-only event log entry per FR-011, NFR-005, C-003, C-005.

## Spec references

- FR-003 (trust anchor configuration), FR-011 (append-only audit), FR-014 (preflight inputs), FR-015 (identity collision)
- NFR-005 (100% audit completeness), NFR-008 (parity)
- C-003 (event-log immutability), C-005 (SOC 2 readiness)
- SC-006 (offline replay of decisions from log)
- Plan §4.1 (anchor store), §5.1 (persistent tables), §5.2 (audit event kinds), §6.3 (event log integration)

## Plan references

- §5.1 tables: `trust_anchors`, `trust_anchor_history`, `trust_identities`, `trust_backend_health`.
- §5.2 audit event kinds: `trust/verification-accepted`, `trust/verification-rejected`, `trust/anchor-installed`, `trust/anchor-removed`, `trust/key-rotated`, `trust/revocation-ingested`, `trust/backend-unavailable`, `trust/preflight-finding`.
- §6.3 (single emission point; reuses event-log redaction pipeline; trust audit emits no credential-shaped material by construction).

## Cross-mission dependencies

- `storage-foundations-01KQ1A3K` — SQLite handle, migration runner.
- `event-log-01KQ1A3M` — append-only emit API, hash-chain integrity, emitter namespace `trust/`.

## Subtasks

- **T001** — Create migration files under `core/trust/migrations/` (or in the storage-foundations migration directory per that mission's convention) for `trust_anchors`, `trust_anchor_history`, `trust_identities`, `trust_backend_health`. Schema follows plan §5.1 column list exactly.
- **T002** — Implement `core/trust/anchor.go`: `InstallAnchor`, `RemoveAnchor`, `ListAnchors`, internal `Lookup(byKeyID|byOrgID|byPeerID)`. Apply precedence rules from FR-003: `pinned_peer > org_identifier > raw_public_key`. Persist tombstones for removed anchors so the verifier can return the distinct `RejAnchorRemoved` code (plan §4.1).
- **T003** — Implement `core/trust/audit.go` exposing `Emit(ctx, kind, payload)` writing to `core/event/` with emitter namespace `trust/`. Payload schema per plan §5.2 final paragraph: `anchor_id`, `algorithm`, `cache_state`, `rejection_code`, `backend_kind`, `payload_hash` (NOT raw payload), `result_timestamp`. No private-key bytes.
- **T004** — Wire the audit emitter into the `Verify` pipeline (WP03 step 10), `InstallAnchor`/`RemoveAnchor`, and the sign dispatcher (WP02 backend-unavailable hook). Guarantee exactly-one event per public-API call via a unit test using a real on-disk event log (charter testing standard: "no mocking of the event log").
- **T005** — Implement identity-collision persistence in `trust_identities`; on collision insert raise `RejIdentityCollision` (FR-015). Add a black-box test that installs anchor A for `agent-1`, then attempts to install/observe a different fingerprint for `agent-1`, asserts the second is rejected and audit logs the rejection.

## Acceptance criteria

- Schema migrations apply cleanly forward; rollback path documented if storage-foundations supports it.
- Every public TrustEngine call emits exactly one event into the real on-disk event log used by the test (NFR-005).
- Audit payloads carry zero credential-shaped material; an integration test scans the event log for known private-key fixtures and asserts none appear (NFR-004 evidence).
- `RejAnchorMissing` vs `RejAnchorRemoved` distinction is preserved via tombstones (plan §4.1).
- Identity-collision detection works for `(agent_id, fingerprint)` per FR-015.
- Append-only invariant holds: an integration test attempts to mutate a prior log entry through the event-log API and is rejected (C-003).

## Files to create/modify

- Create: SQL migrations in `core/trust/migrations/` (or storage-foundations location per that mission)
- Modify: `core/trust/anchor.go` (was a stub from WP01)
- Modify: `core/trust/audit.go` (was a stub from WP01)
- Modify: `core/trust/verify.go` (replace audit-hook placeholder from WP03 with real `audit.Emit`)
- Modify: `core/trust/sign.go` (wire `backend-unavailable` audit emit)
- Tests: `core/trust/anchor_test.go`, `core/trust/audit_test.go`, integration `core/trust/audit_integration_test.go` (uses real on-disk event log under `t.TempDir()`)

## Definition of done

- All five subtasks complete.
- Coverage on `anchor.go` and `audit.go` ≥ 80%.
- Charter testing standard satisfied: no mocking of event log; tests use real on-disk log.
- Audit event payloads conform to plan §5.2 final-paragraph schema; offline-replay test reconstructs decisions from the log alone (SC-006 evidence).
- Cross-mission dependency notes added to PR description citing storage-foundations + event-log mission IDs.
