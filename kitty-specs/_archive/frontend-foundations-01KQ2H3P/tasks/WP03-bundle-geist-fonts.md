---
work_package_id: "WP03"
title: "Bundle Geist + Geist Mono fonts (SIL OFL 1.1) under assets/fonts"
dependencies:
  - "WP02"
planning_base_branch: "main"
merge_target_branch: "main"
branch_strategy: "Planning artifacts were generated on main; completed changes must merge back into main."
subtasks:
  - "T001"
  - "T002"
  - "T003"
phase: "Phase 3 - Typography"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP03 – Bundle Geist + Geist Mono fonts (SIL OFL 1.1)

## Goal

Self-host Geist and Geist Mono under `frontend/src/assets/fonts/` with the SIL OFL 1.1 licence file alongside, declare them via `@font-face` so they resolve through `var(--font-ui)` / `var(--font-mono)`, and ensure the strict CSP `font-src 'self'` will pass without any external font request.

## Spec references

- FR-001a (Kenaz token import — typography is part of tokens)
- C-005 (local-first; zero outbound network traffic in steady state)
- C-003 (OSS / enterprise compatibility)
- NFR-006 (bundle size budget)

## Plan references

- §1 ("Kenaz tokens (… Geist + Geist Mono) are vendored verbatim")
- §2.1 (`frontend/src/assets/fonts/Geist-*.woff2` + `GeistMono-*.woff2` "SIL OFL 1.1 — bundled, served via @font-face, font-src 'self'")
- §5.1 type tokens (`--font-ui` Geist + system fallbacks, `--font-mono` Geist Mono + monospace fallbacks)
- §7 v1.0 item 3 (Geist + Geist Mono bundled at `frontend/src/assets/fonts/`, vendor licence file alongside)
- §8 R-3 (Geist font licensing audit — bundle the OFL 1.1 licence file at `frontend/src/assets/fonts/OFL.txt`, NOTICES entry, ADR)
- §8 R-8 (subset to Latin + numerics for bundle-size budget)

## Subtasks

- T001 — Acquire the woff2 builds of Geist (Regular, Medium, Semibold) and Geist Mono (Regular, Medium) subset to Latin + numerics. Place under `frontend/src/assets/fonts/` with predictable filenames (`Geist-Regular.woff2`, `Geist-Medium.woff2`, `Geist-Semibold.woff2`, `GeistMono-Regular.woff2`, `GeistMono-Medium.woff2`).
- T002 — Add `@font-face` declarations into `frontend/src/styles/global.css` (or a sibling `fonts.css`) referencing the bundled woff2 files via Vite asset URLs. Set `font-display: swap`. Confirm `var(--font-ui)` / `var(--font-mono)` resolve through.
- T003 — Add `frontend/src/assets/fonts/OFL.txt` (full SIL OFL 1.1 text) and append an entry to a top-level `NOTICES` file. Add ADR `docs/adr/0002-bundle-geist-fonts.md` recording the licence decision (DIRECTIVE_003).

## Acceptance criteria

- All five woff2 files exist under `frontend/src/assets/fonts/`.
- Loading the app issues zero outbound font requests (verified via DevTools Network tab and CSP `font-src 'self'` from WP05 not triggering violations).
- A simple visual check renders the Shell (after WP06) in Geist; mono content (event-stream rows) renders in Geist Mono.
- `OFL.txt` and `NOTICES` entry are committed.
- ADR 0002 merged.

## Files to create/modify

- Create: `frontend/src/assets/fonts/Geist-Regular.woff2`, `Geist-Medium.woff2`, `Geist-Semibold.woff2`, `GeistMono-Regular.woff2`, `GeistMono-Medium.woff2`.
- Create: `frontend/src/assets/fonts/OFL.txt`.
- Modify: `frontend/src/styles/global.css` (or create `frontend/src/styles/fonts.css` and import from `global.css`).
- Modify or create top-level `NOTICES`.
- Create: `docs/adr/0002-bundle-geist-fonts.md`.

## Definition of done

- All acceptance criteria pass.
- Bundle-size CI gate from WP01 still passes with fonts included.
- WP05 strict CSP (`font-src 'self'`) does not produce a violation when the app boots.
