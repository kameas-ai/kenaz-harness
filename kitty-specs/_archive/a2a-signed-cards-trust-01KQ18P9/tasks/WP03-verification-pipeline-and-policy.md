---
work_package_id: "WP03"
title: "Verification pipeline (10-step fail-fast) and algorithm/clock-skew policy"
dependencies:
  - "WP01"
  - "WP02"
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
phase: "Phase 2 - Verification pipeline"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP03 – Verification pipeline and algorithm/clock-skew policy

## Goal

Implement `core/trust/verify.go` as the canonical 10-step fail-fast cheap-first verification pipeline from plan §4.2, and `core/trust/policy.go` for algorithm allow-list (FR-004) and clock-skew tolerance (FR-016). Pipeline calls into anchor store, revocation cache, and rotation manager via interfaces (real implementations land in WP05/WP06/WP07); WP03 ships in-memory fakes for those collaborators so the pipeline can be exercised end-to-end before the persistent stores exist.

## Spec references

- FR-002 (verify inbound), FR-004 (algorithm policy), FR-012 (uniform verification API), FR-016 (clock-skew tolerance), FR-017 (rejection taxonomy)
- NFR-001 (verification latency < 5 ms p95)
- C-005 (SOC 2 — every verification yields a typed result)
- Plan §4.2 (10-step pipeline order), §5.3 (envelope fields used by clock check)

## Plan references

- §4.2 verification pipeline order: envelope shape → algorithm policy → chain depth → anchor lookup → revocation → clock skew → signature math → rotation overlap → identity collision → audit emit → return.

## Subtasks

- **T001** — Implement `core/trust/policy.go`: `AlgorithmPolicy` (allow-list, default Ed25519-only per Open Question 2), `ClockSkewPolicy` (configurable tolerance per FR-016), `ChainDepthPolicy` (max chain depth for the edge-case defense).
- **T002** — Implement `core/trust/verify.go` step 1 (envelope shape) and step 2 (algorithm policy gate) — return `RejSignatureInvalid` and `RejAlgorithmNotPermit` respectively.
- **T003** — Implement steps 3–7: chain-depth (`RejChainDepthExceeded`), anchor lookup (`RejAnchorMissing` / `RejAnchorRemoved` distinct codes per spec edge case), revocation cache (`RejKeyRevoked`), clock-skew window (`RejClockSkewExceeded`), signature math via `internal/algo` (`RejSignatureInvalid`).
- **T004** — Implement steps 8–9: rotation overlap detection (sets `CacheState=grace` when verifying against `previous_key` within `overlap_ends`), identity-collision check (`RejIdentityCollision` when a different fingerprint is bound to the same `agent_id`).
- **T005** — Implement step 10 (audit emit hook — placeholder interface satisfied later by WP05) and step 11 (return `VerificationResult`); guarantee exactly-one VerificationResult ↔ exactly-one audit emission (NFR-005, C-005).
- **T006** — Add a black-box table-driven test suite (`verify_test.go`) covering every rejection code, the grace-period accept path, the happy accept path, and a benchmark asserting non-math overhead < 5 ms p95 on a developer laptop fixture (NFR-001).

## Acceptance criteria

- Pipeline order matches plan §4.2 exactly; deviations flagged with inline comments + ADR reference.
- Every `RejectionCode` in FR-017 is reachable via at least one test case (DIRECTIVE_036 — driven by external inputs only).
- `VerificationResult` always has exactly one of (`Decision=accepted` ∧ `AnchorID` set) or (`Decision=rejected` ∧ `RejectionCode` set); a contract test enforces this invariant.
- Clock-skew policy honors FR-016: rejections beyond tolerance use `RejClockSkewExceeded` rather than generic `RejSignatureInvalid`.
- The `anchor_missing` vs `anchor_removed` distinction is preserved (the anchor store fake records "tombstones" so removed anchors return the correct code).
- Benchmark verifies NFR-001 budget; pre-existing failures from absent collaborators are not introduced.

## Files to create/modify

- Create: `core/trust/policy.go`
- Modify: `core/trust/verify.go` (was a stub from WP01)
- Create test fakes (in-package): `core/trust/anchor_fake_test.go`, `core/trust/revocation_fake_test.go`
- Tests: `core/trust/verify_test.go`, `core/trust/policy_test.go`, `core/trust/verify_bench_test.go`

## Definition of done

- All six subtasks complete; lint and vet clean.
- ≥ 80% line coverage on `verify.go` and `policy.go`.
- `go test ./core/trust/... -race` passes.
- Benchmark output recorded in PR description for NFR-001 evidence.
- A separate consumer-package compile test (DIRECTIVE_036) calls `engine.Verify` and inspects only the public `VerificationResult` shape.
