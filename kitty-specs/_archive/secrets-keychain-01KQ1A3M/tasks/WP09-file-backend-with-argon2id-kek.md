---
work_package_id: "WP09"
title: "File backend with optional argon2id KEK envelope"
dependencies:
  - "WP07"
planning_base_branch: "main"
merge_target_branch: "main"
branch_strategy: "Planning artifacts were generated on main; completed changes must merge back into main."
subtasks:
  - "T001: Implement plain-file Backend reading mounted secret files"
  - "T002: Implement argon2id-derived KEK envelope mode (opt-in)"
  - "T003: Validate file permissions (refuse world-readable)"
  - "T004: Implement Health() probe (file exists + readable)"
  - "T005: Black-box tests covering ok / missing / unreadable / empty / KEK-encrypted paths"
  - "T006: Document headless-Linux usage as the explicit-opt-in fallback (D2)"
phase: "Phase 9 - File Backend"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP09 – File backend with optional argon2id KEK envelope

## Goal

Ship the file backend covering Kubernetes/Docker mounted secret files in plain mode, plus an opt-in argon2id-derived KEK envelope mode for headless Linux installs that cannot use a Secret Service daemon. This is the explicit, operator-configured fallback path per research D2 — not a silent fallback.

## Spec references

- FR-005 (File backend): mounted-file backend for Kubernetes/Docker secrets.
- FR-014 (Error taxonomy): emits typed errors for missing, empty, permission-denied paths.
- C-006 (Fail-closed): no silent fallback; the file backend is selected only when explicitly configured.
- Edge case: "headless Linux with no Secret Service daemon" — refuse silent fallback (D2).

## Plan references

- §2 Architectural placement → `core/secrets/backends/file/`.
- §4 Internal layering → backend produces `Secret` via WP03's `StdlibSecret`.
- §7 Phasing → v1.0 ships file (plain + argon2id KEK envelope opt-in).
- §8 Risk register → R3 (headless Linux without Secret Service).
- §12 Acceptance mapping → FR-005 maps here.
- Research D2 → final Linux fallback chain explicitly elevates this backend to "operator-opt-in only".

## Subtasks

- Implement `Backend` for file at `core/secrets/backends/file/file.go` reading `RefFile` references.
- Plain mode: read file bytes; treat empty as `ErrReferenceEmpty`; treat missing as `ErrReferenceNotFound`; treat permission errors as `ErrPermissionDenied`.
- Argon2id KEK envelope mode (opt-in): file payload is a sealed envelope; KEK derived from operator-provided passphrase (or hardware token, future) using argon2id. Use a vetted Go crypto library (`golang.org/x/crypto/argon2` + `crypto/cipher` AEAD). Document parameters chosen.
- Validate file permissions at read time: refuse world-readable files (0o4xx); return `ErrPermissionDenied` with a clear message.
- Implement `Health()` checking file existence and readability for each registered `RefFile` locator.
- Black-box integration tests (DIRECTIVE_036) covering: ok, missing, world-readable refusal, empty, KEK-encrypted ok, KEK-encrypted with wrong passphrase.
- Document the headless-Linux usage pattern (`--secret-backend=file:<path>` with KEK) in the package doc per plan R3.

## Acceptance criteria

- `core/secrets/backends/file/file.go` compiles and registers for `RefFile`.
- Plain and argon2id envelope modes both supported; envelope mode is opt-in via configuration.
- World-readable files rejected with `ErrPermissionDenied`.
- Tests achieve ≥80% line coverage on `core/secrets/backends/file/`.
- Argon2id parameters documented (memory, iterations, parallelism, salt source).
- Charter quality gates pass.

## Files to create / modify

- Create `core/secrets/backends/file/file.go`.
- Create `core/secrets/backends/file/envelope.go` (argon2id KEK envelope helpers).
- Create `core/secrets/backends/file/file_test.go`.
- Create `core/secrets/backends/file/envelope_test.go`.

## Definition of done

- FR-005 acceptance scenarios traceable to tests in this WP.
- Resolver routes `file:` references through this backend after registration.
- No silent fallback (C-006): file backend is dispatched only when reference kind is `file`.
- Argon2id mode tested with positive and negative cases.
- Handoff: serves as the explicit fallback pattern referenced by the OS keychain backend (WP10) when Linux Secret Service is unavailable.
