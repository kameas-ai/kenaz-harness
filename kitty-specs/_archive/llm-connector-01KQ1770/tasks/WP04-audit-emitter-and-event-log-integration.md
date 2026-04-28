---
work_package_id: "WP04"
title: "Audit emitter and event-log integration"
dependencies:
  - "WP01"
  - "WP02"
  - "WP03"
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
phase: "Phase 2 - Audit + retry middleware"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP04 – Audit emitter and event-log integration

## Goal

Implement `core/llm/audit` — the single chokepoint that emits every
connector-generated event into the harness append-only event log under
the `llm/` namespace, with payloads constructed from typed structs (not
raw wire bodies) so plaintext credentials can never enter the log.

## Spec references

- FR-014 — Append-only event log integration (every request, chunk,
  retry, error, cancellation).
- FR-015 — Credential redaction in event log.
- FR-020 — Replay reproducibility (snapshot id on `request_submitted`).
- NFR-004 — Event log append latency < 5 ms p99.
- NFR-007 — Plaintext credential leakage = zero.
- NFR-008 — Credential-pattern redaction recall ≥ 99 %.
- NFR-012 — Audit completeness (request, response, usage, latency).
- C-003 — Append-only event log immutability.
- US3 Acceptance Scenarios 1, 2, 3, 4.
- R2 — credentials must not leak via `params: map[string]any`
  round-tripping; emitter reconstructs from typed shape.
- R9 — provider-specific credential pattern catalog contributions.

## Plan references

- §4 Internal Layering — AuditEmitter as the single chokepoint.
- §5.3 Event-log kinds emitted by the connector — full event-kind
  table that this WP must support.
- §6.2 event-log integration — `Append`, `llm/` namespace, redaction
  is the event-log pipeline's responsibility but the connector must
  never put creds in the payload to begin with.

## Subtasks

- T001 — Define `core/llm/audit.Emitter` interface and a default
  implementation backed by `core/event.Log.Append`. All event kinds
  from plan §5.3 are first-class methods (`RequestSubmitted`,
  `PreflightResolved`, `PreflightFailed`, `CapabilityRejected`,
  `RetryAttempted`, `StreamChunk`, `ResponseFinal`, `Cancelled`,
  `Error`, `PolicyDenied`).
- T002 — Build typed payload structs per kind; serialization explicitly
  excludes any field carrying a resolved credential. The emitter
  reconstructs the redacted view from `GenerationRequest` + adapter
  metadata, never from raw HTTP bodies.
- T003 — Register the `llm/` event-kind namespace with the event log
  and contribute provider-credential patterns (`sk-ant-*`, `sk-*`,
  Bedrock signature substrings, OpenRouter keys, Ollama tokens) to
  the redaction pattern catalog.
- T004 — Wire `Emitter` into `Registry.Stream`, `CapabilityGate`, and
  `PreflightCoordinator` (replaces the hooks left by WP02 / WP03).
- T005 — Embed `ResolvedGraph.snapshot_id` on every
  `request_submitted` payload for replay (FR-020); blank if no graph
  context (test-mode).
- T006 — Tests using a real on-disk event log under `t.TempDir()` (no
  mocks per charter testing standard): assert success-path event
  ordering (`request_submitted` → N × `stream_chunk` →
  `response_final`); failure-path consistency (US3 Acceptance 4);
  zero-plaintext-credential assertion using a synthetic API-key
  string injected through every payload field.

## Acceptance criteria

- `go test ./core/llm/audit/...` passes; coverage ≥ 80 %.
- Black-box test using a real on-disk `core/event` log writes the
  full success-path event chain and the assertion `grep -E
  '(sk-ant-[A-Za-z0-9]+|sk-[A-Za-z0-9]+)' eventlog.jsonl` returns
  zero matches.
- Append latency benchmark in `audit_bench_test.go` reports p99 < 5 ms
  on a developer laptop (NFR-004).
- A test that pumps a known credential string through every field of
  `GenerationRequest.Params` and `Response.Content` confirms the
  persisted log contains the redacted form, not the plaintext
  (NFR-007, NFR-008 ≥ 99 % recall on the catalog).
- Replay test: a recorded `request_submitted` payload is sufficient
  to identify the same `ProviderProfile` snapshot (FR-020).

## Files to create / modify

- `core/llm/audit/emitter.go`
- `core/llm/audit/payloads.go`
- `core/llm/audit/patterns.go` (provider credential pattern
  contributions to the redaction catalog)
- `core/llm/audit/emitter_test.go`
- `core/llm/audit/audit_bench_test.go`
- `core/llm/registry/registry.go` (wire emitter)
- `core/llm/capabilities/gate.go` (wire emitter)
- `core/llm/preflight.go` (wire emitter)

## Definition of done

- All subtasks complete; tests green; lint clean; bench numbers
  recorded in PR description.
- No mocking of the event log in tests that assert audit/replay
  (charter testing standard).
- Cross-mission dependency on `event-log:WP-append-and-namespace-
  registration` documented in PR.
- PR merged.
