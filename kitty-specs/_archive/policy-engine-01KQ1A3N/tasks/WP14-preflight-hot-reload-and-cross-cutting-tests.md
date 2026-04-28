---
work_package_id: "WP14"
title: "Pre-flight validation, hot-reload, and cross-cutting tests"
dependencies:
  - "WP01"
  - "WP04"
  - "WP05"
  - "WP06"
  - "WP07"
  - "WP08"
  - "WP09"
  - "WP10"
  - "WP11"
  - "WP12"
  - "WP13"
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
phase: "Phase 14 - Pre-flight validation + hot-reload + cross-cutting tests"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP14 – Pre-flight validation, hot-reload, and cross-cutting tests

## Goal

Land the cross-cutting features that turn the assembled engine into
something an operator trusts on day one: pre-flight validation at
startup (FR-011), hot-reload without restart (NFR-007), validity
windows with skew tolerance (FR-016), and the cross-cutting test suite
that defends every spec acceptance scenario, success criterion, and
NFR end-to-end.

## Spec references

- FR-011 (pre-flight policy check at startup; violations surface as
  startup errors before any session runs).
- FR-016 (time-bound validity windows with skew tolerance).
- NFR-001 (sub-1 ms p99 — final defense).
- NFR-002 (decision determinism).
- NFR-003 (audit completeness — final defense).
- NFR-004 (narrowing soundness — already defended in WP04; cross-
  cutting tests re-confirm at the integrated level).
- NFR-005 (fail-closed completeness).
- NFR-006 (control-catalog parity — every kind has a consumer that
  enforces it).
- NFR-007 (hot-reload under 60 s).
- SC-001 through SC-007 (all measurable outcomes verified end-to-end).
- Edge cases: clock skew, policy removed (revert to prior, fail-closed
  if no prior), excessive `*` allowlist (warning only),
  per-session override vs. org policy.

## Plan references

- Plan §4 step 7 (hot-reload snapshot semantics with drain of
  in-flight evaluations).
- Plan §6 bootstrap order step 6 (pre-flight policy check).
- Plan §7 v1.0 scope.
- Plan §8 R6 (hot-reload mid-flight evaluations), R8 (consumer
  forgets to call Evaluate — CI lint), R9 (artifact rescinded), R10
  (time-skew weaponization).

## Subtasks

- T001: Pre-flight (FR-011): a `core/policy/preflight/` package walks
  the harness's loaded providers, MCP servers, A2A peers, bundle
  sources, packs, and schedules at startup, dry-runs each through
  `engine.Evaluate`, and returns a typed `[]PreflightViolation`. The
  harness boot path refuses to start a session if any violation is
  present and surfaces a structured startup error citing each
  violation's `(consumer_kind, target_id, policy_id, clause_id,
  reason)`.
- T002: Hot-reload (NFR-007): wire the bundle resolver's "policy
  artifact updated/removed" signal to call `engine.Reload`. Snapshot
  semantics from plan §4 step 7: each Evaluate pins the
  `EffectivePolicy` snapshot at call entry; Reload atomically swaps
  the active snapshot via `atomic.Pointer`; the previous snapshot
  survives until in-flight evaluations drain (use a sync.WaitGroup
  per snapshot or equivalent). Test: a long-running evaluation
  started against snapshot A continues to evaluate against A even as
  snapshot B becomes active.
- T003: Validity windows + skew tolerance (FR-016, edge case): honor
  `not_before` / `not_after` against `time.Now()`. Configurable skew
  tolerance with a strict default (e.g., 60 s); beyond tolerance, a
  future-dated policy is "not yet active" and the prior policy
  continues. Reload re-evaluates eligibility on a periodic timer so
  validity-window transitions activate without external triggering.
- T004: Policy removal (edge case): when a contributing artifact is
  removed, recompose `EffectivePolicy`. If no prior policy exists,
  fall to fail-closed (default-deny) and emit
  `policy_unavailable_fail_closed` events for subsequent
  evaluations. Test exhaustively.
- T005: Cross-cutting acceptance test pack — one file per spec User
  Story (1 through 7), each implemented as a black-box integration
  test (DIRECTIVE_036) driving the engine through its public API
  with on-disk event log, real bundle handler, real signature
  verification (via test trust anchors), and real per-kind clauses
  from WPs 06–11. Each test asserts the spec's acceptance scenarios.
- T006: NFR defense pack:
  - **NFR-001**: benchmark of `Evaluate` against a representative
    v1 policy (one clause per registered kind), asserts p99 < 1 ms
    on developer-laptop hardware. Fail CI if regression.
  - **NFR-002**: identical inputs + identical policy → identical
    decision IDs across two engines (modulo the ULID; assert other
    fields byte-equal).
  - **NFR-003**: 100 % audit coverage — randomized 10k-evaluation
    burst; event count exactly matches evaluation count.
  - **NFR-005**: fail-closed completeness — every clause kind tested
    with a "input source unavailable" injection; default behavior is
    Deny + `policy_unavailable_fail_closed`.
  - **NFR-006**: catalog parity — a static check that every
    registered kind has at least one consumer adapter and that each
    consumer adapter's exposed Action kinds map to a registered
    control kind. Implement as a `TestControlCatalogParity` test.
  - **NFR-007**: a hot-reload latency test asserts policy update
    propagation under 60 s.
  - Plan §8 R8 mitigation: a CI lint (e.g., a small Go program under
    `tools/policy-lint/`) walks consumer packages and flags any
    edge that takes a policy-relevant action without a preceding
    `policy.Engine.Evaluate` call. Document scope and known
    limitations.

## Acceptance criteria

- FR-011 pre-flight refuses startup when a loaded subsystem violates
  the active policy; the structured error names every violation.
- NFR-007: a policy update applied via the bundle resolver propagates
  to evaluation within 60 s without restart; in-flight evaluations
  remain on their pinned snapshot.
- FR-016 validity windows + skew: a future-dated policy beyond skew
  tolerance is inactive; within tolerance, it activates; an expired
  policy is inactive and the prior policy resumes.
- Spec edge case "policy is removed": with no prior policy, the
  engine falls to fail-closed and emits the matching event.
- All NFR defense pack tests pass on CI hardware.
- Catalog-parity static check passes.
- Plan R8 lint identifies a deliberately-missing Evaluate edge in a
  test fixture and exits non-zero.
- All seven User Story acceptance scenarios pass end-to-end.

## Files to create/modify

- Create `core/policy/preflight/{preflight.go, violations.go}` +
  tests.
- Create `core/policy/engine/snapshot.go` (atomic.Pointer +
  drain-aware swap) and `snapshot_test.go`.
- Create `core/policy/engine/validity.go` for time-window logic +
  tests.
- Create `core/policy/integration_test.go` (one acceptance suite per
  User Story; uses real on-disk event log under `t.TempDir()`).
- Create `core/policy/nfr_test.go` with the NFR defense pack
  benchmarks + tests.
- Create `tools/policy-lint/main.go` for the consumer-edge audit and
  wire it into CI.
- Modify the harness boot path to invoke `preflight.Run()` before
  sessions start; refuse startup on violations with a clear error.

## Definition of done

- Acceptance criteria pass.
- Charter quality gates clean (`gofmt`, `goimports`, `go vet`,
  `golangci-lint run`, `vue-tsc` if frontend touched — likely not).
- `go test ./... -race` clean across the whole project.
- All SC-001..SC-007 measurable outcomes have a test or PR-body
  artifact demonstrating they pass.
- Cross-mission dependencies confirmed end-to-end:
  `bundle-format-resolver-01KQ1A3J`, `a2a-signed-cards-trust-01KQ18P9`,
  `event-log-01KQ1A3M`, `llm-connector-01KQ1770`, `core/mcp`,
  `core/a2a`, `core/scheduler`, `core/workflow`, `core/bundle`.
- Conventional-commit message; commit attributed per DIRECTIVE_029.
- ADRs landed for any open questions (OQ1 raw-Rego escape hatch
  decision, OQ2 currency model in WP10, OQ3 network-tier scope in
  WP09) — confirm they exist before merge.
