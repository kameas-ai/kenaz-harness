---
work_package_id: "WP08"
title: "stdio transport — stdin/stdout framing and stderr safety gate"
dependencies:
  - "WP01"
  - "WP02"
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
phase: "Phase 4 - Transports"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP08 – stdio transport — stdin/stdout framing and stderr safety gate

## Goal

Implement `core/mcp/server/stdio` — the inbound stdio transport. Reads
JSON-RPC frames from `os.Stdin`; writes responses to `os.Stdout`. Refuses
to start if stdout is unsuitable for protocol use (TTY with colorization,
buffered terminal). Routes any internal logging to stderr or the event
log.

## Spec references

- FR-001 — stdio transport.
- NFR-009 — stdio works with zero network access.
- NFR-012 — JSON-RPC framing strictness on stdout.

## Plan references

- §2 Architectural Placement — `core/mcp/server/stdio/` is the only
  package owning stdio I/O for the server.
- Research §3.1 — stdio framing + safety gates.
- Risk R1 — stdout corruption protection.

## Subtasks

- T001 — Implement
  `stdio.NewTransport(opts TransportOpts) (Transport, error)`.
  Validates `os.Stdout` is suitable: not a TTY, or operator has
  explicitly opted-in via `--allow-tty-stdout`. Validates that no
  third-party logger is writing to stdout (best-effort: via a
  startup self-test that writes a sentinel and reads back the file
  descriptor's metadata).
- T002 — Implement `Listen(ctx, accept)` — wires `os.Stdin`/`os.Stdout`
  into a single `SessionTransport` and calls `accept` once. Stdio
  is single-session by definition.
- T003 — Implement `SessionTransport.Send` and `Recv` with bufio
  framing (16 MiB buffer ceiling per Risk R3 from the client mission).
- T004 — Register the transport via
  `transport.Register("stdio", stdio.NewTransport)` in an `init()`.
- T005 — Tests: end-to-end stdio session against a fake `os.Stdin`/
  `os.Stdout` (in-memory pipes); large request (4 MiB) handling;
  refuse-to-start test when stdout is a fake TTY.

## Acceptance criteria

- `go test ./core/mcp/server/stdio/...` passes; coverage ≥ 80 %.
- Race-free.
- Refuse-to-start test passes (NFR-012; SC-006).
- Architectural-integrity invariant: `core/mcp/server/stdio/` does
  NOT import `os/exec` (that's the client side).

## Files to create / modify

- `core/mcp/server/stdio/stdio.go`
- `core/mcp/server/stdio/stdio_test.go`
- `core/mcp/server/stdio/stdout_safety.go`

## Definition of done

- All subtasks complete; tests green; lint clean.
- PR merged into `feat/wire-integration`.
