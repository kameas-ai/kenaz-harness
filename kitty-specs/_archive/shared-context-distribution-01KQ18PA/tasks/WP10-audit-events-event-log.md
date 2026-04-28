---
work_package_id: "WP10"
title: "Audit event emission to event-log (resolution, injection, scope, update)"
dependencies:
  - "WP05"
  - "WP07"
  - "WP08"
  - "event-log-01KQ1A3M"
planning_base_branch: "main"
merge_target_branch: "main"
branch_strategy: "Planning artifacts were generated on main; completed changes must merge back into main."
subtasks:
  - "T001"
  - "T002"
  - "T003"
  - "T004"
  - "T005"
phase: "Phase 10 - Audit events"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP10 – Audit events into event-log

## Goal

Implement `core/context/audit/`: emit every context-event kind enumerated in plan §5.4 to the append-only event log, route every payload through the redaction pipeline (per `event-log` C-004), and provide query helpers used by replay (WP07) and operator audit views. This is the SOC 2 audit story for context.

## Spec references

- FR-011 (Resolution-time audit event)
- FR-012 (Session-time audit event)
- FR-016 (Auto-expunge emits `scope_revoked`)
- FR-017 (Update surface — emits `update_available`)
- NFR-006 (100 % of resolution passes and injections produce sufficient event entries to reconstruct snapshot offline)
- SC-007 (Every resolution pass and every injection produces append-only entries)
- C-005 (Append-only invariant)
- C-006 (SOC 2 readiness)

## Plan references

- §4.8 (Injection emits one `injection_emitted` event per session)
- §5.4 (Full event-kind table: `resolution_started`, `pack_fetched`, `pack_verified`, `pack_rejected`, `override_applied`, `cache_served`, `resolution_completed`, `injection_emitted`, `scope_revoked`, `update_available`)
- §6 (Integration with `event-log-01KQ1A3M` — emitter id `context/`, redaction pipeline mandatory)
- Risk R7 (NFR-006 completeness verified by reconstruction tests)

## Subtasks

- T001 Define `core/context/audit/events.go` with one typed payload struct per event kind in plan §5.4; emitter id `context/`.
- T002 Wire emission points throughout the resolver: `resolution_started` at request entry; `pack_fetched`/`pack_verified`/`pack_rejected` per pack; `override_applied` per override; `cache_served` when in cache-only mode; `resolution_completed` at exit; `injection_emitted` at `Inject`; `scope_revoked` at expunge; `update_available` from the update surface.
- T003 Route every payload through the event-log redaction pipeline (`event-log` C-004). Confirm via test that no plaintext credential or scoped-pack content reaches an emitted event.
- T004 Provide query helpers (`audit.Query.SnapshotForSession`, `audit.Query.ContributingPacks`) used by replay (WP07) and operator audit views.
- T005 Reconstruction test (NFR-006 / SC-007): given only the event log entries for a session, reconstruct the resolved context and byte-compare against the stored snapshot.

## Acceptance criteria

- Every event kind in plan §5.4 has a typed payload, an emission site, and a unit test.
- Reconstruction test demonstrates 100 % offline reconstructability of resolved context from event log alone (validates NFR-006 / SC-007).
- Audit-suite assertion: zero plaintext credential bytes and zero role-scoped pack bytes leak into any emitted event (Risk R7).
- All payloads pass through redaction; black-box test against the real event log on disk per charter testing standards.

## Files to create/modify

- `core/context/audit/events.go`
- `core/context/audit/emitter.go`
- `core/context/audit/query.go`
- `core/context/audit/audit_test.go`
- Wiring updates in `core/context/resolver.go`, `core/context/inject/`, `core/context/access/`

## Definition of done

- Reconstruction test passes byte-identity check.
- No mocking of the event log — tests use real on-disk log under tempdir per charter rule.
- WP merged to main via squash-merge PR.
