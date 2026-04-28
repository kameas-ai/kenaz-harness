---
work_package_id: "WP02"
title: "JSON-RPC framing reuse — share or refactor core/mcp/{client,}/jsonrpc"
dependencies:
  - "WP01"
planning_base_branch: "feat/wire-integration"
merge_target_branch: "feat/wire-integration"
branch_strategy: "Planning artifacts were generated on feat/mcp-plans; completed changes merge into feat/wire-integration."
subtasks:
  - "T001"
  - "T002"
  - "T003"
phase: "Phase 1 - Core skeleton"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP02 – JSON-RPC framing reuse — share or refactor core/mcp/{client,}/jsonrpc

## Goal

Establish the shared JSON-RPC framing surface so the MCP server can
reuse the same types as the MCP client. Two options:

- (A) Import `core/mcp/client/jsonrpc/` directly. Acceptable if the
  layer name stays meaningful in both directions.
- (B) Refactor to `core/mcp/jsonrpc/` (one-time move) so the package
  name is symmetric.

This WP picks (B) by default because the JSON-RPC layer is independent
of client/server directionality and the symmetric path is clearer to
future readers.

## Spec references

- FR-003 — Handshake protocol; uses the same JSON-RPC method shapes as
  the client mission.
- FR-004 — `tools/list` and `tools/call`; same parameter / result types.

## Plan references

- §2 Architectural Placement — `core/mcp/jsonrpc/` reuse.
- Research §2.4 — symmetric framing reuse.

## Subtasks

- T001 — If `core/mcp/client/jsonrpc/` exists from the mcp-client
  mission, move it to `core/mcp/jsonrpc/` via `git mv`. Update all
  imports under `core/mcp/client/`. If the client mission has not yet
  shipped, create `core/mcp/jsonrpc/` directly here using the spec
  from the client mission's WP02.
- T002 — Add server-direction helper code: a method dispatch helper
  for handling inbound requests (the client mission only sends them).
  This lands as `core/mcp/jsonrpc/dispatch.go` or `core/mcp/server/dispatcher.go`
  (preferred — keep dispatcher in server package, jsonrpc stays
  framing-only).
- T003 — Tests: assert that round-trips of every method type still
  pass after the move; assert that the dispatcher correctly identifies
  inbound request vs notification.

## Acceptance criteria

- `go build ./...` succeeds across both `core/mcp/client/` (from
  the client mission) and `core/mcp/server/`.
- `go vet` clean; `golangci-lint` clean.
- Coverage ≥ 80 % on `core/mcp/jsonrpc/`.
- No regressions in client-mission tests.

## Files to create / modify

- `core/mcp/jsonrpc/*.go` (moved from `core/mcp/client/jsonrpc/` if
  present, or created here).
- `core/mcp/client/**/*.go` (import path updates).
- `core/mcp/server/dispatcher.go` (or `core/mcp/jsonrpc/dispatch.go`).

## Definition of done

- All subtasks complete; tests green; lint clean.
- PR merged into `feat/wire-integration`.
- Coordinated with the mcp-client mission via shared planning notes
  (the client mission's WP02 originally sites the framing under
  `core/mcp/client/jsonrpc/`; this WP either takes the move or
  reaffirms the original location).
