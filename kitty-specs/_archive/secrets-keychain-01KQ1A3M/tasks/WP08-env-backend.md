---
work_package_id: "WP08"
title: "Environment-variable backend"
dependencies:
  - "WP07"
planning_base_branch: "main"
merge_target_branch: "main"
branch_strategy: "Planning artifacts were generated on main; completed changes must merge back into main."
subtasks:
  - "T001: Implement env Backend wrapping os.Getenv"
  - "T002: Treat empty value as ErrReferenceEmpty"
  - "T003: Implement Health() probe (always ok)"
  - "T004: Record env-var names referenced (per FR-016 audit; never log values)"
  - "T005: Self-register into the registry on import"
  - "T006: Black-box integration tests covering ok / not-set / empty paths"
phase: "Phase 8 - Env Backend"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP08 – Environment-variable backend

## Goal

Ship the environment-variable backend — the simplest backend and the one CI deployments rely on. Implements `Backend` for `RefEnv` references via `os.Getenv`, returning a `[]byte` Secret on success and the appropriate typed error on missing/empty values.

## Spec references

- FR-004 (Environment-variable backend): env-backend for headless deployments.
- FR-002 (Backend abstraction): conforms to the registry contract.
- FR-014 (Error taxonomy): emits `ErrReferenceNotFound` for unset, `ErrReferenceEmpty` for empty.
- FR-016 (Per-reference scoping): env-var *names* referenced are recorded; values never logged.
- C-002 (No plaintext in any persisted state).

## Plan references

- §2 Architectural placement → `core/secrets/backends/env/`.
- §4 Internal layering → "Process-arg / env scrub" subsection (records names, never values).
- §7 Phasing → v1.0 ships env backend.
- §9 Open question 3 → harness records env-var names referenced via this backend, never values.
- §12 Acceptance mapping → FR-004 maps here.

## Subtasks

- Implement `Backend` for env at `core/secrets/backends/env/env.go`. Wraps `os.Getenv`.
- Returns `ErrReferenceNotFound` if the variable is unset; `ErrReferenceEmpty` if set but empty.
- Implement `Health()` returning `ok` (env is always available in-process).
- Record the set of env-var names referenced over the session (for audit / FR-016) — names only, never values.
- Self-register into the registry via `init()` or an explicit registration function called from `core/secrets/secrets.go` (prefer explicit per Go style; document choice).
- Black-box integration tests (DIRECTIVE_036) using `t.Setenv`, covering ok, unset, empty cases.

## Acceptance criteria

- `core/secrets/backends/env/env.go` compiles; backend produces a `[]byte` `Secret` via WP03's `StdlibSecret`.
- Backend is registered and dispatches for `RefEnv` references.
- Empty and missing values return the correct sentinel errors (FR-014).
- Names of referenced env vars are recorded; values never appear in any test artifact, log, or error message.
- Tests achieve ≥80% line coverage on `core/secrets/backends/env/`.
- Charter quality gates pass.

## Files to create / modify

- Create `core/secrets/backends/env/env.go`.
- Create `core/secrets/backends/env/env_test.go`.

## Definition of done

- FR-004 acceptance scenarios traceable to tests in this WP.
- Resolver routes env references through this backend after registration.
- No SDK imports beyond stdlib (C-001).
- Handoff: simplest reference implementation; serves as the template for backends that follow.
