---
work_package_id: "WP11"
title: "YubiKey and PKCS#11 backend interface stubs (v1.x and v2 hooks)"
dependencies:
  - "WP02"
planning_base_branch: "main"
merge_target_branch: "main"
branch_strategy: "Planning artifacts were generated on main; completed changes must merge back into main."
subtasks:
  - "T001"
  - "T002"
  - "T003"
phase: "Phase 9 - HSM backend stubs"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP11 – YubiKey and PKCS#11 backend interface stubs

## Goal

Land the `yubikey` and `pkcs11` backend packages as interface-conformant stubs at v1.0, behind build tags, so the SC-007 "stable contract" claim is testable now and the v1.x / v2 implementations are an additive change rather than a refactor. `yubikey` uses `go-piv/piv-go` v2.6.0 (per secrets-keychain D5); `pkcs11` is interface-only at v1.0 (impl in v2 per plan §1.4).

## Spec references

- FR-008 (signing backend abstraction), FR-010 (HSM optional), NFR-006 (algorithm agility), NFR-007 (fail-closed)
- C-006 (OSS / enterprise split — same contract across editions)
- SC-007 (new backend addable without changes outside its package)
- Plan §1.4 (yubikey "interface-conformant stub at v1.0"; pkcs11 "interface only at v1.0").

## Plan references

- §6.2 (`yubikey` → `go-piv/piv-go` v2.6.0; `pkcs11` → `miekg/pkcs11`).
- §7 phasing: full YubiKey v1.x; PKCS#11 v2.

## Subtasks

- **T001** — Create `core/trust/backends/yubikey/yubikey.go` with `//go:build yubikey` tag. Implement `SigningBackend` returning `ErrBackendNotImplemented` from `Sign` and `PublicKey` (so v1.0 clients get a clear error rather than a panic), but real `Kind`, `SupportedAlgorithms`, and `Health` (probe `piv-go.Cards()`). No-tag stub `stub.go` registers a sentinel returning `ErrBackendNotAvailable`.
- **T002** — Create `core/trust/backends/pkcs11/pkcs11.go` with `//go:build pkcs11` tag — interface conformance only; every method returns `ErrBackendNotImplemented`. No-tag stub returns `ErrBackendNotAvailable`. `doc.go` documents the v2 implementation plan and the `miekg/pkcs11` library decision.
- **T003** — Add a contract conformance test (under `core/trust/backends/contract_test.go`) parametrized over every backend in the registry that confirms each backend's `Kind()` and `SupportedAlgorithms()` are populated and `Health()` returns a typed `HealthStatus`. This is the SC-007 stability check — adding a new backend should require only its own package to satisfy this test.

## Acceptance criteria

- Tag-off OSS build: `go list -deps ./...` shows neither `go-piv/piv-go` nor `miekg/pkcs11` in the binary (R-008 mitigation extended).
- Tag-on builds compile and the stubs return typed `ErrBackendNotImplemented` from `Sign` / `PublicKey` with documentation pointing operators to the v1.x / v2 milestone where the implementation lands.
- Contract conformance test runs against every registered backend (software, oskeychain, awskms, yubikey, pkcs11) and asserts the contract holds — SC-007 evidence.
- ADR not required at this WP (no material decision; libraries are inherited from secrets-keychain mission).

## Files to create/modify

- Create: `core/trust/backends/yubikey/yubikey.go` (tag), `core/trust/backends/yubikey/stub.go` (no-tag), `core/trust/backends/yubikey/doc.go`
- Create: `core/trust/backends/pkcs11/pkcs11.go` (tag), `core/trust/backends/pkcs11/stub.go` (no-tag), `core/trust/backends/pkcs11/doc.go`
- Modify: `go.mod` — add `github.com/go-piv/piv-go/v2` and `github.com/miekg/pkcs11` (only paths that import them)
- Tests: `core/trust/backends/contract_test.go`, `core/trust/backends/yubikey/stub_test.go`, `core/trust/backends/pkcs11/stub_test.go`

## Definition of done

- All three subtasks complete.
- CI matrix builds with `yubikey`, `pkcs11`, `kms_aws` tags off and on (combinations) and stays green.
- Contract conformance test enumerates every backend and asserts contract compliance.
- Tag-off binary unchanged in dependency tree (R-008 mitigation evidence in PR description).
- Inline doc comments specify the milestone (v1.x for YubiKey, v2 for PKCS#11) where stubs become real implementations.
