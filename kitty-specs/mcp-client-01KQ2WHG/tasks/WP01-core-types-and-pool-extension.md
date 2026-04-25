---
work_package_id: "WP01"
title: "Core types and Pool interface extension in core/mcp"
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

# Work Package Prompt: WP01 – Core types and Pool interface extension in core/mcp

## Goal

Extend the existing `core/mcp/pool.go` interface (already imported by
`core/core.go`) with the new methods, types, and typed-error taxonomy this
mission's implementation depends on. This WP establishes the seam every
downstream WP plugs into.

## Spec references

- FR-002 — Bundle artifact `kind: mcp_server` (drives `ServerSpec` extension).
- FR-005 — Tool round-trip (drives `Tool`, `ToolCallResult`).
- FR-006 — Prompt round-trip (drives `Prompt`, `PromptResult`).
- FR-007 — Resource round-trip (drives `Resource`, `ResourceContent`).
- FR-014 — Pool lifecycle (drives `Reload`).
- FR-017 — Pluggable transport contract (drives `Transport`,
  `TransportFactory`).
- FR-018 — Capability gate (drives `ErrServerUnknown`, `ErrToolUnknown`).
- C-001 — `core/mcp/client/` is the only seam.

## Plan references

- §2 Architectural Placement — keep `core/mcp/pool.go` as the public
  façade; implementation in `core/mcp/client/`.
- §3 Public API — full canonical signature set this WP materializes.
- §4 Internal Layering — the connection state machine consumes these
  types.

## Subtasks

- T001 — Audit existing `core/mcp/pool.go` and migrate / extend it
  without breaking `core/core.go` (or any test that compiles against it
  today).
- T002 — Add `ServerSpec` extension fields (`Headers`, `Roots`, `Retry`,
  `Limits`); add `Prompt`, `PromptArgument`, `Resource`,
  `ToolCallResult`, `ContentBlock`, `PromptResult`, `ResourceContent`,
  `ServerHealth`, `RetryPolicy`, `Limits`, `SamplingRequest`,
  `SamplingResponse`.
- T003 — Extend the `Pool` interface with `Reload`, `Prompts`,
  `GetPrompt`, `Resources`, `ReadResource`, `Health`,
  `RegisterTransport`.
- T004 — Define `Transport` interface and `TransportFactory` type;
  define `TransportDeps` (Secrets resolver + Audit emitter facets).
- T005 — Define typed error taxonomy: `ErrServerUnknown`, `ErrToolUnknown`,
  `ErrTransportFailure`, `ErrHandshakeFailed`, `ErrRetryBudgetExhausted`,
  `ErrCancelled`, `ErrInvalidParams`, `ErrMethodNotFound`,
  `ErrServerError`, `ErrSamplingUnavailable`, `ErrPolicyDenied`,
  `ErrResultTooLarge`, `ErrSamplingDepthExceeded`,
  `ErrReplayMissingRecording`. Provide classification helpers
  (`IsTransient(err) bool`).

## Acceptance criteria

- `go build ./core/mcp/...` succeeds with no transport-specific imports
  in `core/mcp/pool.go`.
- `go vet ./core/mcp/...` clean.
- `core/core.go` continues to compile against the extended Pool field
  (no breaking change to `Subsystems.MCP` zero value).
- Table-driven unit tests under `core/mcp/pool_test.go` cover error
  classification (`IsTransient` over each typed error) and zero-value
  behavior of `Pool` consumers.
- No file under `core/mcp/` (this WP only touches root) imports any
  transport-specific package.

## Files to create / modify

- `core/mcp/pool.go` (extended types and interface).
- `core/mcp/errors.go` (typed error taxonomy + helpers).
- `core/mcp/pool_test.go` (table-driven tests for error classification).
- `core/mcp/doc.go` (package doc string referencing the architectural
  invariant).

## Definition of done

- All subtasks complete; tests green; `go vet` and `golangci-lint run`
  clean for `./core/mcp/...`.
- Public types match plan §3 signatures; deviations recorded in commit
  message or ADR per DIRECTIVE_003.
- PR opened against `feat/mcp-client-01KQ2WHG`; merges into
  `feat/wire-integration`.
