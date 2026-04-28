---
work_package_id: "WP13"
title: "Depguard CI rule for transport isolation and tool / transport extensibility test"
dependencies:
  - "WP01"
  - "WP06"
  - "WP08"
  - "WP09"
planning_base_branch: "feat/wire-integration"
merge_target_branch: "feat/wire-integration"
branch_strategy: "Planning artifacts were generated on feat/mcp-plans; completed changes merge into feat/wire-integration."
subtasks:
  - "T001"
  - "T002"
  - "T003"
  - "T004"
phase: "Phase 6 - Acceptance"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP13 – Depguard CI rule for transport isolation and tool / transport extensibility test

## Goal

Lock in the architectural-integrity invariant (DIRECTIVE_001 / FR-012 /
spec C-001): nothing outside `core/mcp/server/<transport>/` may import
transport-specific stdlib helpers in a way that prevents new transports.
Same pattern as the MCP-client mission's WP14. Plus an extensibility
test for both transports and tools.

## Spec references

- FR-012 — Pluggable transport contract.
- FR-013 — Pluggable tool contract.
- C-001 — Single seam.
- C-005 — OSS / enterprise distribution split.

## Plan references

- §2 Architectural Placement — invariants.
- §7 Phasing — extensibility is the seam between OSS and enterprise.

## Subtasks

- T001 — Add `depguard` rules to `.golangci.yml`:
  - Files under `core/mcp/server/` (excluding sub-package transports
    and `tools/`) MUST NOT import `net/http`, `os`-based stdio
    helpers in a transport-specific way, etc.
  - Files under `core/mcp/server/<transport>/` MAY import their
    transport-specific package but MUST NOT import each other's.
- T002 — Transport extensibility test: a fake "in-memory" transport in
  `core/mcp/server/_extensibilitytest/inmem/inmem.go` registers
  itself; an integration test exercises a tool call through it.
- T003 — Tool extensibility test: a fake "echo" tool registered via
  `Server.RegisterTool` exercises a tool call.
- T004 — Document the extensibility patterns in
  `core/mcp/server/doc.go`: how a new transport / tool registers
  without modifying any existing `core/` package.

## Acceptance criteria

- `golangci-lint run ./...` enforces the new depguard rules.
- The two extensibility tests pass.
- Negative test: a sample diff that adds a `net/http` import to
  `core/mcp/server/server.go` triggers the depguard rule.

## Files to create / modify

- `.golangci.yml` (depguard rule additions)
- `core/mcp/server/_extensibilitytest/inmem/inmem.go`
- `core/mcp/server/_extensibilitytest/echotool/echo.go`
- `core/mcp/server/extensibility_test.go`
- `core/mcp/server/doc.go` (pattern doc)

## Definition of done

- All subtasks complete; tests green; lint clean.
- PR merged into `feat/wire-integration`.
