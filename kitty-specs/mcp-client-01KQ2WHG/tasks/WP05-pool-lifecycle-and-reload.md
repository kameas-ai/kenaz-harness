---
work_package_id: "WP05"
title: "Pool lifecycle (Open/Close/Reload) and concurrent fan-out"
dependencies:
  - "WP01"
  - "WP03"
  - "WP04"
planning_base_branch: "feat/wire-integration"
merge_target_branch: "feat/wire-integration"
branch_strategy: "Planning artifacts were generated on feat/mcp-plans; completed changes merge into feat/wire-integration."
subtasks:
  - "T001"
  - "T002"
  - "T003"
  - "T004"
  - "T005"
  - "T006"
phase: "Phase 2 - Connection layer"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP05 – Pool lifecycle (Open/Close/Reload) and concurrent fan-out

## Goal

Implement `core/mcp/client/pool.go` — the `Pool` interface implementation
that owns the set of connections, supports atomic reloads (preserving
unchanged servers), routes calls to the appropriate connection, and
surfaces aggregate health.

## Spec references

- FR-014 — Pool lifecycle: `Open(specs)`, `Close()`,
  `Reload(newSpecs)`; reload preserves unchanged servers.
- FR-018 — Capability gate: calls against unknown servers / tools return
  typed errors before any wire call.
- US5 — Bundle reload swaps the pool atomically.

## Plan references

- §4 Internal Layering — pool open / reload pipeline.
- Risk R6 — reload preserves in-flight calls.
- Risk R11 — child-process reaper / cleanup.
- Open Question 5 — concurrent open parallelism cap (default 16).
- Open Question 6 — stdio kill grace period (default 5 s SIGTERM →
  SIGKILL).

## Subtasks

- T001 — Implement `Pool` struct with `specs map[string]ServerSpec`,
  `connections map[string]*connection`, `transports` lookup map, and
  pool-level `context.Context` + cancel.
- T002 — Implement `Pool.Open(ctx, specs)` — pre-flight credentials
  (delegated to WP06 helper, but stub here), parallel-spawn connections
  capped at 16 (configurable), wait for all to reach `Ready` or
  `Unhealthy`, return aggregate health.
- T003 — Implement `Pool.Reload(ctx, newSpecs)` — diff `specs` vs
  `newSpecs` by id; close removed; spawn added; preserve unchanged
  (deep-equal `ServerSpec`); spawn-replace any whose `ServerSpec`
  changed.
- T004 — Implement `Pool.Close(ctx)` — graceful drain (configurable
  drain timeout, default 5 s), then close all connections.
- T005 — Implement `Pool.Tools`, `Pool.Prompts`, `Pool.Resources`,
  `Pool.Health` as fan-out queries against `connections` with
  consistent ordering by server id.
- T006 — Tests: open 5 servers (fake transports), call tools, reload to
  a different set of 5 (3 unchanged, 1 removed, 1 added, 1 changed),
  assert connection identity preservation for the 3 unchanged servers,
  close cleanly. Race-free under `go test -race`.

## Acceptance criteria

- `go test ./core/mcp/client/...` passes; coverage ≥ 80 %.
- Race-free under `-race`.
- Reload test asserts the 3 unchanged connections retain their ptr
  identity (no respawn).
- `Pool.Open` failure on one server does NOT prevent the others from
  being opened (per US1 Acceptance 4).
- `Pool.Close` returns all child processes reaped (no zombies on
  macOS / Linux test harness).
- `Pool.Tools()` returns a stable, sorted ordering by `(server, name)`.

## Files to create / modify

- `core/mcp/client/pool.go`
- `core/mcp/client/pool_test.go`
- `core/mcp/client/internal/diff/diff.go` (server-set diff helper).

## Definition of done

- All subtasks complete; tests green; lint clean.
- PR merged into `feat/wire-integration`.
