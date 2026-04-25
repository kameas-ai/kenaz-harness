---
work_package_id: "WP04"
title: "Per-session state machine, handshake, and dispatcher"
dependencies:
  - "WP01"
  - "WP02"
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
phase: "Phase 2 - Session layer"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP04 – Per-session state machine, handshake, and dispatcher

## Goal

Implement `core/mcp/server/session.go` and `core/mcp/server/dispatcher.go`
— one session per connected client, performing the MCP handshake,
tracking in-flight handler contexts (for cancellation), and dispatching
JSON-RPC requests to per-method handlers.

## Spec references

- FR-003 — `initialize` / `initialized` handshake with version
  negotiation.
- FR-010 — Cancellation via `notifications/cancelled`.
- FR-011 — Distinguish transient from non-transient errors.

## Plan references

- §4 Internal Layering — session state machine.
- Risk R5 — handler concurrency on a single session.
- Risk R6 — cancellation propagation.
- Risk R12 — panic-recover on handlers.

## Subtasks

- T001 — Implement `session` struct with state, transport, in-flight
  map (request id → cancel func), per-session ctx, audit emitter ref,
  policy guard ref.
- T002 — Implement `session.run(ctx)` — read frames, dispatch to
  handlers in goroutines (per-handler ctx), write responses.
- T003 — Implement `session.handshake()` — initialize round-trip, version
  negotiation via `core/mcp/jsonrpc.Negotiate`, set capabilities,
  transition to Ready. Handshake timeout enforced from config.
- T004 — Implement `dispatcher.go`: method-name → handler dispatch
  table; built once at server start. Includes reserved-method
  validation.
- T005 — Implement `notifications/cancelled` handler that cancels the
  in-flight ctx for the cited request id.
- T006 — Tests: against an in-memory fake `SessionTransport` that
  exchanges raw bytes via channels: handshake success, version
  mismatch (`ErrInvalidParams` with negotiation failure), in-flight
  request cancelled by notification, malformed frame triggers
  `mcp.server/protocol_warning` but session continues, panic in
  handler returns `InternalError` to client.

## Acceptance criteria

- `go test ./core/mcp/server/...` passes; coverage ≥ 80 %.
- Race-free under `-race`.
- Cancellation responsiveness < 1 s from notification to handler ctx
  cancel.
- Panic-recover wraps every handler invocation.
- Architectural-integrity invariant: session.go and dispatcher.go
  import only `core/mcp/jsonrpc/`, `core/mcp/server` (parent), and
  stdlib (no transport-specific imports).

## Files to create / modify

- `core/mcp/server/session.go`
- `core/mcp/server/session_test.go`
- `core/mcp/server/dispatcher.go`
- `core/mcp/server/dispatcher_test.go`

## Definition of done

- All subtasks complete; tests green; lint clean.
- PR merged into `feat/wire-integration`.
