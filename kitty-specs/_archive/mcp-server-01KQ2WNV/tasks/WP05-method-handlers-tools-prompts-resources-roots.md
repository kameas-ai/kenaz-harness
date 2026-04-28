---
work_package_id: "WP05"
title: "Method handlers — tools/*, prompts/*, resources/*, roots, logging, ping"
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

# Work Package Prompt: WP05 – Method handlers — tools/*, prompts/*, resources/*, roots, logging, ping

## Goal

Implement the per-method handlers that materialize `tools/list`,
`tools/call`, `prompts/list`, `prompts/get`, `resources/list`,
`resources/read`, `resources/templates/list`, `logging/setLevel`,
`ping`, and the server-side roots advertisement.

## Spec references

- FR-004 — `tools/list` and `tools/call`.
- FR-005 — `prompts/list` and `prompts/get`.
- FR-006 — `resources/list`, `resources/read`,
  `resources/templates/list`.
- FR-007 — `roots/list` (server-side: advertise operator-declared
  roots).
- FR-008 — `logging/setLevel`.
- FR-018 — Tool argument schema validation.

## Plan references

- §4 Internal Layering — handler pipeline.
- §6.3 — bundle resolver integration for prompts/resources.
- Risk R7 — tool arg-schema bounds.
- Risk R8 — `query_event_log` audit-boundary.

## Subtasks

- T001 — Implement `handlers.go`:
  - `handleToolsList` — enumerates `Catalog.Tools()` filtered by
    config-enabled flags.
  - `handleToolsCall` — looks up tool by name, validates args
    against `tool.InputSchema()`, runs `PolicyGuard.AllowToolCall`,
    invokes `tool.Call` with per-handler ctx.
- T002 — Implement `prompts.go`:
  - `handlePromptsList` — queries `bundle.Resolver` for
    non-private prompts.
  - `handlePromptsGet` — renders the named prompt against arguments;
    returns messages.
- T003 — Implement `resources.go`:
  - `handleResourcesList` — queries `bundle.Resolver` for resources.
  - `handleResourcesRead` — reads bytes + MIME; enforces config size
    cap (default 8 MiB; `ErrResourceTooLarge` over the cap).
  - `handleResourcesTemplatesList` — enumerates resource templates.
- T004 — Implement `roots.go`:
  - Server-side advertisement of operator-declared roots in
    `initialize` capability.
- T005 — Implement `logging/setLevel` and `ping` handlers (trivial).
- T006 — Tests: every handler exercised via the dispatcher; positive
  and negative cases (unknown tool, schema-fail args, oversized
  resource).

## Acceptance criteria

- `go test ./core/mcp/server/...` passes; coverage ≥ 80 % over
  handler surface.
- Tool argument schema validation rejects malformed inputs without
  invoking the tool.
- Bundle reload mid-call: in-flight `prompts/get` finishes against
  the snapshotted bundle; subsequent `prompts/list` reflects new
  state (tested with a fake resolver that switches state).
- Resource size cap returns `ErrResourceTooLarge` for oversized
  responses; emits typed event.
- Roots: only operator-declared paths returned; never `dataDir`.

## Files to create / modify

- `core/mcp/server/handlers.go`
- `core/mcp/server/handlers_test.go`
- `core/mcp/server/prompts.go`
- `core/mcp/server/resources.go`
- `core/mcp/server/roots.go`

## Definition of done

- All subtasks complete; tests green; lint clean.
- PR merged into `feat/wire-integration`.
