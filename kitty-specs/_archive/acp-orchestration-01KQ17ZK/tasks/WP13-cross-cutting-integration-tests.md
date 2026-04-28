---
work_package_id: "WP13"
title: "Cross-cutting integration tests: fan-out, audit reconstruction, local-first defaults"
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
phase: "Phase 13 - Cross-cutting integration tests"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP13 – Cross-cutting integration tests: fan-out, audit reconstruction, local-first defaults

## Goal

Author the mission's black-box, charter-grade integration tests that
exercise the entire `core/acp/` stack end-to-end through its public
interfaces (DIRECTIVE_036). Tests use real on-disk SQLite, a real
event log, and the WP02 envelope's in-process A2A test mode plus a
fixture A2A peer matrix; no mocks for crossing-boundary collaborators
(charter testing standard).

These tests are the executable acceptance evidence for the spec's
Success Criteria.

## Spec references

- US3 Acceptance Scenarios 1, 2, 3 — multi-peer fan-out, propagating
  cancellation, partial failure merging.
- US4 Acceptance Scenarios 1, 2, 3 — full audit reconstruction.
- US5 Acceptance Scenarios 1, 2, 3 — local-first defaults; no
  outbound traffic when no peer configured.
- US7 Acceptance Scenarios 1, 2 — inbound policy gate.
- NFR-001 — Loopback dispatch overhead < 25 ms p95.
- NFR-002 — Cancellation responsiveness < 1 s p99.
- NFR-005 — Local-first guarantee.
- NFR-006 — Default-transport binding scope.
- NFR-008 — Audit completeness 100%.
- NFR-009 — Concurrency target ≥ 32 in-flight Tasks.
- NFR-010 — SDK upgrade blast radius (static import check).
- C-001, C-002, C-005, C-006 — architectural / security boundaries.
- SC-001 through SC-008 — every measurable outcome.

## Plan references

- §7 Phasing — v1.0 test coverage commitments.
- §4 Internal Layering — outbound + inbound flows the integration
  exercises.
- §6 Integration Points — cross-mission boundaries the integration
  asserts against.
- §8 Risk Register — R1, R3, R5, R6, R7, R8, R9, R10 each have a
  test in this WP.

## Subtasks

- T001 — End-to-end fan-out test (US3): a workflow node fans out to
  three fixture A2A peers. One returns a result, one returns an
  error, one is slow enough to be cancelled. Assert: typed result
  set with one success, one failure, one cancellation; event log
  shows three independent task lifecycles; cancellation propagated
  within 1 s (NFR-002).
- T002 — Audit reconstruction test (US4 / SC-003): run a session
  containing inbound and outbound A2A traffic; query the event log;
  reconstruct the full Task and Message history; assert 100% of
  completed Tasks produce `task_created` + terminal
  `task_state_changed` + ≥ 1 message events (NFR-008); assert zero
  plaintext credentials anywhere in the log under the full A2A
  traffic matrix (NFR-004; SC-004).
- T003 — Local-first test (US5 / SC-008): a clean harness install
  with no peers configured. Run the harness for a five-minute idle
  period under packet-capture instrumentation; assert zero
  outbound A2A bytes leave the loopback interface (NFR-005;
  SC-008). Add a peer with `transport: http_public` without
  `auth_ref` and assert bundle-load refusal 100% across the
  configuration matrix (SC-007).
- T004 — Default-binding-scope test (NFR-006): UDS listener refuses
  non-local connections; loopback listener bound only to
  `127.0.0.1` / `::1`; LAN listener refuses public IPs; public
  transport refuses to construct without `auth_ref` AND non-no-op
  Verifier (C-006).
- T005 — SDK isolation static-import check (NFR-010 / SC-006): a
  go-list-deps assertion across all `core/` packages confirms only
  `core/acp/envelope/` imports `github.com/a2aproject/a2a-go`. A
  probe import added to a non-envelope package fails the
  golangci-lint depguard rule (verified by a gofmt-style `go vet`
  matrix or a meta-test that tries to compile a probe file).
- T006 — Concurrency soak (NFR-009 / SC-005): 32 concurrent
  outbound + inbound tasks on a developer laptop; assert no
  lifecycle ordering errors, no event-log gaps, monotonic message
  sequences within each task, and bench p95 < 25 ms loopback
  overhead (NFR-001 cross-check).

## Acceptance criteria

- `go test ./core/acp/... -race` passes including the integration
  package.
- Spec Success Criteria SC-001 through SC-008 each have one or more
  tests in this WP wired to the corresponding assertion.
- `golangci-lint run` (with the WP02 depguard rule) blocks a probe
  `a2a-go` import in a non-envelope package; the meta-test asserts
  this happens.
- The packet-capture idle test reports zero A2A bytes during the
  5-minute window when no peer is configured.
- Audit reconstruction successfully replays a session containing at
  least 32 concurrent tasks with no missing events.

## Files to create / modify

- `core/acp/integration/fanout_test.go`
- `core/acp/integration/audit_test.go`
- `core/acp/integration/localfirst_test.go`
- `core/acp/integration/binding_scope_test.go`
- `core/acp/integration/sdk_isolation_test.go`
- `core/acp/integration/concurrency_test.go`
- `core/acp/integration/testdata/` — fixture bundles, peer servers,
  TLS certs.
- `core/acp/integration/quickstart.md` — promoted quickstart per
  plan §Phase 0 / Phase 1 artifact status.

## Definition of done

- All subtasks complete; tests green under `-race`; lint clean.
- Tests exercise the public surface only (DIRECTIVE_036); no
  reaching into unexported internals.
- Cross-mission dependencies (`event-log`, `secrets-keychain`,
  `bundle-format-resolver`, `storage-foundations`,
  `policy-engine`) cited per test where consumed.
- A2A v1.0 conformance vectors (FR-001) — when the official A2A
  test vectors are publicly available — wired into the
  envelope/integration suite as a follow-up note in the PR if
  not yet shippable.
- PR merged; mission ready for `/spec-kitty.merge`.
