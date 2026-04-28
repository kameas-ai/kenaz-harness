---
work_package_id: "WP02"
title: "Backend contract, registry, and dispatch"
dependencies:
  - "WP01"
planning_base_branch: "main"
merge_target_branch: "main"
branch_strategy: "Planning artifacts were generated on main; completed changes must merge back into main."
subtasks:
  - "T001: Define Backend interface (Kind, SupportedRefKinds, Resolve, Health)"
  - "T002: Define BackendKind and BackendHealth types"
  - "T003: Implement registry with Register/Lookup/List"
  - "T004: Implement dispatch by RefKind → Backend"
  - "T005: Reject unknown kinds with typed error"
  - "T006: Unit tests for registration, dispatch, duplicate-kind handling"
phase: "Phase 2 - Backend Contract"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP02 – Backend contract, registry, and dispatch

## Goal

Define the stable `Backend` contract (`Kind`, `SupportedRefKinds`, `Resolve`, `Health`) and the registry that dispatches a `CredentialReference` to its backend. Every consumer-visible feature (resolution, pre-flight, health, cache) sits on top of this contract; once it lands, backends are addable in their own packages without consumer changes.

## Spec references

- FR-002 (Backend abstraction): stable contract `resolve(ref) -> bytes | error`, `health()`, `kind()` so backends are addable in their own packages.
- FR-014 (Error taxonomy): typed errors surfaced by Resolve/Health; backend-unavailable, reference-not-found, etc. (full taxonomy lands in WP07).
- FR-017 (Backend health probe): per-backend health surfaced via `harness secrets status`.
- NFR-005 (Backend extensibility): adding a new backend requires no changes to consumer packages.
- C-001 (Architectural integrity): only backend subpackages may import their respective SDKs.

## Plan references

- §2 Architectural placement → `core/secrets/registry/` subpackage and Rules (no consumer imports backend SDKs).
- §3 Public API → `Backend` interface sketch with four methods.
- §4 Internal layering → step 3 "Backend dispatch".
- §12 Acceptance mapping → FR-002, FR-017, NFR-005 map here.

## Subtasks

- Define `Backend` interface with `Kind() BackendKind`, `SupportedRefKinds() []RefKind`, `Resolve(ctx, ref) (Secret, error)`, `Health(ctx) BackendHealth`. Note: `Secret` type is forward-declared via interface placeholder until WP03 lands; minimize coupling.
- Define `BackendKind` (string newtype) and `BackendHealth` (struct with `Status` enum: `ok`, `degraded`, `unavailable`, plus `Message` and `LastChecked`).
- Implement `registry.Registry` with `Register(b Backend)`, `Lookup(kind RefKind) (Backend, error)`, `List() []Backend`.
- Implement dispatch helper: given a `CredentialReference`, route to the registered backend by `RefKind`; return typed `ErrBackendUnavailable` when no backend handles the kind.
- Provide unit tests covering registration, duplicate-kind detection, lookup miss, and dispatch correctness. Use a fake in-memory backend for tests (no SDK imports).

## Acceptance criteria

- `core/secrets/registry/registry.go` and `core/secrets/secrets.go` (top-level Backend interface re-export) compile against WP01's reference types.
- A new backend can be added by implementing `Backend` in its own subpackage and calling `registry.Register` — no edits to consumer-facing files (NFR-005 demonstration test).
- Duplicate registration for the same `RefKind` is detected and returns a typed error.
- Tests achieve ≥80% line coverage on `core/secrets/registry/`.
- `go test ./core/secrets/registry/... -race` and `golangci-lint run` are clean.

## Files to create / modify

- Create `core/secrets/secrets.go` (package-level interface re-exports).
- Create `core/secrets/registry/registry.go`.
- Create `core/secrets/registry/registry_test.go`.

## Definition of done

- WP01 dependency satisfied: parsers and `CredentialReference` consumed by the registry.
- Charter quality gates pass; black-box test demonstrates a fake backend dispatched without modifying consumer code (FR-002, NFR-005).
- Architectural integrity preserved: registry imports no backend SDKs (C-001).
- Handoff: stable surface for WP03 (Secret type), WP05 (cache), WP06 (preflight), and all per-backend WPs (WP08–WP13).
