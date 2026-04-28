---
work_package_id: "WP12"
title: "Resolution events emission to the harness event log"
dependencies:
  - "WP09"
  - "WP10"
  - "WP11"
planning_base_branch: "main"
merge_target_branch: "main"
branch_strategy: "Planning artifacts were generated on main; completed changes must merge back into main."
subtasks:
  - "T001"
  - "T002"
  - "T003"
phase: "Phase 11 - Events"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP12 – Resolution events emission

## Goal

Define the bundle event kinds and emit them through the injected `events.Emitter` interface. Every fetch, verification, override, and activation must be recorded for SOC 2 audit replay; the bundle layer never appends to the event log directly.

## Spec references

- FR-010 Resolution events
- C-003 Append-only event log immutability
- C-005 SOC 2 readiness
- US2 (P1) — conflict resolution recorded in the resolution event

## Plan references

- Plan §2 `core/bundle/events/` subpackage
- Plan §5.4 Event kinds table (12 typed kinds with stable payloads)
- Plan §6.4 event-log integration (Emit only; redaction + append-only enforcement live elsewhere)

## Cross-mission dependencies

- **event-log**: provides `events.Emitter.Emit(kind, payload)`. Until that mission lands, this WP defines the interface and a stub emitter for tests.

## Subtasks

- T001 Define `events.Emitter` interface and event kind constants in `core/bundle/events/events.go` exactly per Plan §5.4: `bundle_resolved`, `artifact_fetched`, `artifact_verified`, `artifact_activated`, `artifact_rejected`, `bundle_signature_verified`, `bundle_signature_failed`, `lockfile_updated`, `cache_hit`, `cache_miss`, `channel_unreachable`, `artifact_deactivated`.
- T002 Define typed payload structs for each kind with the fields listed in Plan §5.4. No credentials in any payload (channel auth is by reference, never resolved-value — defense in depth).
- T003 Wire emission calls from WP09 (resolve completion, channel unreachable), WP10 (fetched / verified / rejected / signature outcomes / cache hit/miss / lockfile_updated), and WP11 (activated, deactivated).
- T004 Provide a `events/test_emitter.go` capturing emitted events for assertion in unit/integration tests across this mission.

## Acceptance criteria

- Every state transition in the resolution pipeline emits exactly one event of the correct kind with the documented payload.
- No payload contains a resolved credential value (verified by a static-analysis check or audit test).
- Events emit through the injected `Emitter` only; `core/bundle/` has no import of any event-log persistence package.
- Test emitter captures the full event stream for a happy-path resolve and asserts kind ordering matches Plan §5.4.

## Files to create/modify

- `core/bundle/events/events.go` (new — kinds + Emitter interface)
- `core/bundle/events/payloads.go` (new — typed payload structs)
- `core/bundle/events/test_emitter.go` (new — capturing emitter for tests)
- Wiring edits in `core/bundle/resolver/fetch.go`, `activate.go`, `preflight.go` (extend WP09/10/11 outputs).

## Definition of done

- All acceptance criteria pass.
- Event kind set matches Plan §5.4 exactly — no extra, no missing.
- Test emitter is reused by WP09–WP13 integration tests.
- Credential-leakage audit test passes (no fields containing the substring `secret`, `token`, or `password` in any payload schema).
