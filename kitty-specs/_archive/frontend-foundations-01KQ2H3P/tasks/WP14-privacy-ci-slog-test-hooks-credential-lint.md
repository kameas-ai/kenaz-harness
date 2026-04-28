---
work_package_id: "WP14"
title: "Privacy CI: no-user-content-in-slog grep + test-only-hooks-in-_test + no-credential-in-UI lint"
dependencies:
  - "WP04"
  - "WP05"
  - "WP10"
  - "WP12"
  - "WP13"
planning_base_branch: "main"
merge_target_branch: "main"
branch_strategy: "Planning artifacts were generated on main; completed changes must merge back into main."
subtasks:
  - "T001"
  - "T002"
  - "T003"
  - "T004"
phase: "Phase 14 - Privacy CI invariants"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP14 – Privacy CI invariants #2, #3, and the credential-in-UI lint

## Goal

Land the remaining Kenaz privacy CI invariants:

- Invariant #2 (no user-content fields in slog calls): a CI script greps every Go file and fails the build if any `slog.*` call references fields named `Subject`, `SubjectDim`, `Body`, `Prompt`, `Response`, `DraftInput`, `Path`, or any field flagged `// privacy:never-log` in struct comments.
- Invariant #3 (test-only hooks stay in `_test.go`): a CI script forbids exported identifiers prefixed `Test*`, `Fake*`, `Stub*`, or `Fixture*` in non-test files in `core/rpc/` and `core/rpc/views/*`.
- Vue/TS-side: a `no-credential-in-UI` lint rule and CI grep that fails if any TS interface or Vue component prop accepts a field named `value`, `secret`, `password`, `apiKey`, `token` (case-insensitive) on credential-shaped types — defence in depth for FR-020 / C-004.

Invariants #1 (CSP), #4 (CSS tokens), and #5 (settings file) ship in WP05, WP04, WP13 respectively. After this WP all five privacy CI invariants gate the PR.

## Spec references

- FR-020 (no credential values in UI)
- C-004 (no credential values in UI)
- C-007 (SOC 2 readiness)
- C-005 (local-first)

## Plan references

- §4.3 invariant #2 (slog grep, fields `Subject, SubjectDim, Body, Prompt, Response, DraftInput, Path` + `// privacy:never-log`)
- §4.3 invariant #3 (test-only-hooks-in-_test)
- §6 integration table — `secrets-keychain.Resolver`: "UI defends in depth: TS type for credential references has no `value` field at all"
- §7 v1.0 item 12 (all five invariants gating PR)

## Subtasks

- T001 — Implement `scripts/ci/check-no-user-content-in-slog.sh` that walks every `.go` file under `core/` and fails on any `slog.{Info,Debug,Warn,Error}` call referencing one of the forbidden field names. Use `slog.Attr` and named-arg patterns; respect `// privacy:never-log` struct-tag comments. Add a Go fixture that violates the rule and a fixture that conforms; tests assert correct pass/fail.
- T002 — Implement `scripts/ci/check-test-only-symbols.sh` that walks `core/rpc/` and `core/rpc/views/*` and fails on any non-`_test.go` file exporting an identifier prefixed `Test`, `Fake`, `Stub`, or `Fixture`. Add Go fixtures and tests as in T001.
- T003 — Implement `scripts/ci/check-no-credential-in-ui.sh` plus an ESLint custom rule (or `no-restricted-syntax`-based config) that flags any TS interface, type, or Vue component prop accepting a credential-shaped field name (`value`, `secret`, `password`, `apiKey`, `token`) on types whose name matches `*Reference|*Credential|*Secret`. Allowlist mechanism with `// privacy-allow: <reason>` markers.
- T004 — Update `docs/ci-invariants.md` to enumerate all five invariants with the script paths and rationale. Wire all three new checks into the CI workflow as PR-blocking. Verify on the existing tree (post-WP13) that all checks pass.

## Acceptance criteria

- `scripts/ci/check-no-user-content-in-slog.sh` exits 0 on `main`; exits non-zero when a tampered fixture introduces `slog.Info("...", "Body", body)` style call.
- `scripts/ci/check-test-only-symbols.sh` exits 0 on `main`; exits non-zero when a tampered fixture exports `FakeFoo` in a non-`_test.go` file.
- `scripts/ci/check-no-credential-in-ui.sh` and the ESLint rule both exit 0 on `main`; flag any newly added TS type with a `value`/`secret`/`password` field.
- `docs/ci-invariants.md` lists all five invariants (CSP from WP05, slog from this WP, test-symbols from this WP, CSS-tokens from WP04, single-persistence-file from WP13) with descriptions.
- All three CI checks block PR merge in the workflow.

## Files to create/modify

- Create: `scripts/ci/check-no-user-content-in-slog.sh`, `scripts/ci/check-test-only-symbols.sh`, `scripts/ci/check-no-credential-in-ui.sh`.
- Modify: `frontend/.eslintrc` (or `eslint.config.ts`) with the credential-in-UI custom rule.
- Modify: CI workflow to invoke all three checks.
- Modify: `docs/ci-invariants.md`.
- Create: Go fixtures under `scripts/ci/__tests__/go-fixtures/` and TS fixtures under `scripts/ci/__tests__/ts-fixtures/`.

## Definition of done

- All five privacy CI invariants are implemented and PR-blocking.
- Cross-mission note: `secrets-keychain-01KQ1A3M`'s Resolver returns reference-only metadata; this WP's TS lint enforces that no UI surface ever introduces a `value` field on those references — defence in depth.
- Cross-mission note: any future mission that needs to log fields under the forbidden names must add `// privacy-allow: <reason+ticket>` markers and document the exception in `docs/ci-invariants.md`.
