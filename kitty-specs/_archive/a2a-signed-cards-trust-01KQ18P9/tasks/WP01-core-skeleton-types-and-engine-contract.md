---
work_package_id: "WP01"
title: "Core skeleton: TrustEngine contract, types, and rejection taxonomy"
dependencies: []
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

# Work Package Prompt: WP01 – Core skeleton: TrustEngine contract, types, and rejection taxonomy

## Goal

Establish the `core/trust/` package with the public `TrustEngine` interface, key entity types (`Anchor`, `Envelope`, `VerificationResult`, `IdentityRef`, `RevocationRecord`, `PublicKey`, `Algorithm`, `BackendKind`), and the stable `RejectionCode` taxonomy from FR-017. No verification or signing logic yet — this is the contract surface every consumer (acp, bundle, context, policy) plans against per FR-012/SC-007. Declares package documentation (`doc.go`) and an empty engine factory.

## Spec references

- FR-001, FR-002, FR-008, FR-012, FR-017
- NFR-006 (algorithm agility), NFR-007 (fail-closed surface for `Sign`)
- C-001 (charter architectural integrity)
- SC-007 (backend contract stability)
- Plan §3 (Public API), §2.1 (package layout)

## Plan references

- §1.4 (v1.0 status table — what is implemented vs interface-only)
- §3 (illustrative Go signatures)
- §4 (internal layering)

## Subtasks

- **T001** — Create `core/trust/` directory layout per plan §2.1: `trust.go`, `types.go`, `errors.go`, `doc.go`, plus empty stub files for `verify.go`, `sign.go`, `anchor.go`, `rotation.go`, `revocation.go`, `policy.go`, `envelope.go`, `audit.go`, `config.go`.
- **T002** — Define the `TrustEngine` interface (Verify, Sign, InstallAnchor, RemoveAnchor, ListAnchors, BeginRotation, CompleteRotation, IngestRevocation, Preflight) with the exact signatures from plan §3.
- **T003** — Define value types: `Anchor`, `AnchorKind`, `Envelope`, `VerificationResult`, `Decision`, `CacheState`, `IdentityRef`, `BackendRef`, `RevocationRecord`, `RevocationSubject`, `PublicKey`, `Algorithm`, `BackendKind`, `HealthStatus`, `PreflightFinding`, `VerifyOptions`, `SignOptions`.
- **T004** — Implement the full `RejectionCode` enum from FR-017 (`signature_invalid`, `algorithm_not_permitted`, `anchor_missing`, `anchor_removed`, `key_revoked`, `key_expired`, `identity_collision`, `clock_skew_exceeded`, `chain_depth_exceeded`) as exported constants in `errors.go`; add a `String()` method and a typed error wrapper.
- **T005** — Add `core/trust/doc.go` describing the boundary contract (DIRECTIVE_001, C-001) and a `NewEngine(...)` factory stub returning `ErrNotImplemented` so callers can compile against the interface.

## Acceptance criteria

- `go build ./core/trust/...` succeeds with no implementation in any method body beyond returning a sentinel "not implemented" error.
- The `RejectionCode` enum exposes every code listed in FR-017 plus `chain_depth_exceeded` (defense-in-depth from spec edge cases).
- The `TrustEngine` interface signatures match plan §3 exactly; deviations require an inline comment citing the FR or NFR.
- `go vet ./core/trust/...` and `golangci-lint run ./core/trust/...` are clean.
- A black-box compile-only test (`engine_contract_test.go`) confirms an external package can declare a variable of type `trust.TrustEngine` and reference every method on the interface (DIRECTIVE_036).

## Files to create/modify

- Create: `core/trust/doc.go`, `core/trust/trust.go`, `core/trust/types.go`, `core/trust/errors.go`
- Create stub files: `core/trust/verify.go`, `core/trust/sign.go`, `core/trust/anchor.go`, `core/trust/rotation.go`, `core/trust/revocation.go`, `core/trust/policy.go`, `core/trust/envelope.go`, `core/trust/audit.go`, `core/trust/config.go`
- Create test: `core/trust/engine_contract_test.go`
- Modify: `go.mod` (no new deps yet)

## Definition of done

- All five subtasks complete.
- Public symbols documented with godoc comments citing the relevant FR/NFR.
- Package compiles cleanly; lint and vet are green.
- Contract test demonstrates the interface is consumable from a sibling package.
- Inline reference to plan §3 included in `trust.go` header comment.
