---
work_package_id: "WP05"
title: "Outbound A2A client dispatch flow"
dependencies:
  - "WP01"
  - "WP02"
  - "WP04"
  - "WP11"
  - "WP12"
  - "secrets-keychain:WP-resolve-and-zeroize"
  - "policy-engine:WP-allow-decision"
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
  - "T007"
phase: "Phase 5 - Outbound dispatch flow"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP05 – Outbound A2A client dispatch flow

## Goal

Implement `core/acp/client/` — the outbound dispatch coordinator that
wires `PeerRegistry` (WP04) → `PolicyGuard` → `secrets.Backend.Resolve`
→ `AgentCardCache` → `Verifier` → `envelope.Dispatch` (WP02) → store
(WP11) → events (WP12). Implements the `A2AClient` interface from
WP01 including streaming responses, mid-stream cancellation, and
multi-peer concurrency.

## Spec references

- FR-002 — Outbound A2A client.
- FR-006 — Indirect credential resolution for peers.
- FR-009 — Task lifecycle management (state transitions emitted from
  this layer).
- FR-010 — Mid-Task cancellation.
- FR-013 — Policy gate for outbound calls.
- FR-017 — Multi-peer fan-out support.
- C-002 — Append-only event log immutability.
- C-004 — No inline plaintext credentials.
- US1 Acceptance Scenarios 1, 2, 3.
- US3 Acceptance Scenarios 1, 2, 3.
- NFR-001 — Loopback dispatch overhead < 25 ms p95.
- NFR-002 — Cancellation responsiveness < 1 s p99.
- NFR-009 — ≥ 32 concurrent in-flight Tasks.

## Plan references

- §3 Public API — `A2AClient` interface, `DispatchOption`.
- §4 Internal Layering, "Outbound flow" — the exact sequencing this
  WP implements end-to-end.
- §6.1 secrets-keychain integration.
- §6.5 policy-engine integration.
- §8 R7 — cancellation must close response body within 1 s.

## Subtasks

- T001 — Implement `client.Client` struct holding handles to
  `PeerRegistry`, `AgentCardCache`, `PolicyGuard`,
  `secrets.Backend`, `Verifier`, `envelope.Envelope`, `store.Store`,
  `events.Emitter`. Constructor wires the dependencies; expose via
  the `A2AClient` interface.
- T002 — Implement `Dispatch(ctx, peerID, skillID, body, opts...)`
  per plan §4 Outbound flow:
  1. Lookup peer (return `ErrPeerNotFound` cleanly).
  2. Run `PolicyGuard.AllowOutbound(peer, skill)`; on refusal emit
     `peer_auth_failed`, return `ErrPolicyDenied`.
  3. Resolve `auth_ref` via `secrets.Backend.Resolve`; emit
     `peer_auth_attempted`; on failure return
     `ErrCredentialResolution`.
  4. Get card from cache (handles `card_fetched` / `card_cache_hit`
     / `card_cache_miss` automatically, via WP04).
  5. Verifier.Verify(card).
  6. Mint `Task` (ULID), persist via WP11 store, emit
     `task_created`.
  7. Hand off to envelope `Dispatch`; pump returned `<-chan Message`
     into store + `message_sent`/`message_received` events.
  8. On terminal state, emit `task_state_changed` (running →
     completed/failed/cancelled) and the matching `task_failed`
     event if applicable.
- T003 — Implement `Cancel(ctx, taskID)`: lookup task, call
  `envelope.CancelTask(a2a_task_id)`, emit `task_cancelled` and
  `task_state_changed`. Honor `context.Context` deadline so caller
  cancellation closes the underlying stream within 1 s p99.
- T004 — Resolved credentials are wrapped in `core/secrets.Secret`
  and zeroized after `envelope.Dispatch` builds the wire request.
  No `string`-typed credential field exists in this package.
- T005 — Add `DispatchOption` knobs: parent session id,
  preflight-snapshot id (for replay / FR-019), per-call timeout.
  Snapshot id is embedded on `task_created` per FR-019.
- T006 — Concurrency / fan-out tests: fixture peer servers (in-
  process via WP02 envelope test mode) drive 32 parallel Dispatch
  calls; assert no lifecycle ordering errors, no event-log gaps, and
  per-task event sequences are independently consistent (NFR-009;
  US3 Acceptance 1).
- T007 — Cancellation test: a slow-stream fixture peer holds open;
  cancel the parent context; assert `envelope.CancelTask` is invoked
  within 1 s p99 (NFR-002), `task_cancelled` is emitted, and the
  socket is closed (R7 mitigation).

## Acceptance criteria

- `go test ./core/acp/client/...` passes; coverage ≥ 80%.
- US1 Acceptance Scenarios 1, 2, 3 pass as black-box tests using
  WP02 envelope test-mode + a real on-disk event log.
- US3 Acceptance Scenario 1 pass: 3 fan-out peers (one success, one
  failure, one cancelled) produce three independent task lifecycles.
- NFR-001 micro-bench under `client_bench_test.go` reports loopback
  dispatch overhead < 25 ms p95.
- NFR-002 cancellation bench reports < 1 s p99 from `Cancel` call to
  `task_cancelled` emit.
- A test injecting a synthetic API key string into `auth_ref` and
  the dispatched body confirms the persisted event log contains
  zero plaintext occurrences (C-004; NFR-004 cross-check; full
  matrix in WP13).

## Files to create / modify

- `core/acp/client/client.go`
- `core/acp/client/dispatch.go`
- `core/acp/client/cancel.go`
- `core/acp/client/client_test.go`
- `core/acp/client/client_bench_test.go`
- `core/acp/client/fanout_test.go`

## Definition of done

- All subtasks complete; tests green; lint clean; bench numbers in
  PR description.
- Cross-mission dependencies (`secrets-keychain` resolve API,
  `policy-engine` Allow decision API, `event-log` append) explicitly
  cited.
- No `core/acp/client/` import of `a2aproject/a2a-go` (WP02 lint
  enforces).
- PR merged.
