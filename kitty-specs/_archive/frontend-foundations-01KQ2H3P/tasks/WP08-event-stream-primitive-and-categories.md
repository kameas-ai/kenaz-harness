---
work_package_id: "WP08"
title: "Event-stream row primitive + category color registry"
dependencies:
  - "WP02"
  - "WP06"
planning_base_branch: "main"
merge_target_branch: "main"
branch_strategy: "Planning artifacts were generated on main; completed changes must merge back into main."
subtasks:
  - "T001"
  - "T002"
  - "T003"
  - "T004"
phase: "Phase 8 - Event-stream primitive"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP08 – Event-stream row primitive + category color registry

## Goal

Build the Kenaz event-stream visual primitive (dense monospace tabular list with `timestamp · CATEGORY · subject · trailing-metadata`) as `EventStreamRow.vue`, plus the category color registry (5 Kenaz categories preserved + 10 harness extensions with proposed-from-Kenaz-palette assignments). The primitive is consumed by the audit-log viewer (downstream mission) and every live LLM/MCP/A2A/scheduler stream.

## Spec references

- FR-001c (event-stream primitive)
- FR-001d (category color system — Kenaz's existing 5 + harness extensions LLM/MCP/A2A/POLICY/TRUST/BUNDLE/CONTEXT/SECRETS/STORAGE/SCHEDULER)
- FR-014 (streaming-friendly text rendering — primitive must not thrash)
- NFR-004 (streaming smoothness 60 fps)
- C-006 (single accent color — category colors apply only to event-stream rows / legends / stream-domain labels)

## Plan references

- §2.1 (`EventStreamRow.vue` under `components/ui/`)
- §5.2 (event-stream row shape: `[timestamp] · [CATEGORY DOT + LABEL] · [subject] · [trailing-metadata]`, 6×6 px dot, RFC 3339 second-precision UTC monospace, ≤ 256 UTF-8 bytes subject mid-string truncation)
- §5.3 category color registry table — 5 Kenaz + 10 harness extensions, lives at `frontend/src/lib/categories.ts`
- §7 v1.0 item 5 (shadcn-vue primitives copied including `EventStreamRow`)
- §8 R-5 (streaming smoothness — `markRaw` + `triggerRef`, batch via `requestAnimationFrame`)

## Subtasks

- T001 — Implement `frontend/src/lib/categories.ts` exporting `Category` union and `CATEGORY_REGISTRY: Record<Category, { token: string; label: string }>` with the 5 Kenaz categories (FILESYSTEM/PROCESS/CLIPBOARD/NETWORK/KEYSTROKE) and 10 harness extensions (LLM/MCP/A2A/POLICY/TRUST/BUNDLE/CONTEXT/SECRETS/STORAGE/SCHEDULER) per plan §5.3. Mark TBD entries with a doc comment referencing the Kenaz design review gate.
- T002 — Implement `frontend/src/components/ui/EventStreamRow.vue` with typed props `{ timestamp: string; category: Category; subject: string; trailing?: string; size?: number }`. Render: 6×6 px category dot, uppercase tracking-wide small-caps label, monospace `timestamp` (`--ink-muted`), `subject` truncated mid-string with `…` if it would wrap, optional trailing metadata (`--ink-subtle`).
- T003 — Implement a sibling `EventStreamList.vue` that takes `entries: ReadonlyArray<EventStreamEntry>` and renders rows in a virtualized `<ScrollArea>` with rAF batching for high-rate streams (R-5 mitigation). Use `markRaw` on entry buffers; expose a `pause/resume` toggle for follow-tail.
- T004 — Add Vitest tests covering: row renders with each category color resolving to the correct token; subject mid-string truncation; rate-batched updates do not exceed 1 reactive update per frame; axe-core scan returns zero serious/critical violations.

## Acceptance criteria

- `EventStreamRow.vue` renders the exact tabular layout from §5.2 with token-driven colors for every category.
- `EventStreamList.vue` sustains 60 fps on a synthetic 50 e/s stream in a Vitest perf benchmark.
- `frontend/src/lib/categories.ts` exports the full registry with TBD entries clearly flagged.
- WP04 CSS-token check passes — every color value goes through `var(--token)` or a Tailwind utility.
- Axe-core scan of a populated `EventStreamList` returns zero serious/critical violations.

## Files to create/modify

- Create: `frontend/src/lib/categories.ts`.
- Create: `frontend/src/components/ui/EventStreamRow.vue`, `EventStreamList.vue`.
- Create: `frontend/src/components/ui/__tests__/EventStreamRow.spec.ts`, `EventStreamList.spec.ts`.
- Modify: `frontend/src/shell/LegendBar.vue` to render the category legend using the registry from T001.

## Definition of done

- All acceptance criteria pass.
- Performance benchmark documented in the test report.
- Cross-mission note: the audit-log viewer (downstream) will consume `EventStreamList`; the `event-log` mission's `Reader` is the data source. The primitive is data-source-agnostic.
- TBD category color entries open a follow-up issue tagged for Kenaz design review.
