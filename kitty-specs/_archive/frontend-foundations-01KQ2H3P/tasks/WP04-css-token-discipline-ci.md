---
work_package_id: "WP04"
title: "CSS token discipline CI: raw hex/rgba/hsl/oklch outside tokens.css fails the build"
dependencies:
  - "WP02"
planning_base_branch: "main"
merge_target_branch: "main"
branch_strategy: "Planning artifacts were generated on main; completed changes must merge back into main."
subtasks:
  - "T001"
  - "T002"
  - "T003"
phase: "Phase 4 - Privacy CI invariants"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP04 – CSS token discipline CI invariant

## Goal

Implement Kenaz privacy CI invariant #4 from plan §4.3: a CI script that greps `frontend/src/` and fails the build if any file other than `frontend/src/styles/tokens.css` contains a raw `#[0-9a-fA-F]{3,8}` literal, an `rgb(...)` / `rgba(...)` / `hsl(...)` / `oklch(...)` literal, or a hardcoded font stack. Tailwind utilities and `var(--token)` references are the only way to express color in the codebase. Provide an explicit allowlist mechanism for justified exceptions.

## Spec references

- FR-001a (Kenaz token import — drift impossible)
- FR-005 (centralized theme tokens)
- C-008 (Kenaz tokens are the source of truth)
- C-009 (VM-host visual coherence)
- SC-001 (≥ 4/5 "feels like a frontier AI tool")

## Plan references

- §4.3 invariant #4 ("CSS token discipline: a CI script greps `frontend/src/` and fails the build if any file other than `frontend/src/styles/tokens.css` contains a raw `#[0-9a-fA-F]{3,8}` literal, an `rgb(...)` / `rgba(...)` / `hsl(...)` / `oklch(...)` literal, or a hardcoded font stack")
- §7 v1.0 item 12 ("All five Kenaz privacy CI invariants implemented and gating the PR")

## Subtasks

- T001 — Implement `scripts/ci/check-css-tokens.sh` (or `.ts`) that walks `frontend/src/` (excluding `frontend/src/styles/tokens.css` and `frontend/src/assets/fonts/OFL.txt`) and fails with a non-zero exit code on any forbidden literal. Use ripgrep regex: `#[0-9a-fA-F]{3,8}\b`, `\brgba?\(`, `\bhsla?\(`, `\boklch\(`, font-family hardcoded stacks (e.g., `font-family:\s*['"][^v]`).
- T002 — Implement an allowlist mechanism: a `scripts/ci/css-tokens.allowlist` file lists explicit `path:line` entries (or a `// css-tokens-allow: <reason>` end-of-line marker pattern) that the checker tolerates. Document allowlist policy: every entry must have a reason and an open ticket ref.
- T003 — Wire the check into `package.json` (`npm run check:css-tokens`), the `npm run lint` chain, and a GitHub Actions / equivalent CI step. Add a unit test: introduce a tampered file in a fixture dir and assert the script fails; remove and assert it passes.

## Acceptance criteria

- Running `npm run check:css-tokens` on `main` exits 0.
- Introducing `color: #ff0000;` into any `.vue` / `.ts` / `.css` file under `frontend/src/` (other than `tokens.css`) makes the script exit non-zero.
- Adding a `// css-tokens-allow: <reason+ticket>` marker to a justified line restores exit 0.
- The check runs in CI on every PR and blocks merge on failure.
- Existing files under `frontend/src/` (after WP01–WP03) pass.

## Files to create/modify

- Create: `scripts/ci/check-css-tokens.sh` (or `.ts`).
- Create: `scripts/ci/css-tokens.allowlist` (initially empty with a comment header).
- Modify: `frontend/package.json` (add `check:css-tokens` script).
- Modify: `.github/workflows/ci.yml` or equivalent to invoke the check.
- Create: a small fixture test under `scripts/ci/__tests__/` proving pass and fail behavior.

## Definition of done

- All acceptance criteria pass.
- Fixture test passes locally and in CI.
- The check is documented in a top-level `CONTRIBUTING.md` or `docs/ci-invariants.md` listing all five privacy CI invariants (this is invariant #4).
- WP14 will land the remaining four invariants pointing at the same docs page.
