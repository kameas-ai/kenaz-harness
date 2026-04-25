# ADR 0003 — Vue 3 + shadcn-vue + Tailwind + Radix Vue + TypeScript + Vite + Vitest

**Status**: Accepted
**Date**: 2026-04-25
**Mission**: `frontend-foundations-01KQ2H3P` (WP01)

## Context

The user direction (mission brief) names a firm stack: Vue 3 + shadcn-vue
+ Tailwind + Radix Vue + TypeScript + Vite + Vitest. Kenaz is built on
Preact + hand-written CSS, but the visual identity (not the runtime) is
what the harness inherits.

Spec open questions §9.1, §9.2 are resolved by the user direction:
visual identity flows through vendored Kenaz tokens (ADR 0001); the
implementation stack is independent of Kenaz's.

## Decision

- **Vue 3 Composition API** is the primary authoring shape.
- **TypeScript end-to-end** with `vue-tsc --noEmit` and `@typescript-eslint`.
- **Vite** for dev/build, with a build-time CSP injection plugin.
- **Vitest** + **@vue/test-utils** + **happy-dom** for unit/component tests.
- **Tailwind CSS** with theme tokens mapped to Kenaz CSS variables.
- **shadcn-vue** primitives copied INTO `frontend/src/components/ui/` —
  the team owns them; not a runtime dependency (FR-004 / C-002).
- **Radix Vue** as the accessible-primitive substrate underneath
  shadcn-vue components.

## Consequences

- **Pros**: shipped-and-supported toolchain; tree-shakable bundle; first-
  class accessibility from Radix; idiomatic Composition API.
- **Cons**: maintenance cost for owned primitives is acknowledged
  (spec assumption §"Assumptions" + FR-004).
- **DIRECTIVE_001**: enforced by ESLint `no-restricted-imports` +
  `scripts/ci/check-wailsjs-isolation.sh` — only `frontend/src/lib/harnessClient.ts`
  may import from `wailsjs/*`.
