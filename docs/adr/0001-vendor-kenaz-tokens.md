# ADR 0001 — Vendor Kenaz design tokens as CSS

**Status**: Accepted
**Date**: 2026-04-25
**Mission**: `frontend-foundations-01KQ2H3P` (WP02)

## Context

Plan §1 / §4.1 / §5.1 / C-008 require the harness to consume Kenaz design
tokens as the single source of visual truth. Kenaz currently ships tokens
as a hand-written CSS custom-property block (`frontend/src/styles/tokens.css`).
Spec open question §9.1 enumerates three options: (a) npm package, (b)
vendored CSS, (c) shared Tailwind preset.

## Decision

For v1.0 the harness vendors `tokens.css` byte-identically into
`frontend/src/styles/tokens.css`, with the source path + SHA recorded in
`tokens.source.json` and the refresh procedure documented in
`frontend/src/styles/REFRESH.md`. Tailwind theme config maps each token
group into utility classes via CSS variables (`var(--surface-2)`).

Kenaz publishes no npm package today; vendoring is the only path that
keeps the harness in lock-step. Plan §7 v2 commits to migrating to a
published `@kenaz/design-tokens` package once Kenaz ships one.

## Consequences

- **Pros**: zero runtime drift; harness can audit every token at any
  commit; bundle stays tiny (`tokens.css` is < 5 KB).
- **Cons**: refresh is manual; risk R-1 (silent drift) is mitigated by a
  CI hash-check (deferred to a follow-up issue) and the documented
  refresh cadence.
- **Privacy CI invariant #4** (plan §4.3) blocks any other file under
  `frontend/src/` from declaring a raw color literal — see
  `scripts/ci/check-css-tokens.mjs`.
