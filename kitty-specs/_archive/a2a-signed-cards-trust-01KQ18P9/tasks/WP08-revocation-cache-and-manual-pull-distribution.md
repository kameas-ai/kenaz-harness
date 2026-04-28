---
work_package_id: "WP08"
title: "Revocation ingestion, cache, and manual pull-based distribution"
dependencies:
  - "WP06"
planning_base_branch: "main"
merge_target_branch: "main"
branch_strategy: "Planning artifacts were generated on main; completed changes must merge back into main."
subtasks:
  - "T001"
  - "T002"
  - "T003"
  - "T004"
  - "T005"
phase: "Phase 7 - Revocation"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP08 – Revocation ingestion, cache, and manual pull distribution

## Goal

Implement `core/trust/revocation.go` providing `IngestRevocation`, the in-memory cache backed by `trust_revocations`, and a manual pull-based distributor (Open Question 1 default — operator-configured URL, 60-second poll, configurable). Acceptance scenarios from User Story 4 must pass: revoked keys are rejected with `RejKeyRevoked`; revocations apply to the *identity*, not just future-dated signatures.

## Spec references

- FR-006 (revocation ingestion), FR-007 (revocation distribution placeholder)
- NFR-003 (revocation propagation < 5 min p95)
- SC-004 (5-minute propagation across connected peers)
- User Story 4 acceptance scenarios
- FR-017 (`RejKeyRevoked`), FR-011 (audit)

## Plan references

- §4.5 (revocation cache): in-memory map keyed by `(subject_kind, subject_id)` with `effective_at`, backed by `trust_revocations`. v1.0 ingestion path: operator calls `IngestRevocation` (CLI, RPC, or scheduled pull from configured URL). Default poll 60 s.
- §5.1 `trust_revocations` table schema.
- §5.2 audit kind `trust/revocation-ingested`.
- §7 (v1.0 phasing — manual pull only; automatic distribution deferred to v1.x).
- §9 Open Question 1 default: manual pull, 60-second poll, operator-configurable.

## Subtasks

- **T001** — Implement `core/trust/revocation.go` `IngestRevocation(ctx, rec)`: validate the record's signature against an authorized issuer anchor (`rec.IssuedBy`), persist to `trust_revocations`, update the in-memory cache, emit `trust/revocation-ingested` audit. Idempotent on `revocation_id`.
- **T002** — Implement the cache: `Lookup(subjectKind, subjectID, asOf time.Time) (*RevocationRecord, bool)`. Acceptance rule per plan §4.5: a record applies to the identity (not just future-dated signatures), so a card issued before revocation but presented after still rejects (User Story 4 scenario 2).
- **T003** — Wire revocation into `verify.go` step 5 (from WP03): on cache hit return `RejKeyRevoked`; check both `subject_kind=key_id` (fingerprint) and `subject_kind=identity` (`agent_id`).
- **T004** — Implement the manual pull distributor `core/trust/revocation_pull.go`: operator-configured URL, configurable poll interval (default 60 s, per Open Question 1). Each poll call ingests every record not already seen; failures back off and emit a `trust/preflight-finding` warning rather than crashing the harness. Pull is a goroutine started by `core/config/` at harness startup; tickers honored by `ctx.Done()` for graceful shutdown.
- **T005** — Add black-box tests for User Story 4 scenarios plus: (a) bad signature on RevocationRecord is rejected at ingest; (b) duplicate `revocation_id` is idempotent; (c) pull URL unreachable surfaces a warning, does not crash the harness; (d) NFR-003 / SC-004 propagation budget — simulated peer set sees the revocation within 5 minutes p95 with the default 60 s poll.

## Acceptance criteria

- Revocation precedence: revoked keys reject before signature math runs (plan §4.2 step 5 — cheap-first ordering preserved).
- Revocations apply to identity (User Story 4 scenario 2): a fixture-issued card pre-revocation, presented post-revocation, rejects.
- Manual pull distributor honors `ctx.Done()` and never blocks shutdown.
- ADR `adr-trust-003-revocation-distribution-v1` is drafted under `docs/adr/` recording the Open Question 1 resolution (per DIRECTIVE_003).
- Audit emits exactly one `trust/revocation-ingested` per successful ingest; failures emit `trust/preflight-finding` with `code=revocation_endpoint_unreachable`.
- ≥ 80% coverage on `revocation.go` and `revocation_pull.go`.

## Files to create/modify

- Modify: `core/trust/revocation.go` (was a stub from WP01)
- Create: `core/trust/revocation_pull.go`
- Modify: `core/trust/verify.go` step 5 (real revocation lookup)
- Modify: `core/trust/config.go` (start the pull goroutine if configured)
- Tests: `core/trust/revocation_test.go`, `core/trust/revocation_pull_test.go`, `core/trust/revocation_integration_test.go`
- Create: `docs/adr/adr-trust-003-revocation-distribution-v1.md`

## Definition of done

- All five subtasks complete.
- ADR drafted and linked from PR description (DIRECTIVE_003).
- SC-004 propagation evidence in PR description (5 min p95).
- Test fixtures cover both `key_id` and `identity` revocation subjects.
- Charter testing: integration tests use real on-disk event log.
