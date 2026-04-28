---
work_package_id: "WP12"
title: "End-to-end integration tests, audit suite, and resolution latency bench"
dependencies:
  - "WP02"
  - "WP04"
  - "WP05"
  - "WP07"
  - "WP08"
  - "WP09"
  - "WP10"
  - "WP11"
planning_base_branch: "main"
merge_target_branch: "main"
branch_strategy: "Planning artifacts were generated on main; completed changes must merge back into main."
subtasks:
  - "T001"
  - "T002"
  - "T003"
  - "T004"
  - "T005"
phase: "Phase 10 - Integration + acceptance"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP12 – End-to-end integration, audit suite, latency bench

## Goal

Tie the mission together with end-to-end black-box integration tests against a local OCI registry, an audit suite that proves the security invariants, and a benchmark harness that validates NFR-001 (≤100 ms p95 warm-cache resolution). This WP is the acceptance gate for the mission.

## Spec references

- NFR-001 (Resolution latency under 100 ms p95 warm cache)
- NFR-003 (100 % verification coverage at injection)
- NFR-005 (Zero scoped-pack bytes leak across role boundary)
- NFR-006 (100 % audit reconstructability)
- SC-001 (Operator follows org pack, applies, replays from event log)
- SC-002 (Override precedence 100 % across matrix)
- SC-003 (Broken provenance rejected 100 %)
- SC-005 (30-day-later replay byte-identical)
- SC-006 (Zero bytes leak from role-scoped pack)
- SC-007 (Every pass + injection produces sufficient event entries)
- SC-008 (New channel kind requires zero `core/context/` changes)

## Plan references

- §11 (Acceptance signals)
- §6 (Component view; integration matrix across bundle, trust, event, secret, storage)
- Risk R7 (audit reconstruction byte-comparison)
- Risk R5 (fail-closed integration scenario)

## Subtasks

- T001 End-to-end test (org-only, org+team, org+team+personal): signed packs published to a local OCI registry, lockfile-pinned, resolved end-to-end through `core/bundle/`, merged, injected; verify session output references correct entries per US1/US2/US3 acceptance scenarios.
- T002 Audit suite: assert zero plaintext credential bytes in the event log across the full layered scenario; zero role-scoped pack bytes leaking to an out-of-role operator (NFR-005, SC-006); 100 % snapshot reconstructability from event log alone (NFR-006, SC-007).
- T003 Replay determinism test (SC-005): record a session, mutate the channel head three times (simulate 30 days of churn), invoke replay, byte-compare resolved context against the original.
- T004 Channel-extensibility test (SC-008): add a stub channel kind in `core/bundle/` test fixtures, host a `context-pack`, verify resolution works with zero edits in `core/context/`.
- T005 Bench harness (NFR-001): warm-cache resolution of org+team+personal under 100 ms p95 on a developer laptop, recorded in CI bench output.

## Acceptance criteria

- All US1–US8 acceptance scenarios from the spec exercised by a corresponding end-to-end test.
- Audit suite passes with zero leakage on credential bytes and role-scoped bytes.
- Replay byte-identity verified against a 30-day-equivalent churn scenario.
- Bench harness reports p95 < 100 ms for warm-cache resolution.
- Channel-extensibility test confirms no `core/context/` diff is required to add a new channel kind.

## Files to create/modify

- `core/context/integration_test.go` (end-to-end)
- `core/context/audit_suite_test.go` (security invariants)
- `core/context/replay_determinism_test.go`
- `core/context/bench_test.go`
- `core/context/testdata/...` (signed packs, fixtures, OCI registry seed)

## Definition of done

- All tests pass under `go test ./core/context/... -race`.
- Bench numbers recorded in CI; regression budget defended per charter §Performance.
- All success criteria SC-001..SC-008 mapped to a passing test.
- WP merged to main via squash-merge PR; mission ready for `spec-kitty merge`.
