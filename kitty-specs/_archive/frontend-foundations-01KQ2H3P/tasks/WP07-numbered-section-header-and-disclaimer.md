---
work_package_id: "WP07"
title: "Numbered-section header pattern primitive + AI-output disclaimer chrome"
dependencies:
  - "WP06"
planning_base_branch: "main"
merge_target_branch: "main"
branch_strategy: "Planning artifacts were generated on main; completed changes must merge back into main."
subtasks:
  - "T001"
  - "T002"
  - "T003"
phase: "Phase 7 - Information architecture"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP07 – Numbered-section header pattern + AI-output disclaimer chrome

## Goal

Land two Kenaz-aligned content primitives:

1. The numbered-section header pattern (`NN / SECTION NAME` muted small caps + prominent section title + one-paragraph subtitle) as a reusable primitive every primary surface uses.
2. The AI-output disclaimer chrome ("Content is user-generated and unverified") in the Titlebar adjacent to the brand mark, reused across surfaces that render model output.

## Spec references

- FR-001b (numbered-section header pattern)
- FR-001h (AI-output disclaimer chrome)
- C-009 (VM-host visual coherence)
- C-010 (enterprise-first defaults — no playful empty states; restrained chrome)
- SC-001 (≥ 4/5 "feels like a frontier AI tool")

## Plan references

- §2.1 (`CanvasHead.vue` numbered-section header pattern; `Titlebar.vue` AI-output disclaimer)
- §1 ("Every primary surface uses Kenaz's numbered-section header pattern")
- §7 v1.0 item 4 (shell components) and item 15 (AI-output disclaimer chrome in Titlebar)

## Subtasks

- T001 — Replace the slot scaffold from WP06's `CanvasHead.vue` with a real implementation: typed props `{ number: string; section: string; title: string; subtitle?: string }` rendering muted small-caps `NN / SECTION NAME` separator, bold prominent title (Geist Semibold), one-paragraph subtitle (`--ink-muted`). Add a slot for trailing chrome (Customize button etc.).
- T002 — Implement the AI-output disclaimer in `Titlebar.vue` (or a sibling `DisclaimerChrome.vue`) with copy "Content is user-generated and unverified" rendered in `--ink-subtle` next to the brand mark. Add a typed prop or composable so a surface can hide the disclaimer when it does not render model output.
- T003 — Add Vitest tests covering: numbered-section header rendering with and without subtitle; disclaimer visible by default; disclaimer hidden when `showDisclaimer = false`. Add an axe-core test asserting both primitives have correct ARIA roles (`<header>` semantics; disclaimer text is announced).

## Acceptance criteria

- A demo route under `/sessions` renders a `CanvasHead` with `number="01"`, `section="SESSIONS"`, `title="Recent runs"`, `subtitle="..."` matching the Kenaz visual register exactly (muted small caps + title + paragraph).
- The Titlebar disclaimer renders "Content is user-generated and unverified" by default.
- A surface that opts out (`showDisclaimer = false` via composable or prop) hides the disclaimer.
- All Vitest + axe-core tests pass.
- WP04 CSS-token check still passes.

## Files to create/modify

- Modify: `frontend/src/shell/CanvasHead.vue`, `frontend/src/shell/Titlebar.vue`.
- Optionally create: `frontend/src/components/ui/DisclaimerChrome.vue`.
- Create: Vitest tests under `frontend/src/shell/__tests__/` for both primitives.
- Modify: a placeholder `frontend/src/views/sessions/SessionsView.vue` to demonstrate `CanvasHead` usage.

## Definition of done

- All acceptance criteria pass.
- Both primitives are referenced from `docs/design-primitives.md` (or equivalent) so downstream UI missions know to consume them.
- Visual diff vs Kenaz reference (numbered-section header from the "02 / VM SANDBOX" reference) shows no register drift.
