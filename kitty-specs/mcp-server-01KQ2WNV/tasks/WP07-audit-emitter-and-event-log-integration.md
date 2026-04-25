---
work_package_id: "WP07"
title: "Audit emitter and event-log integration for mcp.server/* kinds"
dependencies:
  - "WP01"
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
phase: "Phase 2 - Session layer"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP07 – Audit emitter and event-log integration for mcp.server/* kinds

## Goal

Implement `core/mcp/server/audit.go` — the chokepoint for every
MCP-server-related event entering `core/event.Log`. Defines payload
builders for each `mcp.server/` event kind in plan §5.3 and ensures the
server never includes resolved credentials in payloads.

## Spec references

- FR-014 — Audit emit for every handshake, request, response,
  notification, error, lifecycle transition.
- FR-015 — Replay-aware: tool-call entries include enough context to
  audit-reconstruct.
- NFR-006 — No plaintext credentials in event log entries.
- NFR-007 — Redaction recall ≥ 99 %.
- C-003 — All audit through harness event log.

## Plan references

- §4 Internal Layering — `AuditEmitter` chokepoint.
- §5.3 Data Model — full event-kind table.
- Risk R4 — credential leak via tool result.

## Subtasks

- T001 — Implement `core/mcp/server/audit.go` wrapping
  `core/event.Log`. Define the `AuditEmitter` interface that the
  session and dispatcher use.
- T002 — Implement payload builders for every kind in plan §5.3:
  `ListenerStarted`, `ListenerStopped`, `SessionOpened`,
  `SessionClosed`, `Handshake`, `HandshakeFailed`, `OriginDenied`,
  `AuthDenied`, `ToolCall`, `ToolResult`, `ToolError`, `PromptGet`,
  `ResourceRead`, `SamplingIssued`, `SamplingCompleted`,
  `NotificationSent`, `ProtocolWarning`, `Cancelled`, `Error`,
  `PolicyDenied`.
- T003 — Ensure no payload builder includes resolved credential bytes.
  HTTP `Authorization` headers MUST NOT appear in any audit payload.
  Resolved bearer tokens MUST NOT appear.
- T004 — Add an emitter id constant (`emitter_id="mcp/server"`) and
  ensure every emit includes `session_id` (when available),
  `transport`, and the event kind.
- T005 — Tests: for every kind, build a payload, marshal to JSON,
  assert no occurrence of fixture credential strings; HTTP path tests
  feed a fake `Authorization` header and assert it does NOT appear in
  recorded events.

## Acceptance criteria

- `go test ./core/mcp/server/...` (audit surface) passes; coverage
  ≥ 80 %.
- SC-003 partial: zero plaintext credentials across the full
  `mcp.server/*` event matrix.
- Audit calls do not block the session goroutine for more than 10 ms
  p95 (best-effort: emits go through a small in-memory queue if the
  event log backpressures).

## Files to create / modify

- `core/mcp/server/audit.go`
- `core/mcp/server/audit_test.go`
- `core/mcp/server/audit_kinds.go` (string constants).

## Definition of done

- All subtasks complete; tests green; lint clean.
- PR merged into `feat/wire-integration`.
