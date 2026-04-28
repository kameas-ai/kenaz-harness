---
work_package_id: "WP08"
title: "Scope-access checker consuming credential refs from secrets-keychain"
dependencies:
  - "WP01"
  - "secrets-keychain-01KQ1A3M"
planning_base_branch: "main"
merge_target_branch: "main"
branch_strategy: "Planning artifacts were generated on main; completed changes must merge back into main."
subtasks:
  - "T001"
  - "T002"
  - "T003"
  - "T004"
phase: "Phase 8 - Scope-access checker"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP08 – Scope-access checker

## Goal

Implement `core/context/access/`: enforce per-pack `AccessPolicy` (role gates and credential gates) using only credential references resolved through `core/secret/`. No plaintext credentials anywhere (C-004). This WP is the gatekeeper that ensures role-scoped pack bytes never reach an out-of-role operator (NFR-005, SC-006), and it owns the auto-expunge-on-scope-loss flow (FR-016).

## Spec references

- FR-009 (Scoped access via signed credentials)
- FR-016 (Automatic expunge on scope loss)
- NFR-005 (0 % of resolved bytes from a role-scoped pack reach an out-of-role operator)
- SC-006 (Role-scoped content never appears in out-of-role resolved context)
- C-004 (Credential references only — no inline plaintext)
- Acceptance scenarios US7.1–7.3 (role denial, role grant, role removal expunge)

## Plan references

- §4.4 (Scope-access checker — role gates and credential gates)
- §3 (`AccessPolicy{RequiredRoles, CredentialRef secret.Ref}`)
- §6 (Integration with `secrets-keychain-01KQ1A3M` — `secret.Ref` resolution)
- Risk R2 (recheck at injection time as well as resolution time — coordinate with WP07)

## Subtasks

- T001 Define `core/context/access/policy.go` with `AccessPolicy{RequiredRoles, CredentialRef}` and `Checker` interface.
- T002 Role-gate check: read operator role set from a configuration file under the project data directory (per plan §4.4 v1 sourcing); deny if any required role is missing; emit typed `ErrAccessDenied`.
- T003 Credential-gate check: resolve `secret.Ref` via `core/secret/`; if missing or expired, deny and ensure no pack content is cached (NFR-005).
- T004 Auto-expunge (FR-016): when a previously-permitted pack's role set changes (operator removed from role R), invalidate cache entry, emit `scope_revoked` event candidate, and remove all local content for the pack.

## Acceptance criteria

- An operator outside a required role cannot resolve a role-scoped pack — typed denial and zero bytes cached (validates NFR-005, SC-006).
- An operator with the role resolves successfully.
- Role removal triggers expunge on the next resolution pass; cache contains no residue from the revoked pack.
- All credential access goes through `core/secret/` `Ref` indirection — no inline plaintext anywhere in pack metadata, content, or channel config (C-004 audit).

## Files to create/modify

- `core/context/access/policy.go`
- `core/context/access/checker.go`
- `core/context/access/checker_test.go`
- `core/context/access/testdata/role-config.yaml`

## Definition of done

- Audit-suite test asserts zero plaintext credential bytes anywhere in events or cache produced by this WP.
- Auto-expunge integration test passes against real on-disk cache.
- WP merged to main via squash-merge PR.
