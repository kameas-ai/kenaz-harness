---
work_package_id: "WP11"
title: "AWS profile and AWS KMS backends (build-tag opt-in)"
dependencies:
  - "WP07"
planning_base_branch: "main"
merge_target_branch: "main"
branch_strategy: "Planning artifacts were generated on main; completed changes must merge back into main."
subtasks:
  - "T001: Implement AWS profile backend resolving via standard credential chain"
  - "T002: Implement AWS KMS backend with envelope encryption (AWS Encryption SDK for Go)"
  - "T003: Build-tag opt-in to keep aws-sdk-go-v2 out of the default binary"
  - "T004: Cache rotation invalidation hook for KMS data-key rotation"
  - "T005: Map SDK errors to FR-014 typed errors"
  - "T006: Black-box integration tests against LocalStack KMS in CI"
  - "T007: Retry-with-backoff on transient KMS unavailability; fail closed (R4)"
phase: "Phase 11 - AWS Backends"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP11 – AWS profile and AWS KMS backends (build-tag opt-in)

## Goal

Ship the AWS profile backend (`RefAWSProfile`) resolving through the standard AWS credential chain, and the AWS KMS backend (`RefAWSKMS`) using `aws-sdk-go-v2/service/kms` plus the AWS Encryption SDK for Go for envelope encryption with caching and commitment. Both backends are build-tag opt-in so default binaries stay free of the AWS SDK surface.

## Spec references

- FR-006 (AWS profile backend): resolves through the standard AWS credential chain.
- FR-007 (Cloud KMS backend, optional): opt-in KMS backend returning short-lived credentials.
- FR-014 (Error taxonomy): map SDK errors to `ErrBackendUnavailable`, `ErrReferenceNotFound`, `ErrPermissionDenied`.
- C-004 (Charter local-first): network-backed backends are opt-in.
- C-006 (Fail-closed): backend unavailability surfaces as a typed error.
- Edge case: "credential backend is temporarily unreachable (KMS API outage): retry per a configurable budget, then fail closed".

## Plan references

- §2 Architectural placement → `core/secrets/backends/awsprofile/` and `core/secrets/backends/awskms/`.
- §7 Phasing → v1.0 ships `awsprofile` and `awskms` (AWS Encryption SDK envelope).
- §8 Risk register → R4 (KMS network unavailability).
- §12 Acceptance mapping → FR-006, FR-007 map here.
- Research D4 → AWS-only for v1; Azure / GCP deferred.

## Subtasks

- Implement AWS profile backend at `core/secrets/backends/awsprofile/awsprofile.go` resolving via `aws-sdk-go-v2/config.LoadDefaultConfig`. Returns access key + secret as a Secret (caller decides framing).
- Implement AWS KMS backend at `core/secrets/backends/awskms/awskms.go` using `aws-sdk-go-v2/service/kms` and the AWS Encryption SDK for Go for envelope encryption with caching commitment.
- Use Go build tags (`//go:build awsbackend`) so default builds exclude AWS SDK; document opt-in flag in the package doc and project README.
- Wire a rotation invalidation hook: when a KMS data-key rotation is detected (via callback or polling DescribeKey), call into WP05's cache invalidation (per FR-011, NFR-007).
- Map SDK errors to FR-014 sentinel errors: throttling and network errors → `ErrBackendUnavailable`; missing key → `ErrReferenceNotFound`; access denied → `ErrPermissionDenied`.
- Implement retry-with-backoff: configurable retry budget on transient errors (per spec edge case); after budget exhaustion, fail closed with `ErrBackendUnavailable` (C-006, R4).
- Black-box integration tests using LocalStack KMS in CI. Mark tests with the `awsbackend` build tag.

## Acceptance criteria

- Both backends compile and register only when the `awsbackend` build tag is enabled.
- Default-build binary contains no AWS SDK symbols (verified via a build-tag exclusion test).
- AWS profile backend resolves valid profiles via the standard credential chain (FR-006).
- KMS backend resolves and caches data keys via envelope encryption (FR-007).
- Transient KMS unavailability triggers retry-with-backoff, then fails closed with `ErrBackendUnavailable` (C-006, R4).
- LocalStack-backed integration tests pass.
- Rotation invalidation hook flushes the cache and re-resolves on next call.
- Charter quality gates pass.

## Files to create / modify

- Create `core/secrets/backends/awsprofile/awsprofile.go` (build-tagged).
- Create `core/secrets/backends/awsprofile/awsprofile_test.go`.
- Create `core/secrets/backends/awskms/awskms.go` (build-tagged).
- Create `core/secrets/backends/awskms/awskms_test.go`.
- Update `go.mod` / `go.sum` to add `aws-sdk-go-v2`, `aws-sdk-go-v2/service/kms`, and the AWS Encryption SDK for Go (under build tag).

## Definition of done

- FR-006, FR-007 acceptance scenarios traceable to tests in this WP.
- Resolver routes `aws_profile:` and `aws_kms:` references through these backends after registration.
- Architectural integrity preserved: only these packages import the AWS SDK (C-001).
- Build-tag opt-in keeps default binary slim (C-004 local-first posture).
- Risk R4 mitigated with retry-with-backoff + fail-closed.
- Handoff: rotation invalidation API exercised end-to-end (NFR-007 supporting evidence).
