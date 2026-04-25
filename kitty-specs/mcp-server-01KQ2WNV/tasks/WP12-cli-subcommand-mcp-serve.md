---
work_package_id: "WP12"
title: "CLI subcommand: kaneaz-harness mcp serve"
dependencies:
  - "WP11"
planning_base_branch: "feat/wire-integration"
merge_target_branch: "feat/wire-integration"
branch_strategy: "Planning artifacts were generated on feat/mcp-plans; completed changes merge into feat/wire-integration."
subtasks:
  - "T001"
  - "T002"
  - "T003"
  - "T004"
phase: "Phase 5 - Integration"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP12 – CLI subcommand: kaneaz-harness mcp serve

## Goal

Add a CLI subcommand `kaneaz-harness mcp serve` (with `--stdio`,
`--http`, `--bind`, `--log-file` flags) so external MCP clients can
launch the harness directly. Routes log output away from stdout in
stdio mode.

## Spec references

- FR-002 — Stdio enable controlled by CLI flag.
- NFR-009 — Stdio works with zero network access.
- NFR-012 — JSON-RPC framing strictness on stdout.

## Plan references

- §6.8 — CLI subcommand integration.
- Risk R1 — stdout corruption protection.

## Subtasks

- T001 — Add CLI plumbing in `main.go` (or a new subcommand handler
  alongside the Wails app entry point). Recognize `mcp serve` and
  delegate to a `cmd/mcp/serve.go` (or similar) handler.
- T002 — In stdio mode:
  - Re-route Go's default logger to stderr.
  - Re-route any third-party logger discovered via init that writes
    to stdout (best-effort: panic on detection, since the protocol
    cannot tolerate it).
  - Construct `core.Core` with `Subsystems.MCPServer` set; configure
    `mcp.server.stdio.enabled = true`; call `Core.Start`; block on
    `Core.Shutdown` signal.
- T003 — In `--http` mode:
  - Same as stdio mode but configure `mcp.server.http.enabled = true`
    with `--bind` flag; stdio remains disabled.
- T004 — Tests: a black-box subprocess test launches
  `go run ./... mcp serve --stdio` and exchanges JSON-RPC frames
  via stdin/stdout; asserts handshake + tool call. (Subprocess test
  may live in `core/mcp/server/integration_test.go` and use
  `os/exec` to spawn the harness binary built by the test.)

## Acceptance criteria

- `go test ./...` passes including the subprocess test.
- Subprocess test demonstrates an end-to-end stdio session against
  the actual harness binary.
- `--http` flag binds and accepts a streamable-HTTP session against
  `httptest`-style fixture client.
- Help text (`kaneaz-harness mcp serve --help`) documents flags.

## Files to create / modify

- `main.go` (CLI subcommand dispatch)
- `cmd/mcp/serve.go` (subcommand handler)
- `cmd/mcp/serve_test.go`

## Definition of done

- All subtasks complete; tests green; lint clean.
- PR merged into `feat/wire-integration`.
