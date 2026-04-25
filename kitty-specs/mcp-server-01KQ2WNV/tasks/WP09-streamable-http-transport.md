---
work_package_id: "WP09"
title: "streamable-HTTP transport — net/http listener with origin and bearer middleware"
dependencies:
  - "WP01"
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
phase: "Phase 4 - Transports"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP09 – streamable-HTTP transport — net/http listener with origin and bearer middleware

## Goal

Implement `core/mcp/server/streamable` — the streamable-HTTP transport.
Uses Go stdlib `net/http.Server` to listen on a configurable bind
address. Dispatches `POST /` to JSON-RPC; responds with either
`application/json` (single response) or `text/event-stream` (streaming
response) depending on whether the handler streams.

## Spec references

- FR-001 — streamable-HTTP transport.
- FR-002 — Listener controlled by `mcp.server.http.enabled`.
- FR-016 — Origin / allowlist policy.
- FR-017 — Optional bearer-token authentication.
- FR-020 — Graceful drain.
- NFR-002 — Handshake latency < 100 ms p95 loopback.
- NFR-010 — Refuses non-loopback bind without explicit opt-in.

## Plan references

- §2 Architectural Placement — `core/mcp/server/streamable/` is the
  only package importing `net/http.Server`.
- Research §3.2 — server-side notes.
- Risk R2 — loopback default protection.
- Risk R3 — constant-time bearer compare.
- Risk R11 — handshake rate-limit.

## Subtasks

- T001 — Implement `streamable.NewTransport(opts TransportOpts)
  (Transport, error)`. Constructs `http.Server` with handler at `/`.
  Validates bind interface against config invariant.
- T002 — Implement origin middleware: checks `Origin` header against
  `cfg.HTTP.AllowedOrigins`; rejects with HTTP 403 + `mcp.server/origin_denied`
  on mismatch.
- T003 — Implement bearer-token middleware: when configured, checks
  `Authorization: Bearer <token>` against the resolved expected token
  using `subtle.ConstantTimeCompare`. Rejects with HTTP 401 +
  `mcp.server/auth_denied` on mismatch.
- T004 — Implement session-id middleware: generates ULID on first
  `initialize`; carries via `Mcp-Session-Id` header; tracks per-session
  state.
- T005 — Implement response framing: handler returns either a single
  JSON response (write `Content-Type: application/json`) or a streaming
  response (write `Content-Type: text/event-stream` and chunked body).
- T006 — Implement `Listen(ctx, accept)` — calls `http.Server.ListenAndServe`;
  on each new session, calls `accept(SessionTransport)`. Implement
  `Shutdown(ctx)` — calls `http.Server.Shutdown` with the configured
  drain timeout.
- T007 — Register via
  `transport.Register("streamable_http", streamable.NewTransport)`
  in `init()`. Tests: full lifecycle against `httptest.Server`; origin
  rejection; bearer rejection; session id propagation; streaming
  response framing; long-running tool with progress notifications;
  concurrent-sessions cap.

## Acceptance criteria

- `go test ./core/mcp/server/streamable/...` passes; coverage ≥ 80 %.
- Race-free under `-race`.
- Origin allowlist rejection emits `mcp.server/origin_denied` with the
  source IP.
- Bearer-token mismatch emits `mcp.server/auth_denied` with the source
  IP and the configured cred-ref's `Kind+Locator` (NEVER the resolved
  token).
- Constant-time bearer compare verified via
  `subtle.ConstantTimeCompare` usage.
- Concurrent-sessions cap: a 17th session attempt is rejected with HTTP
  503 when `max_concurrent_sessions = 16`.

## Files to create / modify

- `core/mcp/server/streamable/transport.go`
- `core/mcp/server/streamable/middleware.go`
- `core/mcp/server/streamable/transport_test.go`

## Definition of done

- All subtasks complete; tests green; lint clean.
- PR merged into `feat/wire-integration`.
