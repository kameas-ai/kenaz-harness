---
work_package_id: "WP08"
title: "Retry middleware and transient/non-transient error classification"
dependencies:
  - "WP01"
  - "WP04"
  - "WP07"
planning_base_branch: "feat/wire-integration"
merge_target_branch: "feat/wire-integration"
branch_strategy: "Planning artifacts were generated on feat/mcp-plans; completed changes merge into feat/wire-integration."
subtasks:
  - "T001"
  - "T002"
  - "T003"
  - "T004"
  - "T005"
phase: "Phase 2 - Connection layer"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP08 – Retry middleware and transient/non-transient error classification

## Goal

Implement `core/mcp/client/retry.go` — a per-server retry middleware that
wraps `connection.call` with exponential backoff and full jitter, gated by
the per-server `RetryPolicy`. Streaming-safe: never re-issues a call once
chunks have been delivered. Non-transient errors bypass retry.

## Spec references

- FR-013 — Per-server retry policy.
- FR-012 — Distinguish transient from non-transient errors.
- US4 — Acceptance scenarios 1, 2, 3.

## Plan references

- §4 Internal Layering — `RetryMiddleware`.
- Risk R4 — streaming-safe retry semantics.
- Risk R7 — buggy server respawn cap.

## Subtasks

- T001 — Implement `retry.go` with `Run(ctx, policy, attemptFn)`. The
  attempt function returns `(result, error)`; the middleware classifies
  errors via `IsTransient` (WP01) and applies exponential backoff with
  full jitter on transient errors.
- T002 — Add streaming-safe gate: a callback `chunksDelivered() bool`
  that the middleware consults before retrying. If true, transient
  errors propagate as terminal failures.
- T003 — Apply the middleware in `connection.call` (WP04) and integrate
  audit emit for `mcp/retry_attempted` (attempt N, backoff delay).
- T004 — Apply per-server retry-budget exhaustion: after exhausting,
  emit `mcp/server_unhealthy` and mark the connection unhealthy in the
  pool (FR-013, US4 Acceptance 4).
- T005 — Tests: fake transport returns `ErrTransportFailure` once then
  succeeds → assert one retry, then success; fake returns
  `ErrTransportFailure` repeatedly → assert
  `ErrRetryBudgetExhausted` after `RetryPolicy.MaxAttempts`; fake
  returns non-transient `ErrInvalidParams` → assert no retry; fake
  returns transient mid-stream after a chunk → assert no retry,
  terminal error.

## Acceptance criteria

- `go test ./core/mcp/client/...` (retry surface) passes; coverage
  ≥ 80 %.
- Backoff delays follow `policy.BaseMS * 2^attempt + rand[0..baseMS]`
  (full jitter); upper-bounded by `policy.MaxMS`.
- Race-free under `-race`.
- Streaming-safety asserted by an explicit test: chunk delivered →
  transient mid-stream → no retry, terminal error.

## Files to create / modify

- `core/mcp/client/retry.go`
- `core/mcp/client/retry_test.go`

## Definition of done

- All subtasks complete; tests green; lint clean.
- PR merged into `feat/wire-integration`.
