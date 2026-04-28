---
work_package_id: "WP10"
title: "Integrity verification (SHA-256 mandatory) and signature verifier facade"
dependencies:
  - "WP03"
  - "WP09"
planning_base_branch: "main"
merge_target_branch: "main"
branch_strategy: "Planning artifacts were generated on main; completed changes must merge back into main."
subtasks:
  - "T001"
  - "T002"
  - "T003"
  - "T004"
phase: "Phase 10 - Integrity"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP10 – Integrity verification and signature verifier facade

## Goal

Wire the fetch pipeline together: cache lookup → channel fetch → SHA-256 verification → optional signature verification (delegated to `a2a-signed-cards-trust`'s `trust.Verifier` API) → atomic CAS write. SHA-256 is mandatory for every activated artifact; signature verification is optional in v1 but enforced when policy says so. This WP closes the integrity loop.

## Spec references

- FR-006 Content-hash integrity verification
- FR-007 Optional signature verification
- NFR-003 Integrity-verification completeness (100%)
- NFR-004 Path-traversal containment
- US3 (P1) cryptographic verifiability — three acceptance scenarios.
- C-005 SOC 2 readiness (verification is auditable evidence)

## Plan references

- Plan §2 `core/bundle/integrity/` (hash + signature facade)
- Plan §4.3 Fetch pipeline (hash verify before CAS write; never write before verification)
- Plan §6.3 a2a-signed-cards-trust integration (delegated, never reimplemented)
- Plan §6.5 sigstore-go integration shape (lives behind trust.Verifier; v1.x for sigstore, v1.0 supports the facade)
- Plan §7 v1 scope: optional sig verification; v1.x: sigstore + Ed25519 detached
- Plan §8 R4 (lockfile-pin authoritative; `harness bundle upgrade` required)
- Research D4, D5

## Cross-mission dependencies

- **a2a-signed-cards-trust**: provides `trust.Verifier` interface and concrete sigstore + Ed25519 implementations. Until that mission lands, the facade accepts a stub verifier with a clear "not configured" path; operator policy `signatures: required` returns `ErrSignatureRequired` until the verifier is wired.

## Subtasks

- T001 Implement `integrity/hash.go`: streaming SHA-256 over an `io.Reader` with bounded memory; optional alongside-BLAKE3 hook for `additionalHashes` (D5; v2 scope but interface ready).
- T002 Implement `integrity/signature.go`: facade calling `trust.Verifier.Verify(ctx, trust.VerifyRequest{Payload, Signature, Anchors})`. The bundle layer offers bytes + `SignatureRef`; trust mission owns algorithm policy, anchor matching, key rotation, revocation.
- T003 Wire the fetch pipeline in `core/bundle/resolver/fetch.go`: for each artifact in the plan: cache lookup; on miss call channel; verify hash against lockfile pin; on mismatch emit `artifact_rejected` and return `ErrIntegrityMismatch` (CAS write never happens); on match, optionally verify signature; on success, CAS atomic write; emit `artifact_fetched`, `artifact_verified`, `artifact_activated` events.
- T004 Implement `SigningPolicy` option (`required` / `optional` / `forbidden`); `required` + missing signature returns `ErrSignatureRequired` before fetching.
- T005 Bounded-parallelism fan-out: configurable, default `runtime.NumCPU()`; each artifact independent; failure of one does not poison others until the consolidation step.
- T006 Refuse to upgrade a pinned bundle without explicit operator action — Plan R4: a new published version with the same name as a pinned bundle is ignored; only `harness bundle upgrade <name>` (WP12) advances pins.

## Acceptance criteria

- 100% of activated artifacts have their content hash verified against the lockfile (NFR-003).
- A modified-byte artifact returns `ErrIntegrityMismatch` and is never written to the canonical CAS path.
- `SigningPolicy: required` with an unsigned bundle returns `ErrSignatureRequired` before any fetch.
- A signature failure returns `ErrSignatureInvalid` with the bundle ref and verifier reason.
- An attacker republishing under the same name with a different content does not displace the pinned hash.
- Fetch pipeline runs with bounded parallelism; one failure does not corrupt others' results.

## Files to create/modify

- `core/bundle/integrity/hash.go` (new — SHA-256 streaming hash)
- `core/bundle/integrity/signature.go` (new — verifier facade)
- `core/bundle/resolver/fetch.go` (new — fetch pipeline orchestrator)
- `core/bundle/errors.go` (extend — `ErrIntegrityMismatch` (already), `ErrSignatureInvalid`, `ErrSignatureRequired`)

## Definition of done

- All acceptance criteria pass.
- Integration test: a tampered fixture artifact triggers `ErrIntegrityMismatch` and never lands in CAS.
- `integrity/` package depends only on stdlib hash plus the `trust.Verifier` interface from a2a-signed-cards-trust; no sigstore-go import in core/bundle.
- Plan §7 v1 scope satisfied; v1.x sigstore wiring is a follow-up mission.
- Plan R4 mitigation: integration test confirms a name-squat attempt against a pinned lockfile is ignored.
