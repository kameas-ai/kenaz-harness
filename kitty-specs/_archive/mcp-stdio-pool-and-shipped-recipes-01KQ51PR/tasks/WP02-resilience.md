---
work_package_id: "WP02"
title: "Resilience — auto-restart, health pings, EOF, stderr ring buffer"
dependencies:
  - "WP01"
planning_base_branch: "main"
merge_target_branch: "main"
branch_strategy: "Branch wp02-stdio-resilience off main; merge back when WP02 acceptance gate passes."
subtasks:
  - "T008"
  - "T009"
  - "T010"
  - "T011"
  - "T012"
phase: "Phase 3 — Resilience"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-26T16:35:00Z"
    agent: "system"
    action: "WP prompt authored manually after Plan agent could not write files."
---

# Work Package Prompt: WP02 — Resilience

## Goal

Bolt restart, health pinging, and crash detection onto the WP01 server lifecycle. After this WP, a crashed MCP server auto-restarts with exponential backoff (1s/2s/4s, capped at 3 attempts in any 5-minute window), unanswered pings trip a restart, EOF on stdin/stdout is detected, and `RecipeStatus` snapshots surface the live state including the stderr ring-buffer tail for debugging.

## Spec / plan references

- Spec: §FR-007, FR-008, FR-010, NFR-002.
- Plan: Phase 3.
- Data-model: `RecipeStatus`, `ServerInstance.restartHistory`, state machine.

## Prerequisites

WP01 merged.

## Subtasks

- **T008 — `core/mcp/stdio/supervisor.go`** — `(*ServerInstance).runSupervisor(ctx)` long-lived goroutine. Listens on `crashCh` (closed by reader/writer when they hit EOF or fatal write). On crash:
  - Prune `restartHistory` to entries within the last 5 minutes.
  - If `len(restartHistory) < 3`: sleep `backoff[len]` (1s, 2s, 4s), respawn the cmd + reopen pipes + re-run `initialize` (reuse WP01's spawn helper). Append now to `restartHistory`. Set state = `restarting` during sleep, `running` once initialize succeeds.
  - Else: set state = `failed`. Emit a structured log event. Stay failed until user-driven toggle off/on (which clears `restartHistory`).
  - First-init failure does NOT trigger restart (handled in WP01); only post-init crashes do.

- **T009 — `core/mcp/stdio/health.go`** — `(*ServerInstance).healthPinger(ctx)` ticker at `recipe.PingPeriodMs` (default 30000). Each tick sends `ping` JSON-RPC request with no params, awaits response with 5 s deadline. Tracks consecutive failures. On second consecutive failure (timeout or transport error), signals supervisor as if the process crashed (close crashCh path, supervisor restarts).

- **T010 — `core/mcp/stdio/status.go`** — `(*ServerInstance).RecipeStatus()` produces the full snapshot per data-model.md:
  - State (string), LastError (from stderr ring tail or initialize error), RestartAttempts (current 5-min window count), LastRestartAt, KeysPresent (always true at this layer; recipes/secrets resolution is upstream), PID, ProtocolVersion, ServerName, ServerVersion (from initialize response), ToolCount / ResourceCount / PromptCount (from cached lists), StderrTail (last 4 KiB of the ring buffer), UpdatedAt.
- `(*Pool).RecipeStatus(id) (RecipeStatus, bool)` — public read accessor. Mutex protects the map; per-instance state captured under instance's own lock.

- **T011 — EOF detection** — wire into WP01's reader goroutine: when `framer.Read()` returns `io.EOF`, the reader closes `crashCh` and exits. Same for the writer on broken-pipe errors. Stderr pump survives the crash (drains until EOF) so the ring buffer captures any final exit message.

- **T012 — Tests** —
  - `supervisor_test.go`: fake server with `--crash-on-call`; trigger crash; assert restart fires with measured backoff (use a clock injection — `Pool.Options{ Now: func() time.Time }` or similar; backoff is wallclock-mockable). Assert third crash within 5 min → `state=failed`. Gate the wallclock-only path behind `-tags=slow` if you can't mock cleanly; else mock.
  - `health_test.go`: fake server with `--ignore-pings`; assert two consecutive ping failures trip a restart. Gate behind `-tags=slow` with a documented threshold so default CI stays fast.
  - `status_test.go`: drive a server through stopped → starting → running → restarting → running; assert RecipeStatus reflects each state correctly. Stderr tail returns the seeded fake-server log line.
  - EOF test: kill the fake server externally via `cmd.Process.Kill()`; assert restart cycle fires.

## Acceptance

- `go test -race -count=1 -short ./core/mcp/stdio/...` ≥ WP01 baseline + new tests.
- A4 from spec: external kill triggers auto-restart within 1 s of EOF detection; status pill cycles `running → restarting → running`.
- A8 from spec: two consecutive failed pings restart the server.
- NFR-002 still satisfied: goroutine count returns to baseline 100 ms after `Close`, even after the supervisor has fired N restarts.
- `core/mcp/stdio/server.go` may be modified (small extensions) but the WP01-owned files keep their primary ownership; WP02-owned files are listed in `wps.yaml`.

## Constraints

- Do NOT introduce a global clock service. Inject the clock via `PoolOptions` if testability needs it.
- Do NOT swallow restart errors silently — emit slog events with `mcp.recipe`, `mcp.attempt`, `mcp.backoff_ms`.
- Do NOT auto-restart on first-`initialize` failure (per spec.md §9 edge case 2). Only post-init crashes.
- Do NOT touch `core/mcp/fixture/`, frontend, or `core/rpc/`.

## Branch strategy

Branch `wp02-stdio-resilience` off `main`.
