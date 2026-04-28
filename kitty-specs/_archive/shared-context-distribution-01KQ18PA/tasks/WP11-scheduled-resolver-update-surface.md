---
work_package_id: "WP11"
title: "Scheduled resolver pass and update surface (diff, accept, defer)"
dependencies:
  - "WP02"
  - "WP09"
  - "WP10"
planning_base_branch: "main"
merge_target_branch: "main"
branch_strategy: "Planning artifacts were generated on main; completed changes must merge back into main."
subtasks:
  - "T001"
  - "T002"
  - "T003"
  - "T004"
phase: "Phase 10 - Audit events (extension)"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP11 – Scheduled resolver + update surface

## Goal

Plug context resolution into `core/scheduler/` so resolution runs at startup, on demand, and on a configurable interval (FR-006). Build the update surface that emits `update_available` events with a diff summary so operators can see proposed changes and explicitly accept or defer — updates never silently apply (FR-017). NFR-008 (update fairness) ensures a stalled org mirror does not block team-layer updates.

## Spec references

- FR-006 (Scheduled resolver via existing scheduler)
- FR-017 (Update surface: see updates, see diff, accept or defer)
- NFR-008 (Update fairness — bounded blocking budget per layer)
- C-007 (No covert egress — only configured channels, only during scheduled passes or operator-initiated)
- Acceptance scenario US1.1 (operator's harness picks up new version on next resolution cycle)

## Plan references

- §4.5 / §4.7 (Cache and policy interactions during scheduled passes)
- §5.4 (`update_available` event payload: pack id + current + available + diff summary)
- §6 (Workflow engine / `core/session/` integration not affected by scheduler)
- §7 v1.0 (Scheduled resolver pass via existing `core/scheduler/`)

## Subtasks

- T001 Register a scheduled job through `core/scheduler/` for context resolution: at startup, on operator demand, and at a configurable interval. No new scheduler primitive — consume existing API.
- T002 Implement update detection: compare lockfile-pinned hash with channel head; when newer is available, emit `update_available` event (WP10 payload) with a diff summary (added/removed/changed entry names, version delta).
- T003 NFR-008: parallelize per-layer update checks with a bounded per-layer time budget so a stalled org mirror does not block team or personal layer updates.
- T004 Provide an `Accept(packID, version)` API that updates the lockfile (through bundle's lockfile API) and triggers a re-resolution; absent operator acceptance, lockfile remains pinned to the prior version.

## Acceptance criteria

- Scheduled passes emit `update_available` events when channel head moves past the lockfile pin.
- Updates do not auto-apply — operator acceptance is required to advance the lockfile (validates FR-017 acceptance scenario).
- Update fairness test: a deliberately stalled mock channel for the org layer does not delay the team layer's update detection beyond the bounded budget (NFR-008).
- No outbound egress outside scheduled passes or operator-initiated calls (C-007).

## Files to create/modify

- `core/context/resolver.go` (scheduler hook + update detection)
- `core/context/update.go` (diff summary + accept API)
- `core/context/update_test.go`
- Test fixtures with version deltas and stall behaviors

## Definition of done

- Scheduler integration test demonstrates resolution at startup and on interval.
- Update-fairness test validates bounded per-layer budget.
- WP merged to main via squash-merge PR.
