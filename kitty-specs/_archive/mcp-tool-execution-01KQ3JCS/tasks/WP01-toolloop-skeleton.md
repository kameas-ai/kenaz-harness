---
work_package_id: "WP01"
title: "core/toolloop skeleton + single-tool happy path"
dependencies: []
planning_base_branch: "main"
merge_target_branch: "main"
branch_strategy: "Planning artifacts were generated on main; completed changes must merge back into main."
subtasks:
  - "T001"
  - "T002"
  - "T003"
  - "T004"
phase: "Phase 1 — Orchestrator skeleton"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-26T00:30:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP01 — core/toolloop skeleton + single-tool happy path

## Goal

Create the `core/toolloop` package that detects an LLM `tool_use`
finish, dispatches a single tool to the configured MCP pool, threads
the result back into the conversation, and re-invokes the LLM until
a non-`tool_use` finish reason. This WP delivers US1 only — no
permissions, no hooks, no concurrency, no cancellation, no
iteration cap. Prove the mechanism end-to-end.

## Spec references

- Spec: `kitty-specs/mcp-tool-execution-01KQ3JCS/spec.md` § 6 (architectural sketch), § 7 (data shapes), US1, FR-001, FR-005.
- Plan: `kitty-specs/mcp-tool-execution-01KQ3JCS/plan.md` § "Step 1".

## Subtasks

- **T001 — Stub MCP pool fixture.** Create
  `core/mcp/fixture/pool.go` with an in-memory `Pool` that returns
  pre-registered (server, tool, args) → result mappings. Tests use
  this until C1 lands.
- **T002 — Loop type + Run method.** Add `core/toolloop/loop.go`
  with `Loop` struct (fields: `reg llm.Registry`, `pool mcp.Pool`,
  `history SessionHistoryRW`, `maxIter int` default 8), constructor,
  and `Run(ctx, sessionID, parentSubID, response *llm.Response,
  request llm.GenerationRequest) error`. Behavior: if
  `response.FinishReason != "tool_use"` → return nil immediately.
  Otherwise: for each `ToolUse` in `response.ToolCalls`, call
  `pool.Invoke(ctx, server, tool, args)`, append a `Message{Role:
  "tool", ToolCallID, Content}` to the history, then invoke
  `reg.Stream(ctx, augmentedRequest)`. Recurse via the same
  detection until non-`tool_use` finish.
- **T003 — Pump integration.** Modify `core/rpc/views/llm/impl.go
  pump`: after `sub.stream.Final()` returns, if the response has
  `FinishReason == "tool_use"` AND `a.toolLoop != nil`, invoke
  `a.toolLoop.Run(...)`. Plumb `*toolloop.Loop` through `Config`
  and the impl struct.
- **T004 — Wiring + tests.** In `core/rpc/api.go newLLMStack`,
  construct the loop with the existing registry + a placeholder
  fixture pool. Add `core/toolloop/loop_test.go` covering: (a)
  non-tool-use finish exits cleanly, (b) one-tool round-trip
  happy path (fake registry emits tool_use → loop dispatches
  fixture pool → re-invokes registry → second response is
  end_turn → loop exits, history has user + assistant + tool +
  assistant turns).

## Out of scope (later WPs)

Permissions, confirm-each, hooks, audit emission, concurrency,
cancellation, iteration cap, streaming feedback to UI, modal flow.

## Acceptance

- `go build ./...` succeeds.
- `go test -race -count=1 -short ./core/toolloop/... ./core/mcp/fixture/...` passes.
- `go test -race -count=1 -short ./core/...` shows zero regressions
  (still 703+ tests passing).

## Branch strategy

Worktree-isolated. Branch `wp01-toolloop-skeleton` off `main`.
Merge back to `main` when WP01 acceptance gate passes. Do NOT
include `Settings.ToolLoop.Enabled` flag wiring yet — that lands
in WP05.
