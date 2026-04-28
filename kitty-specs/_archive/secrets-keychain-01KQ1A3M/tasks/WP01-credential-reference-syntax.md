---
work_package_id: "WP01"
title: "CredentialReference type, parsing, and reference syntax"
dependencies: []
planning_base_branch: "main"
merge_target_branch: "main"
branch_strategy: "Planning artifacts were generated on main; completed changes must merge back into main."
subtasks:
  - "T001: Define RefKind enum (env, keychain, file, aws_profile, aws_kms, yubikey_piv, pkcs11)"
  - "T002: Implement CredentialReference struct (kind, locator, consumer_id)"
  - "T003: Implement parsers for each reference syntax form"
  - "T004: Implement (kind, locator) tuple identity + redaction-safe ID derivation"
  - "T005: Reject unknown kinds and empty locators with FR-014 errors"
  - "T006: Refuse inline plaintext (FR-015) at parse time"
  - "T007: Table-driven unit tests covering positive and negative cases"
phase: "Phase 1 - Reference Syntax"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP01 – CredentialReference type, parsing, and reference syntax

## Goal

Land the foundational `CredentialReference` value object and the parser that recognizes the seven reference forms (`env`, `keychain`, `file`, `aws_profile`, `aws_kms`, `yubikey_piv`, `pkcs11`). This is the type carried in configuration, lockfiles, and RPC payloads in place of any credential value, and the input every backend dispatches on.

## Spec references

- FR-001 (Indirect-reference syntax): stable shape `{ env: "VAR" }`, `{ keychain: "entry" }`, `{ file: "/path" }`, `{ aws_profile: "name" }`, `{ aws_kms: "arn" }`, `{ yubikey_piv: "<slot>" }`, `{ pkcs11: "<uri>" }`.
- FR-015 (Refusal of inline plaintext): parser rejects any inline plaintext credential at config-load time.
- FR-016 (Per-reference scoping): optional `consumer_id` attribute.
- C-002 (No plaintext credentials in any persisted state): persisted form is the reference, never the value.
- Key Entities: CredentialReference (`kind`, `locator`, `consumer_id`).

## Plan references

- §2 Architectural placement → `core/secrets/ref/` subpackage.
- §3 Public API → `CredentialReference` struct + `RefKind` enum sketch.
- §4 Internal layering → step 1 "Reference parse".
- §5 Data model summary → `CredentialReference` row.
- §12 Acceptance mapping → FR-001 maps here.

## Subtasks

- Define `RefKind` enum (typed integer with named constants per plan §3).
- Implement `CredentialReference` struct with `Kind`, `Locator`, `ConsumerID`.
- Implement parser functions producing typed errors for each reference form.
- Implement redaction-safe reference ID (hash of `(kind, locator)`) for use in errors and events; locator never logged.
- Reject unknown kinds, empty locators, and inline-plaintext patterns at parse time using sentinel errors that will be defined in WP07's error taxonomy package (placeholder errors here, replaced when WP07 lands).
- Provide table-driven unit tests covering each kind, malformed inputs, inline-plaintext rejection, redaction-safe ID determinism.

## Acceptance criteria

- `core/secrets/ref/reference.go` exposes `RefKind`, `CredentialReference`, parser entry points, and redaction-safe identity helper.
- Parser accepts all seven canonical forms exactly as specified in FR-001.
- Parser rejects inline plaintext patterns at parse time per FR-015.
- Redaction-safe reference ID is stable and never reveals the locator.
- Tests achieve ≥80% line coverage on `core/secrets/ref/` (charter quality bar).
- `go test ./core/secrets/ref/... -race` and `golangci-lint run` are clean.

## Files to create / modify

- Create `core/secrets/ref/reference.go`.
- Create `core/secrets/ref/reference_test.go`.
- Create `core/secrets/ref/doc.go` (package doc only if needed).

## Definition of done

- All subtasks complete and unit tests passing locally and in CI.
- Charter quality gates pass (`gofmt`, `goimports`, `go vet`, `golangci-lint run`).
- Spec FR-001, FR-015, FR-016 acceptance scenarios traceable to tests in this WP.
- No backend SDK imports in this package (DIRECTIVE_001, C-001).
- Handoff: types are stable enough for WP02 (backend contract) and WP05 (cache) to depend on.
