---
work_package_id: "WP14"
title: "Depguard CI rule for transport isolation and extensibility test"
dependencies:
  - "WP03"
  - "WP09"
  - "WP10"
planning_base_branch: "feat/wire-integration"
merge_target_branch: "feat/wire-integration"
branch_strategy: "Planning artifacts were generated on feat/mcp-plans; completed changes merge into feat/wire-integration."
subtasks:
  - "T001"
  - "T002"
  - "T003"
phase: "Phase 4 - Bundle integration"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP14 – Depguard CI rule for transport isolation and extensibility test

## Goal

Lock in the architectural-integrity invariant (DIRECTIVE_001 / FR-017 /
spec C-001): nothing outside `core/mcp/client/<transport>/` may import
transport-specific stdlib helpers in a way that prevents new transports.
A `depguard` rule under `.golangci.yml` enforces this, plus an
extensibility test demonstrates a third-party transport registers without
modifying any existing `core/` package.

## Spec references

- FR-017 — Pluggable transport contract.
- C-001 — Single seam.
- C-005 — OSS / enterprise distribution split.

## Plan references

- §2 Architectural Placement — invariants.
- Risk R1 — transport leak protection.

## Subtasks

- T001 — Add `depguard` rule to `.golangci.yml`:
  - Files under `core/mcp/client/` (excluding sub-package transports
    and `internal/`) MUST NOT import `os/exec`, `net/http` against
    MCP-specific paths, or any other transport-specific package.
  - Files under `core/mcp/client/<transport>/` MAY import their
    transport-specific package but MUST NOT import each other's.
- T002 — Add an extensibility test: a fake "echo" transport in
  `core/mcp/client/_extensibilitytest/echo/echo.go` (note the
  underscore-prefix to keep `go test` from auto-loading it; loaded
  only by the test harness). Test registers it and exercises an
  end-to-end pool open + tool call.
- T003 — Document the extensibility pattern in
  `core/mcp/client/doc.go`: how a new transport registers without
  any commit touching `core/mcp/client/` interface package or any
  other `core/` package.

## Acceptance criteria

- `golangci-lint run ./...` enforces the new depguard rule; an
  intentional violation in a test fixture triggers the linter.
- The extensibility test passes; the test loads its fake transport
  and exercises a tool-call end-to-end.
- Negative test: a sample diff that adds an `os/exec` import to
  `core/mcp/client/connection.go` triggers the depguard rule (verified
  in CI by a `golangci-lint` invocation against a checked-in
  bad-fixture commit).

## Files to create / modify

- `.golangci.yml` (depguard rule additions)
- `core/mcp/client/_extensibilitytest/echo/echo.go`
- `core/mcp/client/extensibility_test.go`
- `core/mcp/client/doc.go` (pattern doc)

## Definition of done

- All subtasks complete; tests green; lint clean.
- PR merged into `feat/wire-integration`.
