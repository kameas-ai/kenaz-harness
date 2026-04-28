---
work_package_id: "WP10"
title: "AWS KMS signing backend behind kms_aws build tag (OSS opt-in)"
dependencies:
  - "WP02"
  - "WP05"
planning_base_branch: "main"
merge_target_branch: "main"
branch_strategy: "Planning artifacts were generated on main; completed changes must merge back into main."
subtasks:
  - "T001"
  - "T002"
  - "T003"
  - "T004"
phase: "Phase 9 - AWS KMS backend"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP10 – AWS KMS signing backend (kms_aws build tag, OSS opt-in)

## Goal

Implement the `awskms` `SigningBackend` using `aws-sdk-go-v2/service/kms` (per secrets-keychain D4), gated behind the `kms_aws` build tag. Ships in OSS (Open Question 3 default — licensing/support split, not feature fork) so enterprise operators with KMS requirements do not have to fork `core/trust/`. Private keys never transit the harness process: every sign goes through the KMS API.

## Spec references

- FR-008, FR-010 (cloud KMS / HSM optional backend)
- NFR-002 (signing latency bounded by backend SLA — surfaced to operator), NFR-007 (fail-closed on backend unavailability)
- C-006 (OSS / enterprise distribution split — same contract across editions)
- SC-005 (zero plaintext private keys on disk — KMS keys never leave AWS managed boundary)
- Plan §6.2 (`aws-sdk-go-v2/service/kms` per secrets-keychain D4), §7 v1.0 phasing.

## Plan references

- §1.4 (`awskms` "implemented behind `kms_aws` build tag" at v1.0).
- §6.2 backend table.
- §8 R-008 mitigation: backend SDKs gated by build tag; CI matrix verifies binary size and dependency tree with tag off.

## Subtasks

- **T001** — Create `core/trust/backends/awskms/` subpackage with `//go:build kms_aws` tag. Implement `SigningBackend`: `BackendRef.Path` carries the KMS key ARN; `Sign` calls `kms.Sign` with the appropriate `SigningAlgorithmSpec`; `PublicKey` calls `kms.GetPublicKey`; `SupportedAlgorithms` returns the algorithms the keyspec supports.
- **T002** — Add a no-tag stub `core/trust/backends/awskms/stub.go` that registers a sentinel returning `ErrBackendNotAvailable` so the OSS binary built without the tag explicitly rejects the backend rather than silently missing it.
- **T003** — Implement `Health(ctx)`: cheap probe via `kms.DescribeKey` on a configured ARN with a short context deadline; degrade to `unavailable` on error so dispatcher fails closed (NFR-007). Cache health for a configurable TTL to avoid hot-path KMS calls.
- **T004** — Add black-box integration tests using `kms-mock` or a recorded HTTP fixture (no live AWS call in CI). Verify: tag-on roundtrip sign/verify; tag-off binary returns `ErrBackendNotAvailable`; backend unavailability fails closed (does not fall back to a different backend); CI dependency-tree check passes (no `aws-sdk-go-v2` in tag-off binary).

## Acceptance criteria

- Tag-off OSS build: `go build ./...` produces a binary with no `aws-sdk-go-v2` symbols (verified via `go list -deps` or `go tool nm`).
- Tag-on build: full sign/verify roundtrip works against a mock KMS endpoint.
- Same `SigningBackend` contract as `oskeychain` and `software` — no enterprise-only API surface in `core/trust/` (C-006).
- `Health` is cheap enough to run on every Sign call without violating NFR-002 budget; cached with explicit TTL.
- ADR `adr-trust-002-oss-vs-enterprise-backend-split.md` drafted under `docs/adr/` recording Open Question 3 resolution per DIRECTIVE_003.
- ≥ 80% coverage on `awskms` package under `kms_aws` tag.

## Files to create/modify

- Create: `core/trust/backends/awskms/awskms.go` (build-tagged), `core/trust/backends/awskms/stub.go` (no-tag), `core/trust/backends/awskms/doc.go`
- Modify: `go.mod` — add `aws-sdk-go-v2/service/kms` (only path that imports it)
- Tests: `core/trust/backends/awskms/awskms_test.go` (tag-on, mock KMS), `core/trust/backends/awskms/stub_test.go` (tag-off)
- Create: `docs/adr/adr-trust-002-oss-vs-enterprise-backend-split.md`
- Modify: CI workflow — add tag-on and tag-off matrix entries (charter quality gate `golangci-lint run` clean for both)

## Definition of done

- All four subtasks complete.
- Both tag-on and tag-off CI matrices green.
- Binary-size diff documented in PR description (R-008 evidence — tag-off binary size unchanged within a small budget).
- ADR `adr-trust-002` drafted and linked.
- KMS API never sees private bytes leaving AWS managed boundary (SC-005 evidence — integration test scans process memory and disk for the test key bytes after a sign call and finds none).
