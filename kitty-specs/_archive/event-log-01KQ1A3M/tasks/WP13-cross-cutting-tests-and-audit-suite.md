---
work_package_id: "WP13"
title: "Cross-cutting tests, audit suite, and performance gates"
dependencies:
  - "WP01"
  - "WP02"
  - "WP03"
  - "WP04"
  - "WP05"
  - "WP06"
  - "WP07"
  - "WP08"
  - "WP09"
  - "WP10"
  - "WP11"
  - "WP12"
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
phase: "Phase 12 - Cross-cutting validation"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP13 – Cross-cutting tests, audit suite, and performance gates

## Goal

Land the cross-cutting validation that proves the substrate meets every
charter-mandated success criterion and non-functional requirement. The
audit suite is the SOC 2 evidence pack: it drives a full session matrix
through the public API, scans every persisted payload for plaintext
credentials, exercises tamper detection over the chain at every offset,
and asserts replay determinism end-to-end. Performance gates document
NFR-001 / NFR-002 / NFR-007 baselines.

## Spec references

- SC-001 — Replay byte-identical reproduction.
- SC-002 — Branch with same accumulated context.
- SC-003 — Zero plaintext credentials reach disk.
- SC-004 — 100 % single-byte tamper detection.
- SC-005 — Append latency < 5 ms p99 under realistic concurrent load.
- SC-006 — New emitter / kind requires no consumer changes.
- NFR-001 / NFR-002 / NFR-003 / NFR-004 / NFR-005 / NFR-006 / NFR-007 —
  all NFR gates.
- C-005 — SOC 2-readiness audit evidence.

## Plan references

- §10 Test strategy alignment — black-box integration only
  (DIRECTIVE_036), property tests for redaction + chain, replay
  determinism cycle, concurrency under `-race`, performance benchmarks.
- §12 Acceptance traceability — table mapping every spec ID to plan
  section to validation here.
- DIRECTIVE_036 — black-box only; tests drive the public
  `Emitter`/`Reader`/`Verifier`/`Replayer`/`Brancher` API. No imports
  of internal subpackages from this WP's tests.

## Subtasks

- T001 — **Audit suite** under `core/event/audit_test.go`: drive a
  full session matrix (LLM, MCP, A2A, scheduler, bundle, trust,
  context, session emitters) through `Emitter.Append` with payloads
  containing every credential pattern in the matcher catalog plus
  random credential-shaped substrings; scan every persisted payload
  for plaintext credentials; **zero matches required** (SC-003 /
  NFR-003).
- T002 — **Chain integrity property test** under
  `core/event/chain_property_test.go`: 1K trials, each tampering one
  random byte at one random offset in one random row in one random
  session; assert detection 100 % of the time (SC-004 / NFR-005).
- T003 — **Replay determinism** under `core/event/replay_matrix_test.go`:
  for each session shape in the matrix, record-replay-rerecord-compare;
  byte-equality required (SC-001 / NFR-004). Includes branched
  sessions to exercise SC-002.
- T004 — **Concurrency** under
  `core/event/concurrency_integration_test.go`: ten emitters writing
  to ten sessions for one minute under `go test -race`; assert zero
  deadlocks, zero lost writes, zero chain breaks. Append p99 < 5 ms
  (SC-005 / NFR-001).
- T005 — **Performance gates** under `core/event/bench_test.go`:
  documented baselines for NFR-001 (5 ms p99 append), NFR-002 (1 ms
  p95 redaction), NFR-007 (50 ms p95 query against a synthetic 10M
  corpus). Numbers checked into `docs/event-log/perf-baselines.md`;
  CI compares within tolerance.
- T006 — **Forward-compat + SC-006 surrogate**: new-emitter test
  registers a fresh prefix at runtime, writes a kind under it, and
  asserts no other test in the matrix changed; older-reader test
  preserves an unknown kind verbatim and queries it back without
  error (NFR-006). Acceptance traceability matrix from plan §12 is
  realized as a checklist file; this WP adds tests for any rows still
  marked unverified.

## Acceptance criteria

- All NFR gates met or documented as baselines:
  - NFR-001 < 5 ms p99 append.
  - NFR-002 < 1 ms p95 redaction.
  - NFR-003 = 0 plaintext credentials in audit-suite scan.
  - NFR-004 = 100 % byte-identical replay across the matrix.
  - NFR-005 = 100 % single-byte tamper detection.
  - NFR-006 — unknown kinds preserved by older readers.
  - NFR-007 = baseline recorded; not a hard gate per plan §7.
- All SC items asserted by at least one test in the audit suite.
- DIRECTIVE_036 honored: every test in this WP imports only
  `core/event` (not internal subpackages).
- `go test ./... -race` green on a clean tree; CI green.

## Files to create / modify

- `core/event/audit_test.go`
- `core/event/chain_property_test.go`
- `core/event/replay_matrix_test.go`
- `core/event/concurrency_integration_test.go`
- `core/event/bench_test.go`
- `docs/event-log/perf-baselines.md`
- `docs/event-log/acceptance-traceability.md` (checklist mapping
  spec IDs to test names; mirror of plan §12 with test references)

## Definition of done

- Audit suite green; performance baselines recorded.
- Acceptance traceability matrix complete; every FR / NFR / SC / C
  ID maps to at least one test file (DIRECTIVE_010 fidelity).
- `go test -race ./core/event/...` green; `golangci-lint run` clean
  for the whole package; charter Quality Gates satisfied.
- PR opened against `feat/event-log-01KQ1A3M` targeting `main`,
  ≥ 1 maintainer approval, squash-merge.
