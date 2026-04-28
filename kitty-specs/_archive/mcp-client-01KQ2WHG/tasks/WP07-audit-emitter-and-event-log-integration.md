---
work_package_id: "WP07"
title: "Audit emitter and event-log integration for mcp/* kinds"
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
phase: "Phase 2 - Connection layer"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP07 – Audit emitter and event-log integration for mcp/* kinds

## Goal

Implement `core/mcp/client/audit.go` — the single chokepoint through which
every MCP-related event reaches `core/event.Log`. Defines payload builders
for each `mcp/` event kind in plan §5.3 and ensures the connector never
puts resolved credentials into a payload (defense-in-depth alignment with
the event-log redaction pipeline).

## Spec references

- FR-014 — Audit emit for every handshake, request, response,
  notification, error, lifecycle transition.
- FR-015 — Replay-aware: each tool-call entry includes routing context.
- NFR-007 — Zero plaintext credential bytes in event log.
- NFR-008 — Append-only invariant.
- C-003 — All audit goes through harness event log.

## Plan references

- §4 Internal Layering — `AuditEmitter` is the single chokepoint.
- §5.3 Data Model — full event-kind table.
- Risk R2 — payload reconstruction from typed shapes, not wire bodies.
- Risk R9 — redaction recall for novel credential shapes.

## Subtasks

- T001 — Define `AuditEmitter` interface in
  `core/mcp/client/transport/transport.go` (already declared) and
  implement the concrete adapter in `core/mcp/client/audit.go` that
  wraps `core/event.Log`.
- T002 — Implement payload builder functions for every kind in plan
  §5.3 (`PoolOpen`, `PoolClose`, `PoolReload`, `ServerInitialize`,
  `ServerInitialized`, `ServerInitializeFailed`, `ServerUnhealthy`,
  `ToolCallRequest`, `ToolCallResponse`, `PromptGet`, `ResourceRead`,
  `SamplingRequest`, `SamplingResponse`, `ServerLog`,
  `ProtocolWarning`, `TransportFailure`, `RetryAttempted`, `Cancelled`,
  `Error`, `ServerExit`, `PreflightResolved`, `PreflightFailed`).
  Each builder takes typed args and produces `event.AppendInput`.
- T003 — Ensure no payload builder includes resolved credential bytes;
  cred refs appear only as `{Kind, Locator}` tuples.
- T004 — Add an emitter id constant (`emitter_id="mcp/client"`) and
  ensure every emit includes `session_id` (when available),
  `server_id`, and the event kind.
- T005 — Tests: for every kind, build a payload, marshal to JSON,
  assert no occurrence of fixture credential strings in the output;
  tests around tool-call request capture a fake credential pattern
  in the args and verify it's NOT redacted by the audit layer (that's
  the event-log pipeline's job — the audit layer is defense-in-depth
  and assumes the payload is already credential-clean).

## Acceptance criteria

- `go test ./core/mcp/client/...` (audit surface) passes; coverage
  ≥ 80 %.
- Test SC-003 partial: zero plaintext credential bytes across the
  full mcp/* event matrix.
- Lint clean.
- Audit calls do NOT block the connection state machine for more than
  10 ms p95 (best-effort: emits go through a small in-memory queue if
  the event log backpressures).

## Files to create / modify

- `core/mcp/client/audit.go`
- `core/mcp/client/audit_test.go`
- `core/mcp/client/audit_kinds.go` (string constants for kind names).

## Definition of done

- All subtasks complete; tests green; lint clean.
- PR merged into `feat/wire-integration`.
