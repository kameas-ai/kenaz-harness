---
work_package_id: "WP06"
title: "Shell layout: Shell + Titlebar + Toolbar + LeftRail + CanvasHead + LegendBar + RailEntry + StatusPill"
dependencies:
  - "WP02"
  - "WP03"
planning_base_branch: "main"
merge_target_branch: "main"
branch_strategy: "Planning artifacts were generated on main; completed changes must merge back into main."
subtasks:
  - "T001"
  - "T002"
  - "T003"
  - "T004"
  - "T005"
  - "T006"
  - "T007"
phase: "Phase 6 - Shell"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP06 – Kenaz-aligned layout shell components

## Goal

Implement the persistent app-level layout shell from plan §2.1: a 3-region grid (Titlebar / [LeftRail | content + Toolbar/CanvasHead/LegendBar]) wrapping a Vue Router + KeepAlive surface area. Every component is token-themed only (no raw colors), uses Lucide line-style icons, and matches the Kenaz visual register so a designer alt-tabbing between the harness and a Kenaz surface sees no visual seam.

## Spec references

- FR-001 (left-rail layout shell)
- FR-002 (multi-session UI state preservation — KeepAlive scaffolding here, full per-session state in WP12)
- FR-003 (primary surface navigation)
- FR-016 (window-size minimum + collapsing rail)
- C-006 (single accent color)
- C-009 (VM-host visual coherence)
- C-010 (enterprise-first defaults)
- SC-001 (≥ 4/5 "feels like a frontier AI tool")

## Plan references

- §2.1 frontend tree under `src/shell/`: `Shell.vue`, `Titlebar.vue`, `Toolbar.vue`, `LeftRail.vue`, `RailEntry.vue`, `CanvasHead.vue`, `LegendBar.vue`, `StatusPill.vue`, `icons.ts`
- §4.1 ("Shell mounts: router + KeepAlive + HarnessClientContext (provided once at the root)")
- §7 v1.0 item 4 (shell components list)

## Subtasks

- T001 — Implement `Shell.vue` as a CSS-grid 3-region wrapper (Titlebar / [LeftRail | main]). Layout-only CSS in `frontend/src/styles/shell.css` (grid regions); colors via Tailwind utilities resolving to tokens. Mount Vue Router `<router-view>` inside the main region, wrapped in `<KeepAlive>` (placeholder cache until WP12 wires per-session state).
- T002 — Implement `Titlebar.vue` with the Wails window-drag region (`-webkit-app-region: drag`), brand mark slot, AI-output disclaimer slot (filled in WP07), and right-side utility slot (theme switch button placeholder, command-palette trigger).
- T003 — Implement `LeftRail.vue` with three regions: new-session affordance (top), sessions list (middle, scrollable), primary-surfaces nav (bottom — sessions / tools / bundles / providers / audit / settings). Use `RailEntry.vue` for each row. Collapse to icons-only at < 960px (`--breakpoint-two-col`).
- T004 — Implement `RailEntry.vue` (icon + label + active-state indicator using `--accent` for active). Lucide icon via `icons.ts`. Active state respects single-accent constraint (C-006).
- T005 — Implement `Toolbar.vue` (surface-level action row hosting Customize, theme switch, palette trigger). Token-themed; one row, low-contrast.
- T006 — Implement `CanvasHead.vue` as a slot-driven primitive (handed off to WP07 for the numbered-section header pattern). For this WP it renders the slot wrappers and reserves the `NN / SECTION NAME` muted-small-caps + title + subtitle slots.
- T007 — Implement `LegendBar.vue` (placeholder for category color legend + live-rate inline indicator slots, wired in WP08/WP09) and `StatusPill.vue` (placeholder for Model · Tier · Build triple, wired in WP09). Add `frontend/src/shell/icons.ts` re-exporting Lucide icons used by the shell.

## Acceptance criteria

- The harness opens to a populated layout shell rendering at the correct token-driven palette: surfaces #0A0A0B → #26262C, ink #F4F1EA, brass accent reserved for active states only.
- Window-drag region works on macOS / Windows / Linux per Wails defaults.
- Below `--breakpoint-two-col` (960 px), the LeftRail collapses to an icon-only column.
- All components use only Tailwind token utilities; no raw color literal — WP04 CI passes.
- `vue-tsc --noEmit` clean; `npm run lint` clean.
- An axe-core scan of the bare shell returns zero serious or critical violations (WP15 will tighten this end-to-end).
- A Vitest snapshot/render test covers each shell component.

## Files to create/modify

- Create: `frontend/src/shell/Shell.vue`, `Titlebar.vue`, `Toolbar.vue`, `LeftRail.vue`, `RailEntry.vue`, `CanvasHead.vue`, `LegendBar.vue`, `StatusPill.vue`, `icons.ts`.
- Create: `frontend/src/styles/shell.css` (grid regions only).
- Modify: `frontend/src/App.vue` to render `<Shell />`.
- Modify: `frontend/src/main.ts` to install Vue Router with a placeholder `/sessions` route.
- Create: Vitest tests for each shell component.

## Definition of done

- All acceptance criteria pass.
- Designer review (or screenshot diff against a reference Kenaz surface) confirms no visual seam at the shell level.
- WP07 can layer the numbered-section header content into `CanvasHead`; WP08/WP09 can layer event-stream / privacy / status content into `LegendBar` and `StatusPill`.
