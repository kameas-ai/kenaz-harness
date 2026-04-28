# Spec: MCP server health + lifecycle UI

**Status**: draft · **Owner**: alecfeeman

## 1. Why

Stdio MCP servers can crash, hang, or fail to ping. Today these failures surface as a generic `tool not available` error mid-conversation or as silence. The user has no way to see "filesystem-project failed at 14:05 with EOF" without grepping logs.

## 2. Goals

- Per-server health row in the Tools panel: state (running / stopped / failed), uptime, last error, last successful tool call.
- Restart button per server.
- Tail-stderr drawer for diagnosis.
- Toast on first failure of a previously-healthy server.

## 3. Functional requirements

| ID | Requirement | Status |
|---|---|---|
| FR-001 | `core/mcp/stdio` already tracks server state; expose via a new `MCP.ListServerHealth` RPC. | proposed |
| FR-002 | New `RecipeStatus` field: `last_error_message`, `last_error_at`, `successful_tool_calls`, `restart_count`. | proposed |
| FR-003 | Tools panel renders an "MCP servers" subsection with health rows. | proposed |
| FR-004 | Restart button calls `MCP.RestartServer(id)` which kills and respawns; success → toast. | proposed |
| FR-005 | Tail-stderr drawer subscribes to a broker topic streaming the server's stderr lines. | proposed |
| FR-006 | First-failure toast fires once per up→down transition, suppressed for repeats while down. | proposed |

## 4. Success criteria

- A user whose filesystem server EOF'd sees the failure within 1 s and can read the underlying stderr without leaving the UI.
- Restart button recovers a known-broken server in ≤ 5 s p95.
