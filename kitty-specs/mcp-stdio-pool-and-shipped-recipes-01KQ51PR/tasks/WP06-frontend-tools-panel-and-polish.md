---
work_package_id: "WP06"
title: "Frontend Tools panel + key modal + polish + docs"
dependencies:
  - "WP05"
planning_base_branch: "main"
merge_target_branch: "main"
branch_strategy: "Branch wp06-tools-panel off main; merge back when WP06 acceptance gate passes."
subtasks:
  - "T031"
  - "T032"
  - "T033"
  - "T034"
  - "T035"
  - "T036"
phase: "Phase 9+10 — Frontend + polish"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-26T16:35:00Z"
    agent: "system"
    action: "WP prompt authored manually after Plan agent could not write files."
---

# Work Package Prompt: WP06 — Frontend Tools panel + key modal + polish + docs

## Goal

Extend the existing `KaneazToolsPanel.vue` (which currently hosts only the Memory tool) to render the shipped MCP recipes catalog with toggles, status pills, and a key-prompt modal. Add the cold-spawn warming indicator. Ship `docs/mcp-recipes.md`.

## Spec / plan references

- Spec: §FR-026..FR-029, NFR-003 (keys never leave the device), NFR-004 (cold-spawn UX), NFR-005 (FE green).
- Plan: Phase 9 (Frontend) + Phase 10 (Polish).

## Prerequisites

WP05 merged (rpc surface present + `Tools_*` bindings + chassis wiring).

## Subtasks

- **T031 — Type updates `frontend/src/lib/types.ts`** — add `Recipe`, `EnvKey`, `Capabilities`, `RecipeListing`, `RecipeStatus` matching the Go-side wire shapes. Field naming: TS camelCase (`displayName`, `envKeys`, etc.). Discriminator strings for state + scope.

- **T032 — Client updates `frontend/src/lib/harnessClient.ts` + `useHarnessAPI.ts`** — add `client.tools` namespace:
  - `listRecipes(): Promise<RecipeListing[]>`
  - `installRecipe(id, env): Promise<RecipeStatus>`
  - `uninstallRecipe(id): Promise<void>`
  - `forgetRecipeKey(id, envName): Promise<void>`
  - `recipeStatus(id): Promise<RecipeStatus>`
  - `useTools()` hook returning `{ recipes, refresh, install, uninstall, forgetKey, statusFor }`. Polling: when any visible recipe is in non-terminal state (`starting | running | restarting`), poll `recipeStatus` for those rows at 1 Hz; idle when all rows are terminal (`stopped | failed`).

- **T033 — `frontend/src/views/tools/KaneazToolsPanel.vue` extension** — keep the existing Memory row at the top. Below it, render a section `Connected MCP recipes`:
  - List `recipes` from `useTools()` on mount.
  - Each row: category icon (mapping `category → Lucide icon`: `search → Search`, `filesystem → Folder`, `memory → Brain`, `fetch → Globe`, default → `Wrench` — extend `frontend/src/shell/icons.ts` with the missing ones), display name, description, status pill (reuse existing `text-signal-*` color logic; states: stopped / starting / running / restarting / failed), toggle.
  - Toggle on with all required env keys present in keychain → calls `installRecipe(id, {})`.
  - Toggle on with missing keys → opens `RecipeKeyPromptModal`.
  - Toggle off → `uninstallRecipe(id)`.
  - "Forget key" small affordance per env-key when keys are present.
  - Cold-spawn warming indicator: when `state === "starting"` and elapsed time > 4 s, show a "warming…" inline note with a brief explanation ("npm fetch on first run can take 5–15 s").

- **T034 — `frontend/src/views/tools/RecipeKeyPromptModal.vue` (NEW)** —
  - Renders a form for each `EnvKey` in the recipe.
  - Required keys are marked with an asterisk; submit is disabled until they're filled.
  - "Get a key →" link uses `EnvKey.docsUrl`.
  - Submit calls `installRecipe(id, env)` and surfaces error from impl into a banner.
  - On success, closes modal and emits `installed` event.
  - Modal pattern follows existing modals in the codebase (look at `NewSessionDialog.vue` or `radix-vue` Dialog).

- **T035 — Tests** —
  - `frontend/src/views/tools/__tests__/KaneazToolsPanel.test.ts`:
    - Stub the rpc client's `tools` namespace.
    - Asserts list rendering with mocked recipes.
    - Toggle-on with keys-present calls `installRecipe(id, {})`.
    - Toggle-on with missing keys opens the modal (asserts modal-open state).
    - Toggle-off calls `uninstallRecipe`.
    - Polling mounts when a row enters `starting`; polling unmounts when all rows are terminal.
  - `frontend/src/views/tools/__tests__/RecipeKeyPromptModal.test.ts`:
    - Required-key validation (submit disabled until all required filled).
    - Docs link wiring.
    - Submit emits `installed` and clears state.

- **T036 — `docs/mcp-recipes.md`** — operator/dev-facing documentation:
  - How to install a shipped recipe from the UI (Brave Search walkthrough end-to-end).
  - How to add a new recipe (drop entry in `shipped.json`, no Go code change required).
  - Cost-amplification note for `samplingPolicy.allowed` recipes (when on, the server gets to call your active provider).
  - Manual A14 verification checklist (the wails dev walkthrough that CI cannot run).

## Acceptance

- `cd frontend && npm test -- --run` ≥ baseline + new tests.
- `cd frontend && npm run build` clean.
- A1 walkthrough manual checklist from `docs/mcp-recipes.md` reproduces end-to-end (toggle on Brave → enter key → assistant performs a real web search). Verified manually post-merge — the merger should walk it before declaring done.

## Constraints

- Reuse existing UI primitives. Don't introduce a new modal / popover system.
- Don't add raw color literals — use existing token CSS variables (privacy CI #4).
- Don't render markdown via v-html in any new surface (the chat's `StreamingText.vue` is the only allowed markdown renderer).
- The existing Memory tool row stays unchanged.
- Don't touch backend code — WP05 owns it.

## Branch strategy

Branch `wp06-tools-panel` off `main`.
