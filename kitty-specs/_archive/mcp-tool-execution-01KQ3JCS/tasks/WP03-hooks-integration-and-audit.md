---
work_package_id: "WP03"
title: "Hook lifecycle integration + audit emission"
dependencies:
  - "WP01"
  - "WP02"
planning_base_branch: "main"
merge_target_branch: "main"
branch_strategy: "Planning artifacts were generated on main; completed changes must merge back into main."
subtasks:
  - "T001"
  - "T002"
  - "T003"
  - "T004"
  - "T005"
phase: "Phase 3 — Hook runner + audit"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-26T00:30:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP03 — Hook lifecycle integration + audit emission

## Goal

Wire pre/post tool-use hook events through Mission B's hook
runner, and route every tool call through the existing audit
emitter. Implements US7 (hook-driven arg mutation) and US6
(audit trail with redacted args).

## Spec references

- Spec: § 4 (US6, US7), FR-003, FR-009, NFR-003.
- Plan: § "Step 3".

## Prerequisites

WP01 + WP02 merged. Mission B's hook runner with
`RunPreToolUse(event) (Result, error)` /
`RunPostToolUse(event)` exposed in `core/hooks`. If Mission B
has not landed, define minimal stub interfaces locally and
swap when it merges.

## Subtasks

- **T001 — Hook event types.** Define `core/toolloop` types
  `PreToolUseEvent` `{ SessionID, Tool, Server, Args, AttemptNo }`
  and `PostToolUseEvent` `{ SessionID, Tool, Server, Args, Result,
  Error, LatencyMS }`. Mirror Claude Code's hook event JSON shape
  exactly (stdin/stdout protocol).
- **T002 — Pre-hook integration.** Before each tool dispatch in
  the loop, call `hooks.RunPreToolUse(event)`. Honor a
  `continue: false` return by emitting a synthetic
  `tool_blocked` result with the hook-provided reason. Honor a
  `args` mutation by substituting the hook's args into the
  outgoing dispatch.
- **T003 — Post-hook integration.** After each dispatch (success
  or error), call `hooks.RunPostToolUse(event)`. Side-effect
  only; ignore any returned mutation.
- **T004 — Audit emission.** New file
  `core/toolloop/audit.go` emits `tool_invoked` (success) and
  `tool_failed` (error) via the existing `audit.Emitter`.
  Required fields: `tool`, `server`, `session_id`,
  `parent_sub_id`, `latency_ms`, `outcome`. Args MUST be
  redacted via the existing event-log redactor before
  emission (NFR-003).
- **T005 — Tests.** `core/toolloop/hooks_test.go` proving:
  (a) pre-hook receives correct event payload, (b) `continue:
  false` short-circuits with `tool_blocked`, (c) `args` mutation
  is observable in the dispatched MCP request via the fixture
  pool's args-capture, (d) audit log shows redacted args for
  every call.

## Acceptance

- US6 + US7 acceptance criteria pass via tests.
- No credential / secret in any audit-log payload (verified by
  the existing privacy CI pattern).
- `go test -race -count=1 -short ./core/...` zero regressions.

## Branch strategy

Branch `wp03-hooks-and-audit` off `main`, merge back when WP03
gate passes.
