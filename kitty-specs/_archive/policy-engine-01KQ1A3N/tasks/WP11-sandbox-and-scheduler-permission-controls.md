---
work_package_id: "WP11"
title: "sandbox_required and scheduler_permission control kinds"
dependencies:
  - "WP02"
  - "WP03"
  - "WP04"
  - "WP08"
planning_base_branch: "main"
merge_target_branch: "main"
branch_strategy: "Planning artifacts were generated on main; completed changes must merge back into main."
subtasks:
  - "T001"
  - "T002"
  - "T003"
  - "T004"
phase: "Phase 11 - sandbox_required + scheduler_permission"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP11 – sandbox_required and scheduler_permission control kinds

## Goal

Land the final two v1-catalog control kinds: `sandbox_required` (for
workflow node execution) and `scheduler_permission` (which schedules /
tasks the scheduler may dispatch). Together with WPs 06–10 these
complete the v1 catalog (eleven kinds total).

## Cross-mission dependencies

- **core/workflow** (consumer): node execution path consults
  `Evaluate` with `Action.Kind() == "workflow.node.execute"`.
- **core/scheduler** (consumer): cron registration consults
  `Evaluate` with `Action.Kind() == "scheduler.register"`; task
  dispatch consults `"scheduler.dispatch"`.

## Spec references

- FR-004 (control catalog v1 — workflow sandbox + scheduler
  permissions).
- FR-008 (denial taxonomy — `ReasonSandboxRequired`,
  `ReasonScheduleNotPermitted`).
- FR-005 (extensibility).
- NFR-006 (control-catalog parity — each kind enforced by a consumer).

## Plan references

- Plan §2 — `clauses/sandbox_required/`,
  `clauses/scheduler_permission/`.
- Plan §6 — consumer rows for `core/workflow`, `core/scheduler`.
- Plan §4 strict-narrowing — `BoolStricterWins` for
  `sandbox_required`; `SetIntersect` (over allowed schedule patterns
  / task ids) for `scheduler_permission`.

## Subtasks

- T001: `sandbox_required`: `params: { node_kinds: [<kind>...]
  required: bool }` (or simplified scalar form per design choice).
  Lowering matches `Action.Kind() == "workflow.node.execute"` and
  denies with `ReasonSandboxRequired` if `params.required == true`
  and `inputs.sandboxed == false`. Narrowing: `BoolStricterWins`.
- T002: `scheduler_permission`: `params: { allow: { cron_patterns:
  [<glob>...], task_ids: [<id>...] } }`. Lowering matches
  `Action.Kind() == "scheduler.register"` against `inputs.cron`
  pattern and `Action.Kind() == "scheduler.dispatch"` against
  `inputs.task_id`. Deny with `ReasonScheduleNotPermitted`.
  Narrowing: per-field `SetIntersect`.
- T003: Per-kind tests for schema, lowering, narrowing matrix, and
  end-to-end `Evaluate`. Black-box integration test driving a fake
  workflow runner and a fake scheduler through this WP's adapters
  per DIRECTIVE_036.
- T004: Register both kinds; update `RegisteredKinds()` test to
  assert the full **eleven-kind catalog** is present after this WP
  lands.

## Acceptance criteria

- Both kinds register; `RegisteredKinds()` returns exactly the
  eleven v1 kinds (User Story 5 acceptance scenario 1).
- A workflow node execution call without sandbox under a
  `sandbox_required: true` policy is denied with
  `ReasonSandboxRequired`.
- Scheduler registration with a cron pattern not matching any
  allowed glob is denied with `ReasonScheduleNotPermitted`.
- Narrowing matrix exhaustive across both kinds.

## Files to create/modify

- Create `core/policy/clauses/sandbox_required/{kind.go, schema.go,
  lower.go, merge.go, adapter.go}` + `*_test.go`.
- Create `core/policy/clauses/scheduler_permission/{kind.go,
  schema.go, lower.go, merge.go, adapter.go}` + `*_test.go`.
- Modify `core/policy/registry_test.go` to assert the full
  eleven-kind catalog.

## Definition of done

- Acceptance criteria pass.
- Charter quality gates clean.
- v1 control catalog complete: `provider_allowlist`,
  `model_allowlist`, `mcp_server_allowlist`,
  `mcp_capability_allowlist`, `a2a_peer_allowlist`, `network_tier`,
  `signature_required`, `redaction_strictness`, `cost_ceiling`,
  `sandbox_required`, `scheduler_permission`.
- Cross-mission dependencies (`core/workflow`, `core/scheduler`)
  documented in PR body.
- Conventional-commit message; commit attributed per DIRECTIVE_029.
