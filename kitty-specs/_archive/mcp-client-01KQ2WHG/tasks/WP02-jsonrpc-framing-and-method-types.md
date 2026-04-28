---
work_package_id: "WP02"
title: "JSON-RPC 2.0 framing layer and canonical MCP method types"
dependencies:
  - "WP01"
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
phase: "Phase 1 - Core skeleton"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP02 – JSON-RPC 2.0 framing layer and canonical MCP method types

## Goal

Implement the pure JSON-RPC 2.0 framing layer plus the typed parameter
and result struct types for every MCP method the v1 surface needs. This
package (`core/mcp/client/jsonrpc/`) is the only place in the harness
that knows JSON-RPC; the connection layer above it speaks Go types.

## Spec references

- FR-004 — `initialize` handshake (drives `initializeParams`,
  `initializeResult`, capability negotiation types).
- FR-005 — Tool round-trip (drives `listToolsResult`, `callToolParams`,
  `callToolResult`).
- FR-006 — Prompt round-trip (drives `listPromptsResult`,
  `getPromptParams`, `getPromptResult`).
- FR-007 — Resource round-trip (drives `listResourcesResult`,
  `readResourceParams`, `readResourceResult`).
- FR-008 — Server logging notifications (drives `loggingMessageParams`).
- FR-009 — Sampling: server → client requests (drives
  `samplingCreateMessageParams`, `samplingCreateMessageResult`).
- FR-010 — Roots: server → client requests (drives `listRootsResult`).
- FR-011 — Cancellation notifications (drives `cancelNotificationParams`).
- FR-012 — Error envelope types (drives `rpcError`, MCP error code
  constants).

## Plan references

- §2 Architectural Placement — `core/mcp/client/jsonrpc/` is internal
  and never imported outside `core/mcp/client/**`.
- §4 Internal Layering — connection state machine consumes these types.
- Research §1.1 — canonical method names.
- Research §1.2 — transport-agnostic framing.

## Subtasks

- T001 — Implement `request`, `response`, `notification`, `rpcError`
  framing types with JSON-RPC 2.0 invariants enforced (`jsonrpc: "2.0"`,
  id presence rules).
- T002 — Implement an `Encode(frame any) ([]byte, error)` helper that
  produces newline-terminated JSON for stdio transport AND a
  `Decode(bytes []byte) (frame, error)` helper that auto-discriminates
  request/response/notification.
- T003 — Define MCP error code constants (`CodeParseError = -32700`,
  `CodeInvalidRequest = -32600`, `CodeMethodNotFound = -32601`,
  `CodeInvalidParams = -32602`, `CodeInternalError = -32603`, plus
  MCP-specific codes for sampling / roots).
- T004 — Define typed parameter / result structs for: `initialize`,
  `initialized`, `ping`, `tools/list`, `tools/call`, `prompts/list`,
  `prompts/get`, `resources/list`, `resources/read`,
  `resources/templates/list`, `logging/setLevel`,
  `notifications/message`, `sampling/createMessage`, `roots/list`,
  `notifications/cancelled`, `notifications/progress`,
  `notifications/tools/list_changed`,
  `notifications/prompts/list_changed`,
  `notifications/resources/list_changed`,
  `notifications/resources/updated`,
  `notifications/roots/list_changed`.
- T005 — Define `clientCapabilities`, `serverCapabilities`,
  `implementationInfo`, `protocolVersion` (string), and version
  negotiation helper `Negotiate(client, server string) (string, error)`.
- T006 — Tests: round-trip every defined struct through
  `json.Marshal` → `json.Unmarshal` and assert byte-identical. Tests
  for negotiation: success when overlapping, `ErrHandshakeFailed` when
  no overlap.

## Acceptance criteria

- `go build ./core/mcp/client/jsonrpc/...` succeeds with no imports
  outside the stdlib.
- `go vet` clean; `golangci-lint` clean.
- Round-trip tests pass for every defined struct.
- Negotiation test covers: equal versions, client newer, server newer,
  no overlap.
- `go list -deps ./core/mcp/client/jsonrpc | grep -v ^github.com/sigil-tech`
  returns nothing outside the stdlib.

## Files to create / modify

- `core/mcp/client/jsonrpc/frame.go`
- `core/mcp/client/jsonrpc/codes.go`
- `core/mcp/client/jsonrpc/methods.go`
- `core/mcp/client/jsonrpc/version.go`
- `core/mcp/client/jsonrpc/jsonrpc_test.go`
- `core/mcp/client/jsonrpc/doc.go`

## Definition of done

- All subtasks complete; tests green; lint clean; coverage ≥ 80 %.
- No imports outside stdlib in this package.
- PR opened, merges into `feat/wire-integration`.
