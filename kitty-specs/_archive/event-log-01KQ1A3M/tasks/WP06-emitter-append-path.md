---
work_package_id: "WP06"
title: "Emitter append path: validate, redact, hash, link, persist"
dependencies:
  - "WP01"
  - "WP02"
  - "WP03"
  - "WP04"
  - "WP05"
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
phase: "Phase 3 - Append path"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP06 – Emitter append path: validate, redact, hash, link, persist

## Goal

Wire WP02 (id + namespacing + kind), WP04 (redaction), WP05 (canonical +
hash), and WP03 (store) into the single non-bypassable
`Emitter.Append` write path described in plan §4.1. This is the only
ingress for events; redaction is unconditional; the per-session
chain-head row is locked for serialization within a session; the
transaction boundary is the durability boundary.

## Spec references

- FR-001 — Single shared event-log surface.
- FR-002 — Typed kinds; well-formed unknown kinds accepted forward-compat.
- FR-003 — Append-only at the public API.
- FR-004 — Hash-chain link computed per append.
- FR-005 — Redaction pipeline applied before persistence.
- FR-016 — ULID assigned at append.
- FR-017 — Emitter id validated against allowlist.
- FR-018 — Helper-constructed cancel/error/timeout events accepted.
- NFR-001 — Append latency < 5 ms p99.
- NFR-002 — Redaction overhead < 1 ms p95 (composed in this path).
- C-002 — Append-only at storage; no mutation paths.
- C-004 — Redaction non-bypassable.

## Plan references

- §4.1 Emit path — full nine-step procedure.
- §3 Public API — `Emitter` interface and `AppendInput` shape.
- §6.1 storage-foundations integration — txn semantics, WAL, latency budget.
- Risk R4 — multi-emitter concurrency mitigation via per-session
  chain-head locking.

## Subtasks

- T001 — Implement `Emitter` constructor that wires `redact.Pipeline`,
  `kind.Registry`, `idgen.ULID`, `chain.Canonical`/`chain.PayloadHash`,
  and the unexported `log.Store`. Pipeline is held for the process
  lifetime; nil pipeline is an error at construction (C-004 enforced
  at construction).
- T002 — Implement `Append` per plan §4.1: validate emitter id (WP02),
  validate kind well-formedness (WP02), call `Pipeline.Apply` (WP04),
  canonicalize + hash (WP05), open txn, read+lock session chain-head
  row (or zero-hash on first append in session), generate ULID,
  insert row, update chain-head row, commit, return `Event`.
- T003 — Concurrency guarantees: chain-head row lock serializes writes
  within a session; cross-session writes proceed in parallel; surface
  `context.Cancelled` cleanly mid-append (txn rollback).
- T004 — Implement crash-safety semantics: emitter crash before insert
  returns → event simply does not exist (durability boundary = post-write
  return), per spec edge case.
- T005 — Black-box integration tests under
  `core/event/append_integration_test.go`: real on-disk DB, ten
  emitters writing to ten sessions for one minute under
  `go test -race`; assert zero deadlocks, zero lost writes, zero chain
  breaks; assert append p99 < 5 ms (NFR-001).
- T006 — Negative-path tests: unknown emitter prefix → `ErrUnknownEmitter`;
  malformed kind → `ErrInvalidKind`; pipeline panic → append aborts
  with `ErrRedactionBypassed`; cancellation mid-redaction → no row
  written.

## Acceptance criteria

- Black-box integration tests green: 10×10 concurrency for 60 s under
  `-race`; no deadlocks, lost writes, or chain breaks.
- Benchmark: append p99 < 5 ms on a developer laptop (NFR-001) and
  redaction p95 < 1 ms (NFR-002), measured end-to-end.
- Append is the only path to the events table; static check (build tag
  or analyzer) confirms no other code under `core/event/` calls the
  store's insert primitive (C-002 / C-004).
- All negative paths return typed errors from WP01's taxonomy.
- `go test ./core/event/...` and `go test -race` green.

## Files to create / modify

- `core/event/emitter.go`
- `core/event/emitter_test.go`
- `core/event/append_integration_test.go`
- `core/event/append_bench_test.go`

## Definition of done

- All subtasks complete; integration + benchmark gates met.
- ADR drafted (or referenced) for the per-session chain-head row
  locking strategy if it deviates from the plan default
  (DIRECTIVE_003).
- `go vet`, `golangci-lint run` clean.
