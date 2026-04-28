---
work_package_id: "WP07"
title: "Error taxonomy and resolution-event emission"
dependencies:
  - "WP01"
  - "WP06"
planning_base_branch: "main"
merge_target_branch: "main"
branch_strategy: "Planning artifacts were generated on main; completed changes must merge back into main."
subtasks:
  - "T001: Define sentinel errors per FR-014 (backend-unavailable, reference-not-found, ...)"
  - "T002: Define ResolutionEvent payload (consumer_id, reference_kind, backend_kind, outcome, latency_ms, cache_hit)"
  - "T003: Define event kinds (secret.resolution.*, secret.preflight.*, secret.cache.*, secret.rotation.*)"
  - "T004: Wire event emission into Resolver.Resolve and Resolver.PreFlight paths"
  - "T005: Implement event-log Logger nil-tolerant pre-boot buffering (cycle break)"
  - "T006: Black-box integration test: real on-disk event log under t.TempDir() asserts no value bytes appear"
  - "T007: Audit-suite test scanning event log for plaintext after a session (NFR-003)"
phase: "Phase 7 - Errors and Events"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP07 – Error taxonomy and resolution-event emission

## Goal

Land the FR-014 typed-error taxonomy and the FR-012 resolution-event emission pipeline. Every resolution attempt — cache hit, cache miss, backend dispatch, success, failure, pre-flight, invalidation, rotation — produces a structured event into the append-only event log, with the resolved value never present. Also resolve the cycle between this mission and `event-log-01KQ1A3M` (event-log needs a salt reference resolved by this layer).

## Spec references

- FR-012 (Credential-resolution events): every resolution attempt recorded — consumer, reference, backend, success/failure, latency — with the resolved value never present.
- FR-014 (Error taxonomy): backend-unavailable, reference-not-found, reference-empty, permission-denied, format-invalid, rotated-mid-use.
- C-002 (No plaintext in any persisted state): event log is one of the named persisted states.
- C-003 (Append-only event log immutability): resolution and rotation events are append-only.
- C-005 (SOC 2 readiness): resolution events produce evidence sufficient for SOC 2 audit.
- NFR-003 (Plaintext leakage): zero matches across the audit matrix; event log is in-scope.

## Plan references

- §2 Architectural placement → `core/secrets/events/` and `core/secrets/errors/` subpackages.
- §5 Data model summary → ResolutionEvent kinds list (10 kinds) and error taxonomy block.
- §6 Integration points → event-log integration; "Cycle break" subsection.
- §8 Risk register → R9 (event-log emission cycle).
- §12 Acceptance mapping → FR-012, FR-014, C-002, C-003, C-005, NFR-003 partially map here.

## Subtasks

- Define sentinel errors in `core/secrets/errors/errors.go` exactly as plan §5 lists: `ErrBackendUnavailable`, `ErrReferenceNotFound`, `ErrReferenceEmpty`, `ErrPermissionDenied`, `ErrFormatInvalid`, `ErrRotatedMidUse`, `ErrInlinePlaintext`. Wrap with `fmt.Errorf("%w: ref=%s", err, redactedRefID)`.
- Define `ResolutionEvent` payload struct: `consumer_id`, `reference_kind` (NOT locator), `backend_kind`, `outcome`, `latency_ms`, `cache_hit`. Locator is hashed.
- Define event-kind string constants for all ten kinds: `secret.resolution.requested`, `cache_hit`, `cache_miss`, `backend_dispatched`, `ok`, `failed`, `secret.preflight.ok`, `secret.preflight.failed`, `secret.cache.invalidated`, `secret.rotation.detected`.
- Wire event emission into `Resolver.Resolve` and `Resolver.PreFlight`: every code path emits the appropriate kind via `event.Logger.Append(ctx, kind, payload)`.
- Implement nil-tolerant Logger handling: accept `*event.Logger` as nullable on `Resolver` construction; buffer events until the logger is wired, per plan §6 cycle-break.
- Black-box integration test using a real on-disk event log under `t.TempDir()` (charter rule: no event-log mocking when asserting audit/replay behavior).
- Audit-suite test: after a representative session, scan the event log file for any byte-pattern matching a known credential value. Zero matches required (NFR-003).

## Acceptance criteria

- `core/secrets/errors/errors.go` exposes all sentinel errors named in the plan; every backend WP's failures wrap one of them.
- `core/secrets/events/events.go` exposes the `ResolutionEvent` shape and the ten event-kind constants.
- The Resolver emits events at every code-path branch (request, cache hit/miss, dispatch, ok, failed, preflight ok/failed, invalidated, rotation).
- The locator never appears in any event payload; only the redaction-safe reference id and the `reference_kind` enum appear.
- Audit-suite test confirms zero plaintext value bytes in the event log after a session (NFR-003 partial).
- Cycle-break works: Resolver constructed with nil Logger buffers events until Logger wires in.
- Charter quality gates pass.

## Files to create / modify

- Create `core/secrets/errors/errors.go`.
- Create `core/secrets/errors/errors_test.go`.
- Create `core/secrets/events/events.go`.
- Create `core/secrets/events/events_test.go`.
- Update `core/secrets/secrets.go` and `core/secrets/preflight/preflight.go` to emit events.

## Definition of done

- WP01 (reference) and WP06 (Resolver assembled) dependencies satisfied.
- FR-012, FR-014 acceptance scenarios traceable to tests in this WP.
- Cross-mission dep: coordinates with `event-log-01KQ1A3M` — this WP consumes the `event.Logger` contract; the cycle break is implemented here, the salt-reference resolution path is consumed by event-log.
- Handoff: backend WPs (WP08–WP13) inherit the error taxonomy and event emission automatically through the Resolver.
