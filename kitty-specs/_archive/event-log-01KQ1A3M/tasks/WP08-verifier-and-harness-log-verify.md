---
work_package_id: "WP08"
title: "Verifier and harness log verify CLI surface"
dependencies:
  - "WP01"
  - "WP05"
  - "WP07"
planning_base_branch: "main"
merge_target_branch: "main"
branch_strategy: "Planning artifacts were generated on main; completed changes must merge back into main."
subtasks:
  - "T001"
  - "T002"
  - "T003"
  - "T004"
  - "T005"
phase: "Phase 7 - Verifier"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP08 – Verifier and harness log verify CLI surface

## Goal

Implement the `Verifier` interface that walks per-session chains
end-to-end, recomputes payload hashes from canonical bytes, validates
`prev_hash` linkage, and reports tamper points and truncation points
cleanly. Wire it to a `harness log verify` CLI surface so operators can
exercise it without writing Go (FR-011). Verification on a freshly
restored / truncated log reports the truncation point cleanly without
aborting (spec edge case).

## Spec references

- FR-004 — Hash-chain integrity.
- FR-011 — `harness log verify` operation.
- NFR-005 — 100 % single-byte tamper detection.
- C-005 — SOC 2-readiness evidence.

## Plan references

- §3 Public API — `Verifier.VerifySession`, `VerifyAll`.
- §4.3 Verify path — chain walk; recompute `payload_hash`; check
  `prev_hash` link; report ok span, first tamper id, last verified id,
  truncation point.
- Risk R2 — `chain.rebased` marker handling for legitimate migrations.

## Subtasks

- T001 — Implement `core/event/chain/verify.go`: walks events in ULID
  order for a session; recomputes `payload_hash` from canonical bytes
  (WP05); checks `prev_hash` link; emits `Report` (ok span, first
  tamper id, last verified id, truncation point).
- T002 — Implement `Verifier` constructor + `VerifySession(sid)` and
  `VerifyAll()`; `VerifyAll` walks every distinct `session_id`.
- T003 — Truncation handling: if the cached chain head points to an id
  not present in `events`, report it as truncation point (not error).
  If a `chain.rebased` self-event is encountered, restart link
  validation from that point per plan Risk R2.
- T004 — CLI surface `harness log verify`: invokes `Verifier`; prints
  human-readable report; exits non-zero on tamper detection. Lives in
  the harness CLI binary, not `core/event/` (C-001 boundary —
  `core/event/` exposes only the Go API).
- T005 — Property-based tests: tamper a random byte at a random offset
  in a random event row in a random session; assert detection 100 %
  of the time across 1K trials (NFR-005). Truncation tests:
  intentionally truncate the tail; assert clean truncation report.

## Acceptance criteria

- 100 % single-byte tamper detection in property tests over 1K trials
  (NFR-005).
- Truncation point reported cleanly without aborting verification of
  earlier events.
- `harness log verify` exits zero on a clean log, non-zero with
  human-readable diagnostic on a tampered log.
- `go test ./core/event/...` green; `-race` clean.
- ADR cross-reference: per-session chain decision (WP03 ADR) is the
  basis here.

## Files to create / modify

- `core/event/chain/verify.go`
- `core/event/chain/verify_test.go`
- `core/event/verifier.go`
- `core/event/verifier_integration_test.go`
- `cmd/harness/log_verify.go` (CLI subcommand)
- `cmd/harness/log_verify_test.go`

## Definition of done

- All subtasks complete; property gate for NFR-005 met.
- `harness log verify` works end-to-end against a temp-dir DB.
- `go vet`, `golangci-lint run` clean.
