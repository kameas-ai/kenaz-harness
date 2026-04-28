---
work_package_id: "WP02"
title: "Signing backend contract, algorithm registry, and dispatcher"
dependencies:
  - "WP01"
planning_base_branch: "main"
merge_target_branch: "main"
branch_strategy: "Planning artifacts were generated on main; completed changes must merge back into main."
subtasks:
  - "T001"
  - "T002"
  - "T003"
  - "T004"
  - "T005"
phase: "Phase 1 - Core skeleton"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP02 – Signing backend contract, algorithm registry, and dispatcher

## Goal

Define the `SigningBackend` interface, the backend registry, and the internal algorithm registry (Ed25519 implemented; ECDSA-P256 and RSA-PSS interface-conformant slots). Implement `core/trust/sign.go` as a dispatcher that resolves `IdentityRef.Backend` to a registered backend, calls `Health`, and fails closed (NFR-007) if unavailable. No backend implementation yet — that is WP03/WP04/WP09.

## Spec references

- FR-001, FR-004, FR-008, FR-010
- NFR-006 (algorithm agility), NFR-007 (fail-closed)
- C-001 (boundary), C-006 (OSS/enterprise split via build tags)
- SC-007 (backend contract stability)
- Plan §3 (SigningBackend), §4.3 (sign dispatcher), §6.2 (backend dispatch table)

## Plan references

- §2.1 (package layout — `backends/backend.go`, `internal/algo/`, `internal/fingerprint/`)
- §4.3 (sign dispatcher fail-closed semantics)
- §6.2 (backend → SDK mapping per secrets-keychain research)

## Subtasks

- **T001** — Create `core/trust/backends/backend.go` defining the `SigningBackend` interface (`Kind`, `Health`, `SupportedAlgorithms`, `Sign`, `PublicKey`) and a process-local `Registry` with `Register(BackendKind, factory)` and `Lookup(BackendKind)`.
- **T002** — Create `core/trust/internal/algo/` registry: `Algorithm` enum (`ed25519`, `ecdsa-p256`, `rsa-pss-sha256`), per-algorithm metadata (key size, signature size, JOSE name), and `Verify(alg, pubKey, payload, signature) error` Ed25519-only implementation; ECDSA/RSA stubs return `ErrAlgorithmNotImplemented`.
- **T003** — Create `core/trust/internal/fingerprint/` providing canonical SHA-256 fingerprint over a public key for use as `key_id` and the identity-collision unique index column.
- **T004** — Implement `core/trust/sign.go` `Sign(ctx, payload, ident, opts)`: lookup backend, call `Health`, on `unavailable` emit a `backend-unavailable` audit hook (placeholder until WP05) and return a typed `ErrBackendUnavailable`; on `ok` call `backend.Sign` and return an `Envelope` shell (real envelope shaping lands in WP08).
- **T005** — Add table-driven unit tests covering: registry lookup miss, health = unavailable → fail-closed, health = ok → backend.Sign called exactly once, algorithm not in `SupportedAlgorithms` → typed error.

## Acceptance criteria

- `SigningBackend` matches plan §3 exactly.
- `Sign` never falls back to a different backend on unavailability (NFR-007 — verified by a test that registers a healthy `software` backend alongside an unavailable `oskeychain` backend and asserts the call fails closed for the unavailable one).
- Algorithm registry exposes Ed25519 verification; ECDSA/RSA-PSS slots compile and return `ErrAlgorithmNotImplemented` so v1.x can fill them per FR-004 phasing.
- Fingerprint output is deterministic across runs and platforms.
- Tests pass `go test ./core/trust/... -race`.

## Files to create/modify

- Create: `core/trust/backends/backend.go`, `core/trust/backends/registry.go`
- Create: `core/trust/internal/algo/algo.go`, `core/trust/internal/algo/ed25519.go`, `core/trust/internal/algo/ecdsa.go` (stub), `core/trust/internal/algo/rsa_pss.go` (stub)
- Create: `core/trust/internal/fingerprint/fingerprint.go`
- Modify: `core/trust/sign.go` (was a stub from WP01)
- Tests: `core/trust/backends/registry_test.go`, `core/trust/sign_test.go`, `core/trust/internal/algo/ed25519_test.go`, `core/trust/internal/fingerprint/fingerprint_test.go`

## Definition of done

- All subtasks complete; lint and vet clean.
- Coverage on `core/trust/backends/` and `core/trust/internal/algo/` ≥ 80% per charter testing standard.
- Sign dispatcher fail-closed behavior is asserted by a black-box test (DIRECTIVE_036).
- No `core/trust/` file outside `backends/` imports any backend SDK (charter C-001 — verified manually until WP12 lands the `depguard` rule).
