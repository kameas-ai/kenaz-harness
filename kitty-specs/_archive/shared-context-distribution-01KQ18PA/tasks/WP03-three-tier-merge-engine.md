---
work_package_id: "WP03"
title: "Three-tier merge engine with override-by-precedence policy"
dependencies:
  - "WP01"
planning_base_branch: "main"
merge_target_branch: "main"
branch_strategy: "Planning artifacts were generated on main; completed changes must merge back into main."
subtasks:
  - "T001"
  - "T002"
  - "T003"
  - "T004"
  - "T005"
phase: "Phase 3 - Layered merge engine"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP03 – Three-tier layered merge engine

## Goal

Implement `core/context/layer/`: the deterministic merge engine that resolves org → team → personal packs into an ordered `[]ResolvedEntry` plus an override registry. This WP delivers the v1 default `override-by-precedence` policy (silent winner, override recorded) — strict mode (`fail-on-conflict`) is wired in WP04. Workflow / agent scoping (FR-015) is applied **before** override evaluation.

## Spec references

- FR-001 (Three-tier layering with personal > team > org precedence)
- FR-002 (Named entries — override key)
- FR-008 (Conflict detection — default override-by-precedence)
- FR-015 (Workflow / agent-scoped entries filtered before override)
- SC-002 (Override precedence correct 100 % across test matrix)

## Plan references

- §3 (`ResolutionSnapshot`, `OverrideRecord`, `ConflictReport`)
- §4.3 (Three-tier merge engine, entry-name keyed override registry)
- §4.7 (Conflict policy table — this WP delivers the default lane)
- Open Question §9.2 (override-by-precedence is the v1 default)

## Subtasks

- T001 Define `core/context/layer/` package with `Merger`, `OverrideRecord`, `ResolvedEntry` per plan §3 / §4.3.
- T002 Implement workflow / agent-scope filtering: drop entries whose frontmatter restricts them to a different workflow/agent than the active `ResolveRequest`.
- T003 Implement override-by-precedence merge: collect entries by name, pick the highest-precedence layer's value, record an `OverrideRecord` for every override (winner layer, loser layer, entry name).
- T004 Emit deterministic ordering: sorted by entry name; merge result must be byte-stable across runs (necessary for SC-005 replay byte-identity).
- T005 Table-driven tests: org-only, org+team, org+team+personal, workflow-scoped exclusion, multiple overrides per session, no-conflict baseline.

## Acceptance criteria

- Given org/team/personal fixtures with overlapping entries, the merger produces the spec's expected winner 100 % of the time across the test matrix (validates SC-002).
- The merge result is byte-stable across repeated runs with the same inputs.
- Workflow-scoped entries do not appear in resolutions for other workflows.
- An entry present only in the org pack still surfaces from the team layer's resolution view (acceptance scenario US2-2).
- ≥80 % coverage; tests run under `-race`.

## Files to create/modify

- `core/context/layer/merge.go`
- `core/context/layer/scope.go`
- `core/context/layer/types.go`
- `core/context/layer/merge_test.go`
- `core/context/layer/testdata/...` (override matrix fixtures)

## Definition of done

- All test matrix cases pass; deterministic ordering verified.
- Override records carry enough information for downstream audit (winner, loser, entry name, source pack ids).
- No imports of `core/trust/`, `core/event/`, or channel code from this package.
- WP merged to main via squash-merge PR.
