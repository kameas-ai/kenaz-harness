---
work_package_id: "WP06"
title: "Pre-flight validator with fail-closed startup"
dependencies:
  - "WP02"
  - "WP05"
planning_base_branch: "main"
merge_target_branch: "main"
branch_strategy: "Planning artifacts were generated on main; completed changes must merge back into main."
subtasks:
  - "T001: Define PreFlightResult struct and PreFlightStatus enum"
  - "T002: Implement Resolver.PreFlight iterating registered references"
  - "T003: Resolve + immediate Destroy each reference at startup"
  - "T004: Distinguish required vs optional references; fail-closed on required"
  - "T005: Surface per-reference errors with redaction-safe reference id"
  - "T006: Wire pre-flight into Resolver assembly (resolver entrypoint)"
  - "T007: Black-box integration tests against fake backends covering ok / not-found / degraded paths"
phase: "Phase 6 - Pre-flight"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP06 – Pre-flight validator with fail-closed startup

## Goal

Implement the pre-flight validator that runs at harness startup, attempts to resolve every registered `CredentialReference` exactly once, and fails the startup if any required reference is unresolvable. This is the moment misconfiguration becomes a clear, actionable startup error rather than a midnight-on-friday "your model call mysteriously failed". Also assemble the top-level `Resolver` orchestrator that ties WP02 (registry), WP03 (Secret), and WP05 (cache) together.

## Spec references

- FR-009 (Pre-flight validation): every configured reference validated at startup; failures surface immediately with the reference id.
- NFR-006 (Pre-flight completeness): 100% of configured references validated at startup.
- C-006 (Fail-closed): backend unavailability surfaces as a typed error; never falls back to a less-secure backend without explicit operator opt-in.
- User Story 5 (Pre-flight validation catches misconfiguration before first use).
- D8 (research): refuse to start if any required reference is unresolvable at pre-flight.

## Plan references

- §2 Architectural placement → `core/secrets/preflight/` subpackage and top-level `secrets.go` Resolver wiring.
- §3 Public API → `Resolver.PreFlight(ctx, refs) ([]PreFlightResult, error)`.
- §4 Internal layering → "Pre-flight validator" subsection.
- §5 Data model summary → `PreFlightResult` row.
- §12 Acceptance mapping → FR-009, NFR-006, C-006 map here.

## Subtasks

- Define `PreFlightResult` struct (`reference_id`, `status`, `error_code`, `tested_at`) and `PreFlightStatus` enum (`resolvable`, `unresolvable`, `optional_unresolvable`, `backend_degraded`).
- Implement `Resolver` orchestrator at the top-level `core/secrets` package wiring WP02 registry, WP05 cache, and (to-be) events.
- Implement `Resolver.PreFlight(ctx, refs []CredentialReference) ([]PreFlightResult, error)`: iterate references, call `Backend.Resolve`, immediately `Destroy` the returned Secret, record status.
- Distinguish required vs optional references via a flag on `CredentialReference` (or an adjacent registration option); required-unresolvable causes startup failure.
- Surface per-reference errors using the redaction-safe reference id from WP01 — never the locator.
- Black-box integration tests using a fake backend that simulates `ok`, `not-found`, `permission-denied`, `backend-unavailable`. Per DIRECTIVE_036, drive only via the public `Resolver` surface.

## Acceptance criteria

- `core/secrets/preflight/preflight.go` and the top-level `core/secrets/secrets.go` Resolver compile and integrate WP02/WP03/WP05.
- Pre-flight iterates 100% of registered references (NFR-006).
- A required-unresolvable reference fails startup with a structured error naming the reference id (FR-009).
- An optional-unresolvable reference does not fail startup; status is recorded (`optional_unresolvable`).
- No silent fallback (C-006): a backend-unavailable result surfaces as the typed error, never as a switch to a different backend.
- Tests achieve ≥80% line coverage on `core/secrets/preflight/`.
- Charter quality gates pass.

## Files to create / modify

- Create `core/secrets/preflight/preflight.go`.
- Create `core/secrets/preflight/preflight_test.go`.
- Update `core/secrets/secrets.go` to assemble the `Resolver` (registry + cache + preflight).

## Definition of done

- WP02, WP05 dependencies satisfied; cache and registry wired through the Resolver.
- FR-009, NFR-006, C-006 acceptance scenarios traceable to tests in this WP.
- Pre-flight emits structured results ready to be event-logged by WP07.
- Handoff: Resolver surface stable for backend WPs (WP08–WP13) to be plugged in via `registry.Register`.
