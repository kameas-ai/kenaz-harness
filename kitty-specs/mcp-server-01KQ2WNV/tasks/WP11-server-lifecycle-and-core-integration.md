---
work_package_id: "WP11"
title: "Server lifecycle wiring with core.Core.Start / Shutdown"
dependencies:
  - "WP01"
  - "WP03"
  - "WP04"
  - "WP05"
  - "WP07"
  - "WP08"
  - "WP09"
  - "WP10"
planning_base_branch: "feat/wire-integration"
merge_target_branch: "feat/wire-integration"
branch_strategy: "Planning artifacts were generated on feat/mcp-plans; completed changes merge into feat/wire-integration."
subtasks:
  - "T001"
  - "T002"
  - "T003"
  - "T004"
  - "T005"
phase: "Phase 5 - Integration"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP11 – Server lifecycle wiring with core.Core.Start / Shutdown

## Goal

Wire the MCP server into `core.Core` so its lifecycle integrates with
the harness's `Start`/`Shutdown`. Concrete server constructed from
`Options` populated by the embedder; transports activated per config;
graceful drain on shutdown.

## Spec references

- FR-019 — Lifecycle: `Start` / `Shutdown` integrate with `core.Core`.
- FR-020 — Graceful drain on shutdown.

## Plan references

- §6 Integration Points — all six in-tree dependencies.
- §4 Internal Layering — server `Start` order: load config → register
  transports → register tools → bind listeners → enter serve loop.

## Subtasks

- T001 — Implement
  `core/mcp/server/server_impl.go`: concrete `Server` struct
  implementing the interface. Holds `Options`, transport list, tool
  catalog, audit emitter.
- T002 — Implement `Start(ctx)`:
  - Load + validate config.
  - Resolve bearer-token cred ref (if configured).
  - Register transports per config (`stdio.enabled`, `http.enabled`).
  - Register built-in tools (filtered by config-enabled flags).
  - For each enabled transport, call `Listen(ctx, accept)` in a
    goroutine.
  - Emit `mcp.server/listener_started` per transport.
- T003 — Implement `Shutdown(ctx)`:
  - Stop accepting new connections (per transport).
  - Wait up to `drain_timeout_ms` for in-flight handlers.
  - Force-close after timeout.
  - Emit `mcp.server/listener_stopped` per transport.
- T004 — Update `core/core.go`:
  - Add `MCPServer server.Server` field to `Subsystems` and `Core`.
  - In `Core.Start`, call `MCPServer.Start(ctx)` after other
    subsystems are up.
  - In `Core.Shutdown`, call `MCPServer.Shutdown(ctx)` before
    closing the event log.
- T005 — Tests: lifecycle integration test against a fake transport;
  asserts `Start` succeeds, sessions accept, `Shutdown` drains and
  closes; lifecycle event ordering verified.

## Acceptance criteria

- `go test ./core/...` passes including the new core integration.
- `core/core.go` modifications are minimal (additive, preserves
  existing behavior).
- Lifecycle event ordering is deterministic and audited.
- Shutdown completes within `drain_timeout_ms + 1s` slack even when
  handlers are stuck (force-close fallback).

## Files to create / modify

- `core/mcp/server/server_impl.go`
- `core/mcp/server/server_impl_test.go`
- `core/core.go` (additive)
- `core/core_test.go` (lifecycle test extension)

## Definition of done

- All subtasks complete; tests green; lint clean.
- PR merged into `feat/wire-integration`.
