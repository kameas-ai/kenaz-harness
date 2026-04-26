---
work_package_id: "WP01"
title: "stdio pool foundations + Pool surface"
dependencies: []
planning_base_branch: "main"
merge_target_branch: "main"
branch_strategy: "Branch wp01-stdio-pool-foundations off main; merge back when WP01 acceptance gate passes."
subtasks:
  - "T001"
  - "T002"
  - "T003"
  - "T004"
  - "T005"
  - "T006"
  - "T007"
phase: "Phase 1+2 — Foundations + Pool surface"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-26T16:35:00Z"
    agent: "system"
    action: "WP prompt authored manually after Plan agent could not write files."
---

# Work Package Prompt: WP01 — stdio pool foundations + Pool surface

## Goal

Implement the real, full-featured stdio MCP pool: framer, protocol shapes for every method we speak (tools, resources, prompts, ping, plus all standard notifications), per-server response router by id, ring buffer for stderr, server lifecycle (spawn → initialize → close), and `*stdio.Pool` implementing `core/mcp.Pool`. End-state: a child process spawned over stdio per the MCP spec, `tools/list` + `tools/call` aggregated through `Pool.Tools()` / `Pool.Call()`, with concurrent calls correctly multiplexed by JSON-RPC id.

## Spec / plan references

- Spec: §FR-001..FR-006, FR-009, FR-011..FR-014, NFR-001, NFR-002.
- Plan: Phase 1 (Foundations) + Phase 2 (Pool surface).
- Data-model: `Framer`, `RequestEnvelope`, `ResponseEnvelope`, `RingBuffer`, `ResponseRouter`, `ServerInstance`, `Pool`.

## Prerequisites

None. Branch directly from main.

## Subtasks

- **T001 — `core/mcp/stdio/framer.go`** — newline-delimited JSON-RPC 2.0 reader/writer. `bufio.Scanner` with `MaxScanTokenSize = 4 MiB`, custom split that strips trailing `\r`. Non-JSON lines return sentinel `errSkipped` so the reader loop can `continue` instead of dying. Write side uses `json.Encoder` followed by `\n`. Tests: `framer_test.go` covers round-trip, multi-line input including malformed lines, oversized lines (4 MiB +1) → transport error and reader exits.

- **T002 — `core/mcp/stdio/protocol.go`** — message shapes for: `initialize` (request + response with `protocolVersion`, `capabilities`, `clientInfo`, `serverInfo`), `tools/list`, `tools/call`, `resources/list`, `resources/read`, `resources/subscribe`, `prompts/list`, `prompts/get`, `ping`, plus server→client `roots/list`, `sampling/createMessage`. Notification shapes: `notifications/initialized`, `notifications/cancelled`, `notifications/progress`, `notifications/message`, `notifications/tools/list_changed`, `notifications/resources/list_changed`, `notifications/resources/updated`, `notifications/prompts/list_changed`. `RequestEnvelope` / `ResponseEnvelope` / `Notification` / `RPCError`. Constant `SupportedProtocolVersion = "2024-11-05"`.

- **T003 — `core/mcp/stdio/router.go`** — `ResponseRouter` with `Register(id int64) <-chan ResponseEnvelope`, `Deliver(env)`, `Cancel(id)`. Late deliveries are dropped via non-blocking send: `select { case ch <- env: default: log.Debug("router.late_delivery", ...) }`. `Cancel(id)` removes the entry atomically; the channel is closed so any caller still blocking on it unblocks with a zero value (caller must check ctx.Err()). Tests: parallel registers + delivers; cancellation followed by late delivery does not block; race-detector clean.

- **T004 — `core/mcp/stdio/ringbuf.go`** — fixed 64 KiB lock-around-write ring buffer for stderr. `Write(p []byte) (n int, err error)` never returns an error. `Snapshot(maxBytes int) string` returns the tail. Tests: assert occupancy after >64 KiB write stays at 64 KiB; Snapshot ordering is correct.

- **T005 — `core/mcp/stdio/server.go`** (lifecycle subset) — `ServerInstance` per data-model.md. `Spawn(ctx, recipe, env)` does `exec.CommandContext`, wires stdin/stdout/stderr pipes, starts reader/writer/stderr-pump goroutines under a `sync.WaitGroup`, sends `initialize`, awaits response within `recipe.InitTimeoutMs` (default 5000) deadline. Note: research flagged a `firstByteTimeout` (30 s) distinct from `init_timeout_ms` for `npx` cold-spawn; implement both — `firstByteTimeout` is the deadline for the first byte on stdout (covers npm fetch); `init_timeout_ms` starts ticking once we send `initialize`. On `initialize` success, send `notifications/initialized`. `Close(ctx)` does SIGTERM, waits 2 s, then SIGKILL; closes stdin first to nudge graceful exit; `Wait()`s the cmd; signals `doneCh`; waits the WaitGroup. NO restart logic in this WP — that's WP02.

- **T006 — `core/mcp/stdio/pool.go`** — `*Pool` implementing `core/mcp.Pool`. `NewPool(opts PoolOptions)` where `PoolOptions{ Sampler SamplingHandler; Roots RootsHandler; Broker EventPublisher; Logger *slog.Logger }`. WP03 fills in concrete Sampler/Roots; WP01 just declares the interfaces and accepts nil. `Open(ctx, specs)` spawns concurrently via `errgroup.Group`; one bad spec doesn't poison the others (failures recorded on the instance, surfaced via WP02's RecipeStatus). `Close(ctx)` fans out close; collects first error; returns after all goroutines exit. `Tools(ctx)` aggregates each running server's cached tool list (cached at initialize-time + on `notifications/tools/list_changed`). `Call(ctx, server, tool, args)` looks up instance, calls `inst.CallTool(ctx, tool, args)` which assigns id, registers a router channel, writes envelope, blocks on channel; ctx cancellation sends `notifications/cancelled` and returns ctx.Err(). Compile-time witness `var _ mcp.Pool = (*Pool)(nil)`.

- **T007 — `core/mcp/stdio/testdata/fake-mcp-server/main.go`** — small in-tree fake MCP server compiled via `go test`. Implements `initialize`, `tools/list` (returns 1-2 fake tools), `tools/call` (echoes args back), `ping`. CLI flags for testing edge cases: `--banner` (emits non-JSON banner line on stdout), `--slow-init` (delays initialize response), `--crash-on-call` (exits after first tool/call). The pool's integration tests build this binary and spawn it via `go test`'s built-in `BuildArtifacts` pattern (or `os.Executable()` + a TestMain that re-execs in fake-server mode based on an env var — pick whichever idiom the repo already uses for similar fakes).

## Acceptance

- `go test -race -count=1 -short ./core/mcp/stdio/...` passes including:
  - Framer round-trip, malformed-line skip, oversized-line transport error.
  - Router concurrent register/deliver, cancellation + late delivery (no block).
  - Server spawn → initialize → close happy path + first-byte timeout + init-deadline timeout.
  - Pool: 50 concurrent `Call`s against the fake server with deterministic id-correlated responses.
- Goroutine count returns to baseline within 100 ms of `Close` (NFR-002).
- `go test -race -count=1 -short ./core/...` ≥ baseline 850 + the new tests.
- The pool implements `core/mcp.Pool` (compile-time witness).
- No production code path uses the new pool yet — wiring lives in WP05.

## Constraints

- stdio transport only. No HTTP/SSE.
- Stdlib only for JSON-RPC. Don't pull in a third-party JSON-RPC library.
- The fake server lives in `testdata/`; it doesn't ship in the binary.
- `core/mcp.Pool` (the interface in `core/mcp/pool.go`) does NOT change — the new struct conforms to it.
- Do NOT touch `core/mcp/fixture/` (kept for unit tests of upstream consumers like toolloop).
- Do NOT modify `core/rpc/api.go`, `core/core.go`, or any frontend file.

## Branch strategy

Branch `wp01-stdio-pool-foundations` off `main`, merge when acceptance gate passes.
