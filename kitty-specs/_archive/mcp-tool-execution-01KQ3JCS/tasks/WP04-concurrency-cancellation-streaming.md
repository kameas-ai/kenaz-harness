---
work_package_id: "WP04"
title: "Concurrency, cancellation, iteration cap, streaming chips"
dependencies:
  - "WP01"
  - "WP02"
  - "WP03"
planning_base_branch: "main"
merge_target_branch: "main"
branch_strategy: "Planning artifacts were generated on main; completed changes must merge back into main."
subtasks:
  - "T001"
  - "T002"
  - "T003"
  - "T004"
  - "T005"
phase: "Phase 4 — Concurrent dispatch + UI feedback"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-26T00:30:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP04 — Concurrency, cancellation, iteration cap, streaming chips

## Goal

Lift the loop from "single-tool" to "real" — bounded parallel
dispatch, cancellable via the existing Stop button, iteration
bound to prevent runaway loops, and inline tool-chip rendering
on the chat surface so the user sees what's happening.

## Spec references

- Spec: US2, US5, FR-004, FR-006, FR-008, FR-010, NFR-001, NFR-004.
- Plan: § "Step 4".

## Prerequisites

WP03 merged.

## Subtasks

- **T001 — Bounded parallelism.** New
  `core/toolloop/concurrent.go` runs the per-iteration tool
  dispatches through a `chan struct{}` semaphore (default
  capacity 4, configurable via `Loop.parallel`). Results are
  joined in declared order before the LLM re-invocation. A
  failure of one tool does NOT cancel the others (FR-004).
- **T002 — Iteration cap.** Add `maxIter` enforcement to the
  loop. On exhaustion, append a synthetic `Message{Role:
  "assistant", Content: "Tool loop exceeded N iterations…"}`
  to the history with finish reason `iter_cap`. Default 8
  (FR-006).
- **T003 — Cancellation.** Honor `ctx.Done()` between
  iterations and DURING in-flight `pool.Invoke` calls. On
  user-initiated stop (the existing Stop button cancels the
  parent ctx), the loop drops pending dispatches and emits a
  `tool_loop_cancelled` event. p95 stop latency ≤ 1s (FR-008).
- **T004 — StreamToolInvocation chunk kind.** Add
  `StreamToolInvocation` to `core/llm/llm.go StreamEventKind`.
  In the loop, emit two `llm:stream-chunk` events per call:
  `tool_invocation_started{tool, server, args_summary}` and
  `tool_invocation_finished{tool, status, summary}`. Frontend
  side: new `ToolInvocationChip.vue` component renders inline
  in `MessageBubble.vue` between text deltas. Update
  `useSession.ts` to thread the new chunk kind.
- **T005 — Tests.** Cover US2 (sequential-as-presented but
  concurrent-internally with ordered merge), US5 worst-case
  iter-cap, FR-008 stop within 1s p95 (timing test with
  `time.Now`), frontend snapshot for the chip rendering.

## Acceptance

- US2 + US5 + FR-008 acceptance gates pass.
- NFR-001 measured: orchestrator overhead per round ≤ 50 ms p99
  (excluding actual tool work). Add a benchmark in
  `core/toolloop/loop_bench_test.go`.
- Frontend chip rendering visible in `wails dev`.

## Branch strategy

Branch `wp04-concurrency-streaming` off `main`, merge back when
WP04 gate passes.
