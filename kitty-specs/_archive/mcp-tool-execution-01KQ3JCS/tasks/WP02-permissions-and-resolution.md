---
work_package_id: "WP02"
title: "Tool resolution + permission gate"
dependencies:
  - "WP01"
planning_base_branch: "main"
merge_target_branch: "main"
branch_strategy: "Planning artifacts were generated on main; completed changes must merge back into main."
subtasks:
  - "T001"
  - "T002"
  - "T003"
  - "T004"
phase: "Phase 2 — Resolution + per-tool deny gates"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-26T00:30:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP02 — Tool resolution + permission gate

## Goal

Add the merged-catalog tool resolver and a per-tool policy
enforcement step that lets sessions explicitly deny tools without
involving the modal UI flow. Implements US3 (denied tools yield
a synthetic `tool_blocked` result; conversation continues).

## Spec references

- Spec: § 7 (Resolution / ToolPolicy types), US3, FR-002, FR-007 (deny path only).
- Plan: § "Step 2".

## Prerequisites

WP01 merged. Mission C2 (per-session MCP overrides) MAY land in
parallel — if its `sessions.MCPOverrides` field is not yet
present, define a temporary local stand-in shape in WP02 and
swap to the real one when C2 lands.

## Subtasks

- **T001 — PermissionResolver interface.** Add
  `core/toolloop/perms.go` with:
  ```go
  type ToolPolicy string // "auto_allow" | "confirm_each" | "deny"
  type PermissionResolver interface {
      Resolve(ctx, sessionID, server, tool string) (Resolution, error)
  }
  type Resolution struct {
      Server string
      Tool   string
      Policy ToolPolicy
  }
  ```
- **T002 — Static + session-override resolver.**
  `staticResolver` reads global server allow/deny rules from
  `<DataDir>/mcp_servers.json` (placeholder: empty global config
  loads as default `auto_allow`). `sessionOverrideResolver`
  layers `Session.MCPOverrides` on top. `MergedResolver`
  composes both, session-overrides win.
- **T003 — Loop integration.** In `core/toolloop/loop.go`,
  between tool-detection and dispatch: call
  `perms.Resolve(...)`. If `policy == "deny"`, skip
  `pool.Invoke` and instead append a synthetic
  `Message{Role: "tool", Content: "Tool blocked: <reason>"}` to
  the history. The conversation proceeds normally.
- **T004 — Tests.** `core/toolloop/perms_test.go` (resolver
  tables) and an extension to `loop_test.go` proving US3 round
  trip (model emits tool_use → loop sees deny → synthetic
  result threaded → model adapts).

## Out of scope

Hooks, confirm_each modal flow, audit emission, concurrency.

## Acceptance

- `go test -race -count=1 -short ./core/toolloop/...` passes US3.
- All sibling tests continue to pass.

## Branch strategy

Branch `wp02-permissions-and-resolution` off `main`, merge back
when WP02 gate passes.
