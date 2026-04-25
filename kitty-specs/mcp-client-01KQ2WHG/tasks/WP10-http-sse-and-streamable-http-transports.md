---
work_package_id: "WP10"
title: "HTTP+SSE legacy and streamable-HTTP transports"
dependencies:
  - "WP02"
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
  - "T007"
phase: "Phase 3 - Transports"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP10 – HTTP+SSE legacy and streamable-HTTP transports

## Goal

Implement two HTTP-based transports under `core/mcp/client/httpsse/` and
`core/mcp/client/streamable/`. Both use Go's stdlib `net/http`. The first
is the legacy transport (two connections per session); the second is the
modern single-endpoint transport with content-type discriminated single
response vs streaming response.

## Spec references

- FR-001 — HTTP+SSE and streamable-HTTP transports.
- FR-003 — HTTP header values may carry credential references.
- C-006 — These transports are network; stdio remains the local-first path.

## Plan references

- §2 Architectural Placement — each transport in its own package.
- Research §3.2 / §3.3 — transport-specific notes.
- Risk R8 — strict SSE parser.

## Subtasks

- T001 — Implement `httpsse.NewTransport`: two-connection design.
  `GET /sse` for server pushes (long-lived), `POST /` for outbound
  requests. Session id from server's first SSE event. Use
  `http.Client` with no `Timeout` (use ctx).
- T002 — Implement strict SSE parser per RFC 8895:
  `data:`-prefixed lines accumulated until blank line; reject
  malformed frames with `mcp/protocol_warning`.
- T003 — Implement `streamable.NewTransport`: single endpoint;
  `POST` outbound; content-type discrimination on response
  (`application/json` vs `text/event-stream`); session id via
  `Mcp-Session-Id` header.
- T004 — Resolve header credential refs through `deps.Secrets`; never
  log raw header values; assert no plaintext credential bytes appear
  in audit payloads.
- T005 — Implement `Close()` for both transports — closes the SSE
  connection (httpsse), drains in-flight requests (streamable), no
  child processes to reap.
- T006 — Register both transports via
  `transport.Register("http_sse", httpsse.NewTransport)` and
  `transport.Register("streamable_http", streamable.NewTransport)` in
  `init()`.
- T007 — Tests: in-process `httptest.Server` fixtures for each
  transport. `initialize` → `tools/list` → `tools/call` round-trip;
  ctx cancellation closes the body within 1 s; malformed SSE frame
  triggers `mcp/protocol_warning`; large streaming result (32 MiB)
  succeeds.

## Acceptance criteria

- `go test ./core/mcp/client/httpsse/... ./core/mcp/client/streamable/...`
  passes; coverage ≥ 80 % for each.
- Cancellation responsiveness < 1 s p99 verified per transport.
- No file outside the two transport packages imports
  `net/http.Client` against MCP servers.
- httpsse SSE parser rejects bad frames without aborting the session.

## Files to create / modify

- `core/mcp/client/httpsse/transport.go`
- `core/mcp/client/httpsse/sse_parser.go`
- `core/mcp/client/httpsse/transport_test.go`
- `core/mcp/client/streamable/transport.go`
- `core/mcp/client/streamable/transport_test.go`
- `core/mcp/client/internal/httpcommon/headers.go` (shared cred-ref
  resolution helpers).

## Definition of done

- All subtasks complete; tests green; lint clean.
- PR merged into `feat/wire-integration`.
