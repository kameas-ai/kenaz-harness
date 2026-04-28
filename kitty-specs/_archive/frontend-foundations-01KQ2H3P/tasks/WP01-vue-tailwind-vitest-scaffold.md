---
work_package_id: "WP01"
title: "Vue 3 + Tailwind + shadcn-vue + Radix Vue + Vite + Vitest scaffold"
dependencies: []
planning_base_branch: "main"
merge_target_branch: "main"
branch_strategy: "Planning artifacts were generated on main; completed changes must merge back into main."
subtasks:
  - "T001"
  - "T002"
  - "T003"
  - "T004"
  - "T005"
phase: "Phase 1 - Scaffolding"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP01 – Vue 3 + Tailwind + shadcn-vue + Radix Vue + Vite + Vitest scaffold

## Goal

Replace the existing wails-init Vue scaffold with the firm stack from the plan: Vue 3 (Composition API) + TypeScript + Vite + Vitest, with Tailwind CSS, shadcn-vue primitives layered on Radix Vue, ESLint + Prettier + `vue-tsc` clean, and the `frontend/` tree shape from plan §2.1 stubbed out. This WP delivers the empty chassis on which every other WP layers; no tokens, fonts, RPC, or shell components yet.

## Spec references

- FR-004 (owned design-system primitives)
- FR-007 (typed RPC client — interface stub only here)
- NFR-006 (bundle size budget < 1.5 MB gzipped — establish CI hook)
- NFR-007 (RPC type fidelity — strict TS, no `any`)
- C-002 (owned primitives, not runtime framework)
- C-003 (OSS / enterprise compatibility)
- Assumptions: "Wails v2 + Vue 3 + Vite scaffold; no rewrite to Tauri / Electron"; "TypeScript end-to-end in the frontend"

## Plan references

- §1 (stack: Vue 3 + Vite + TS + Vitest, Tailwind, shadcn-vue, Radix Vue)
- §2.1 (frontend tree skeleton — directory shape only here)
- §7 v1.0 item 1 ("Vue 3 + Vite + TypeScript + Vitest scaffold; ESLint + Prettier + `vue-tsc` clean")
- §8 R-8 (bundle-size CI gate at PR time — establish wiring)

## Subtasks

- T001 — Remove wails-init Vue scaffold artifacts (`frontend/` if present, top-level `App.vue`, `app.go` if redundant) and re-init at `frontend/` with Vite + Vue 3 + TS template. Preserve the existing `wailsjs/` generation pathway.
- T002 — Install Tailwind CSS, PostCSS, autoprefixer; add `tailwind.config.ts` (token mapping deferred to WP02), `postcss.config.cjs`, `frontend/src/styles/global.css` with the Tailwind directives.
- T003 — Install Radix Vue + scaffold an empty `frontend/src/components/ui/` directory ready for shadcn-vue copies in WP06–WP09; wire up `tsconfig.json` paths (`@/*` → `frontend/src/*`).
- T004 — Install Vitest + Vue Test Utils + happy-dom; add `vitest.config.ts`, a smoke test that renders `App.vue`, and a `package.json` `test` script.
- T005 — Install ESLint (with `eslint-plugin-vue`, `@typescript-eslint`), Prettier, and `vue-tsc`; add `lint`, `format`, `typecheck` scripts; ensure `vue-tsc --noEmit` passes on the empty scaffold.

## Acceptance criteria

- `frontend/` tree exists with the directories listed in plan §2.1 (empty `shell/`, `views/`, `components/ui/`, `lib/`, `styles/`, `assets/fonts/`).
- `npm run dev` (or pnpm equivalent) boots Vite with the Vue 3 SPA.
- `npm run test` runs a passing Vitest smoke test.
- `npm run typecheck` (`vue-tsc --noEmit`) is clean.
- `npm run lint` is clean on the scaffold.
- Wails build still succeeds end-to-end (the binary embeds the new frontend bundle).
- No raw color literals introduced — all styling deferred to WP02.

## Files to create/modify

- Create: `frontend/index.html`, `frontend/vite.config.ts`, `frontend/tailwind.config.ts` (token mapping placeholder), `frontend/postcss.config.cjs`, `frontend/tsconfig.json`, `frontend/vitest.config.ts`, `frontend/package.json`.
- Create: `frontend/src/main.ts`, `frontend/src/App.vue`, `frontend/src/styles/global.css` (Tailwind directives only).
- Create empty directories: `frontend/src/shell/`, `frontend/src/views/`, `frontend/src/components/ui/`, `frontend/src/lib/`, `frontend/src/assets/fonts/`.
- Modify: `wails.json`, `main.go` if needed to point at new frontend root.
- Delete or replace: legacy `app.go` if redundant per the working-tree status.

## Definition of done

- All acceptance criteria pass on a clean checkout.
- ADR added under `docs/adr/` documenting the choice of Vue 3 + Tailwind + shadcn-vue + Radix Vue (DIRECTIVE_003).
- A bundle-size CI placeholder exists (script that fails if gzipped main chunk > 1.5 MB; passes trivially on empty scaffold).
- WP02 can begin without further scaffold changes.
