---
work_package_id: "WP01"
title: "Core Server façade, Options, Tool/Session contracts, and error taxonomy"
dependencies: []
planning_base_branch: "feat/wire-integration"
merge_target_branch: "feat/wire-integration"
branch_strategy: "Planning artifacts were generated on feat/mcp-plans; completed changes merge into feat/wire-integration."
subtasks:
  - "T001"
  - "T002"
  - "T003"
  - "T004"
  - "T005"
phase: "Phase 1 - Core skeleton"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP01 – Core Server façade, Options, Tool/Session contracts, and error taxonomy

## Goal

Establish the canonical Go types and interfaces in
`core/mcp/server/server.go` that every downstream WP depends on:
`Server` interface, `Options` (the bootstrap struct), `Tool` and
`Session` contracts, `Transport` and `SessionTransport`, and the typed
error taxonomy.

## Spec references

- FR-013 — Pluggable tool contract (drives `Tool` interface).
- FR-019 — Lifecycle (drives `Server.Start` / `Shutdown`).
- FR-009 — Sampling (drives `Session.Sample`, `SamplingRequest`).
- FR-010 — Cancellation (drives ctx propagation in tool contract).
- FR-011 — Typed errors (drives error taxonomy).
- C-001 — Single seam.

## Plan references

- §2 Architectural Placement — server lives in `core/mcp/server/`.
- §3 Public API — full canonical signature set.
- §4 Internal Layering — types consumed by every downstream layer.

## Subtasks

- T001 — Create `core/mcp/server/server.go` with the `Server`
  interface, `Options` struct, `Tool`, `Session`, `ToolResult`,
  `ContentBlock`, `ClientInfo`, `SessionSnapshot`, `SamplingRequest`,
  `SamplingResponse`, `Progress`, `LogLevel`.
- T002 — Define `Transport` and `SessionTransport` interfaces;
  define `TransportFactory` type and `TransportOpts` carrier.
- T003 — Define `PolicyGuard` interface (gates `tools/call` and
  sampling); ship a no-op default implementation.
- T004 — Define typed error taxonomy: `ErrInvalidParams`,
  `ErrMethodNotFound`, `ErrInternalError`, `ErrPolicyDenied`,
  `ErrSamplingUnavailable`, `ErrAuthDenied`, `ErrOriginDenied`,
  `ErrResourceTooLarge`, `ErrSessionClosed`, `ErrShutdown`,
  `ErrSamplingDepthExceeded`. Provide JSON-RPC code mapping.
- T005 — Tests: round-trip every type through `json.Marshal` and
  `json.Unmarshal`; assert error → JSON-RPC code mapping.

## Acceptance criteria

- `go build ./core/mcp/server/...` succeeds (with no dependents yet).
- `go vet` clean; `golangci-lint` clean.
- Coverage ≥ 80 %.
- No imports of Wails or frontend types.

## Files to create / modify

- `core/mcp/server/server.go`
- `core/mcp/server/errors.go`
- `core/mcp/server/server_test.go`
- `core/mcp/server/doc.go`

## Definition of done

- All subtasks complete; tests green; lint clean.
- PR merged into `feat/wire-integration`.
