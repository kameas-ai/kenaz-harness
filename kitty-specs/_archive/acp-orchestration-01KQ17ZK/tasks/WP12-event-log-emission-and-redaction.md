---
work_package_id: "WP12"
title: "Event-log emission, redaction-aware payloads, and acp/ namespace"
dependencies:
  - "WP01"
  - "WP11"
  - "event-log:WP-append-and-namespace-registration"
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
phase: "Phase 12 - Event-log emission"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP12 – Event-log emission, redaction-aware payloads, and acp/ namespace

## Goal

Implement `core/acp/events/` — the SINGLE chokepoint that emits every
A2A-generated event into the harness append-only event log under the
`acp/` namespace, with payloads constructed from typed `core/acp.*`
shapes (never raw SDK wire bodies) so plaintext credentials cannot
enter the log. Register all 11 event kinds with the event-log
namespace registry.

## Spec references

- FR-011 — Append-only event log integration.
- FR-012 — Credential and payload redaction in event log.
- FR-019 — Replay-friendly Task records (snapshot id on
  `task_created`).
- NFR-003 — Event log append latency < 5 ms p99.
- NFR-004 — Plaintext credential leakage = zero.
- NFR-008 — Audit completeness 100%.
- C-002 — Append-only event log immutability.
- C-007 — SOC 2-readiness.
- US4 Acceptance Scenarios 1, 2, 3.
- SC-003 — 100% replayable event-log trail.
- SC-004 — Zero plaintext credentials.

## Plan references

- §4 Internal Layering, "Events" — single chokepoint to
  `core/event.Log.Append`.
- §5.3 Event-log kinds — full table of 11 acp/ kinds.
- §6.2 event-log integration — namespace, redaction, blob refs.
- §8 R5 — chunk coalescing for high-frequency streaming.
- §8 R6 — emitter reconstructs from `core/acp.*` types, never SDK
  wire body.

## Subtasks

- T001 — Define `events.Emitter` interface with one method per kind:
  `TaskCreated`, `MessageSent`, `MessageReceived`,
  `TaskStateChanged`, `TaskCancelled`, `TaskFailed`, `CardFetched`,
  `CardCacheHit`, `CardCacheMiss`, `PeerAuthAttempted`,
  `PeerAuthFailed`. Default impl is backed by `core/event.Log.Append`.
- T002 — Define typed payload structs per kind under
  `core/acp/events/payloads.go`. Each payload explicitly excludes
  any field carrying a resolved credential. Every payload carries
  `session_id`, `task_id` (where applicable), `peer_id` (where
  applicable), and `emitter_id="acp/<role>"`.
- T003 — Register the `acp/` namespace with the event log
  (event-log FR-017) along with the kind list. Contribute any A2A-
  specific credential patterns (auth-header substrings, common
  bearer formats) to the redaction pattern catalog.
- T004 — Embed `ResolvedGraph.snapshot_id` on every `task_created`
  payload (FR-019); blank if no graph context (test-mode).
- T005 — Implement chunk-event coalescing per plan §8 R5: contiguous
  `chunk` `MessageSent` / `MessageReceived` runs within a single
  task can be coalesced into a `chunk_batch` payload keyed by
  `(task_id, sequence_range)`. Per-peer flag opts out for cases
  needing every chunk individually audited.
- T006 — Tests using a real on-disk event log under `t.TempDir()`
  (no mocks; charter testing standard):
  - Success-path event ordering: `task_created` → ≥ 1 ×
    `message_sent`/`message_received` → `task_state_changed`
    (running) → terminal `task_state_changed` (US4 Acc. 1).
  - Failure-path consistency: partial messages persisted, failure
    entry includes protocol error, log append-only invariant holds
    (US4 Acc. 3).
  - Plaintext credential injection test: a synthetic key string is
    pumped through every `Message.Payload`, every event-payload
    field, and every `auth_ref` value; persisted log contains zero
    occurrences of the plaintext string (NFR-004; SC-004 cross-
    check).
  - Append latency benchmark: p99 < 5 ms on a developer laptop
    (NFR-003).

## Acceptance criteria

- `go test ./core/acp/events/...` passes; coverage ≥ 80%.
- Bench (`acp_bench_test.go`) reports event-log append p99 < 5 ms.
- Black-box test with a real on-disk event log writes the full
  success-path event chain and `grep` for synthetic credentials
  returns zero matches across the full audit suite (NFR-004).
- All 11 kinds registered with the event-log namespace registry;
  verified by an introspection test against the event log API.
- Coalescer round-trips contiguous chunk runs into `chunk_batch`
  entries; opt-out flag preserves individual chunk events.

## Files to create / modify

- `core/acp/events/emitter.go`
- `core/acp/events/payloads.go`
- `core/acp/events/coalescer.go`
- `core/acp/events/patterns.go` — A2A credential pattern
  contributions to the redaction catalog.
- `core/acp/events/emitter_test.go`
- `core/acp/events/acp_bench_test.go`

## Definition of done

- All subtasks complete; tests green; lint clean; bench numbers in
  PR description.
- Cross-mission dependency on `event-log` `Append` API and namespace
  registry documented in PR.
- No mocking of the event log in tests that assert audit / replay
  (charter testing standard).
- PR merged.
