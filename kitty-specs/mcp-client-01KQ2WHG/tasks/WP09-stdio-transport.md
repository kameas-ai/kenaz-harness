---
work_package_id: "WP09"
title: "stdio transport — child process spawn, framing, stderr forwarding"
dependencies:
  - "WP02"
  - "WP03"
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
phase: "Phase 3 - Transports"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP09 – stdio transport — child process spawn, framing, stderr forwarding

## Goal

Implement `core/mcp/client/stdio` — the stdio transport. Spawns the
declared child process, frames newline-delimited JSON on stdout/stdin,
forwards stderr to the audit log as `mcp/server_log` events, and reaps
the child cleanly on close.

## Spec references

- FR-001 — stdio transport.
- FR-013 — Retry policy applies to respawn.
- C-006 — Local-first; stdio works with zero network access.

## Plan references

- §2 Architectural Placement — `core/mcp/client/stdio/` is the only
  package importing `os/exec`.
- §4 Internal Layering — Transport contract.
- Research §3.1 — framing and edge cases.
- Risk R3 — buffer sizing.
- Risk R11 — child reaping.

## Subtasks

- T001 — Implement `stdio.NewTransport(spec ServerSpec, deps
  TransportDeps) (Transport, error)`. Spawns `exec.Cmd` with stdin /
  stdout / stderr pipes; resolves env credential refs via
  `deps.Secrets`; injects them into `cmd.Env`.
- T002 — Implement `Send(ctx, frame)` writing one newline-terminated
  JSON object to stdin; `Recv(ctx)` reading one such object via
  `bufio.Scanner` with 16 MiB buffer ceiling.
- T003 — Implement stderr goroutine that line-by-line forwards to
  `deps.Audit.Append` as `mcp/server_log` events with level
  inferred from prefix (`[INFO]`, `[ERROR]`, etc.).
- T004 — Implement `Close()` — send SIGTERM, wait up to 5 s
  (configurable via `Limits.GracePeriodMS`), SIGKILL if still alive,
  `cmd.Wait()` to reap.
- T005 — Register the transport via
  `transport.Register("stdio", stdio.NewTransport)` in an `init()`.
- T006 — Tests: spawn a tiny test binary in `testdata/echo-mcp/` (a
  Go program that echoes received JSON-RPC requests as responses
  with a fake content payload); end-to-end `initialize` →
  `tools/list` → `tools/call`; large-result test (4 MiB result body);
  crash test (binary exits after one call); stderr-forwarding test.

## Acceptance criteria

- `go test ./core/mcp/client/stdio/...` passes; coverage ≥ 80 %.
- Race-free.
- The 4 MiB result test passes (validates buffer sizing per R3).
- Crash test asserts `Close()` reaps the child; no zombie processes
  remain (verified by `ps` on the test host or a unix.WaitStatus check).
- The transport registers itself via `init()`; lookup of "stdio" in
  the registry returns the factory.
- No imports outside `os/exec`, `bufio`, `core/mcp/client/transport`,
  `core/mcp/client/jsonrpc`, and stdlib.

## Files to create / modify

- `core/mcp/client/stdio/stdio.go`
- `core/mcp/client/stdio/stdio_test.go`
- `core/mcp/client/stdio/testdata/echo-mcp/main.go`
- `core/mcp/client/stdio/doc.go`

## Definition of done

- All subtasks complete; tests green; lint clean.
- PR merged into `feat/wire-integration`.
