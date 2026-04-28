---
work_package_id: "WP04"
title: "Connection state machine, handshake, and JSON-RPC routing"
dependencies:
  - "WP01"
  - "WP02"
  - "WP03"
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
  - "T007"
phase: "Phase 2 - Connection layer"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP04 – Connection state machine, handshake, and JSON-RPC routing

## Goal

Implement `core/mcp/client/connection.go` — one connection per server,
carrying the JSON-RPC routing table, performing the MCP `initialize`
handshake, and serializing send/receive over a `Transport`. Inbound
server-initiated requests (sampling, roots) are routed to embedder
handlers.

## Spec references

- FR-004 — `initialize` / `initialized` handshake with capability +
  protocol version negotiation.
- FR-005 — Tool round-trip routed via JSON-RPC id tracking.
- FR-009 — Sampling: server → client requests dispatched to handlers.
- FR-010 — Roots: server → client requests dispatched to handlers.
- FR-011 — Cancellation honors `context.Context`; transport closes
  within 1 s p99.
- FR-012 — Distinguish transient (transport drops, EOF) from
  non-transient (`MethodNotFound`, `InvalidParams`) errors.

## Plan references

- §4 Internal Layering — connection state machine lifecycle and routing.
- §3 Public API — `SamplingHandler`, `RootsProvider`.
- Risk R3 — buffer sizing for stdio.
- Risk R5 — cancellation propagation.

## Subtasks

- T001 — Implement `connection` struct with `state`, `transport`,
  `pending` map (id → response chan), `serverInfo`, `capabilities`,
  and per-connection `context.Context` + cancel.
- T002 — Implement `connection.initialize(ctx)` — sends `initialize`
  request, reads response, performs version negotiation via
  `jsonrpc.Negotiate`, sends `notifications/initialized`, transitions
  state to `Ready`.
- T003 — Implement read goroutine: decode incoming frame; if response,
  route to `pending[id]`; if request, dispatch to inbound handler; if
  notification, dispatch to notification handler.
- T004 — Implement write goroutine + send queue with bounded backpressure.
- T005 — Implement `connection.call(ctx, method, params)` — generates
  id, registers pending channel, sends, waits on channel or ctx
  cancellation, emits `notifications/cancelled` on ctx cancel.
- T006 — Define `SamplingHandler` and `RootsProvider` interfaces and
  wire them in. Default handlers return typed errors when not provided
  (`ErrSamplingUnavailable`, empty roots list).
- T007 — Tests: against an in-memory fake `Transport` that exchanges
  raw bytes via channels, verify: handshake success, handshake version
  mismatch (`ErrHandshakeFailed`), in-flight call cancelled by ctx,
  inbound sampling routed to handler, inbound roots/list returns the
  bundle-declared roots, malformed frame triggers `mcp/protocol_warning`
  but does NOT abort the connection.

## Acceptance criteria

- `go test ./core/mcp/client/...` (this WP's surface) passes;
  coverage ≥ 80 %.
- Race-free under `go test -race`.
- Handshake test asserts `protocolVersion`, `capabilities`, `serverInfo`
  populated on the connection after `Ready`.
- Cancellation test asserts ctx cancel → `notifications/cancelled` sent
  to server within 100 ms; pending channel closed within 100 ms.
- Inbound-request routing test asserts `sampling/createMessage` reaches
  the handler with verbatim params.
- No dependencies on transport-specific stdlib packages (`os/exec`,
  HTTP types) in `connection.go`.

## Files to create / modify

- `core/mcp/client/connection.go`
- `core/mcp/client/connection_test.go`
- `core/mcp/client/handlers.go` (SamplingHandler / RootsProvider
  interfaces).
- `core/mcp/client/internal/idgen/idgen.go` (monotonic id generator
  for JSON-RPC ids).

## Definition of done

- All subtasks complete; tests green; lint clean.
- Architectural-integrity invariant checked: connection.go imports
  ONLY `core/mcp/client/jsonrpc/`, `core/mcp/client/transport/`,
  `core/mcp` (parent for types), and stdlib.
- PR merged into `feat/wire-integration`.
