---
work_package_id: "WP05"
title: "Retry middleware with exponential backoff, jitter, and error classification"
dependencies:
  - "WP01"
  - "WP04"
planning_base_branch: "main"
merge_target_branch: "main"
branch_strategy: "Planning artifacts were generated on main; completed changes must merge back into main."
subtasks:
  - "T001"
  - "T002"
  - "T003"
  - "T004"
  - "T005"
  - "T006"
phase: "Phase 2 - Audit + retry middleware"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP05 – Retry middleware with exponential backoff, jitter, and error classification

## Goal

Implement `core/llm/retry` — provider-agnostic middleware that wraps
adapter calls with exponential-backoff + full-jitter retries (default
3 attempts, base 250 ms, max 5 s; per-profile overridable), retries
only transient errors, never re-issues a streaming request after any
chunk has been delivered, and emits `llm/retry_attempted` events.

## Spec references

- FR-016 — Per-provider retry with exponential backoff and jitter.
- FR-017 — Distinguish transient from non-transient errors.
- NFR-006 — Retry budget effectiveness (single transient + success
  ≥ 99 % within budget ≥ 3 attempts).
- US4 Acceptance Scenarios 1, 2, 3.
- SC-005 — Single transient failure invisible to bundle author at
  default settings ≥ 99 % across day-one matrix.
- R3 — streaming retry must not double-bill (no re-issue after first
  chunk delivered).

## Plan references

- §4 Internal Layering — RetryMiddleware between AuditEmitter and
  ProviderAdapter.Stream; streaming retry constraint explicit.
- §3 Public API — `RetryPolicy` shape (`max_attempts`, `base_ms`,
  `max_ms`, `jitter`).
- §9 Open Questions — default budget = 3 attempts / 250 ms base / 5 s
  cap / full jitter (matches spec OQ-3 default).

## Subtasks

- T001 — Implement `core/llm/retry.Policy` with defaults and YAML
  unmarshalling matching the profile-config shape (`max_attempts`,
  `base_ms`, `max_ms`, `jitter`).
- T002 — Implement `Middleware.Run(ctx, policy, fn)` where `fn` is the
  adapter-call thunk. On success → return; on `ErrTransient` → sleep
  per backoff (full jitter; honor `ctx`) and retry; on non-transient
  → return immediately.
- T003 — Streaming-safe wrapper: middleware receives a "first-chunk
  delivered" sentinel from the adapter; if the failure happens after
  first chunk, classify as terminal `error` regardless of transience
  (R3); only pre-first-chunk transient failures are retried.
- T004 — On budget exhaustion, return
  `ErrRetryBudgetExhausted{Attempts: []AttemptOutcome}` listing every
  attempt's error and backoff delay.
- T005 — Emit `llm/retry_attempted` for each attempt boundary
  (attempt number, classified error, planned backoff_ms,
  actual_delay_ms after jitter); emit final `llm/error` on
  exhaustion.
- T006 — Tests: fault-injecting fake adapter that returns 429-then-200
  (US4.1), 401 first call (US4.2 — no retry), all-transient until
  budget exhausted (US4.3 — `ErrRetryBudgetExhausted` with attempt
  list), streaming case where chunk delivered then drop (no retry,
  terminal error). Property-style test asserts jitter distribution
  fits within `[0, base * 2^n]`.

## Acceptance criteria

- `go test ./core/llm/retry/...` passes; coverage ≥ 80 %.
- Fault-injection test (single 429 then 200) produces success in
  every run of N=100 (NFR-006 / SC-005 proxy).
- Auth error (401) test produces a single failed attempt (no
  retry) — `Attempts == 1` in the response.
- Streaming test: drop after first chunk does NOT trigger a retry;
  caller observes a terminal error event after the partial stream.
- Budget-exhausted test: `errors.As(err, &ErrRetryBudgetExhausted{})`
  succeeds and the attempt list length equals `policy.MaxAttempts`.
- `llm/retry_attempted` events recorded with monotonic attempt
  numbers and the actual jittered delay.

## Files to create / modify

- `core/llm/retry/policy.go`
- `core/llm/retry/middleware.go`
- `core/llm/retry/middleware_test.go`
- `core/llm/registry/registry.go` (wire middleware between gate and
  adapter)

## Definition of done

- All subtasks complete; tests green; lint clean.
- No silent retries on streaming after first chunk (R3 test green).
- PR merged.
