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

## Amendment — 2026-11-14: package consumption, DS v0.13.0, OSS asset rule

**Status of the original decision**: superseded in mechanism, kept in
intent. The harness no longer vendors a copy of `tokens.css`.
`frontend/src/styles/tokens.css` `@import`s
`@kameas-ai/kenaz-design-system/tokens.css` from the published package
(pinned to an exact version in `frontend/package.json`); the refresh
procedure is the version bump described in
`frontend/src/styles/REFRESH.md`. The "Plan §7 v2" migration this ADR
anticipated has happened.

**DS v0.13.0** adds the spacing (`--space-*`), UI type (`--type-h3-*`,
`--type-ui|small|caption|tag-size`) and radius (`--radius-xs`,
`--radius-pill`) groups. `frontend/tailwind.config.ts` maps them onto the
Tailwind theme (value-identical for every on-scale step), and
`scripts/ci/check-css-tokens.mjs` (invariant #4) now forbids literals for
those groups as well as for colour — see the header of that script for
the exact rules. DS 0.12 retired the serif register (Newsreader,
`--font-serif`); headings use `--font-heading`.

**DS assets in an OSS repository.** The harness is Apache-2.0 and public;
`@kameas-ai/kenaz-design-system` is `UNLICENSED` and published with
restricted access. Rule, applied to every DS asset class:

- Do **not** copy DS source assets (font binaries, icon path data,
  component code, token files) into this repository. Consume them from
  the installed package at build time: tokens via `@import`, fonts via
  `url()` into `node_modules` (`fonts.css`), icons via the package's
  `ICON_REGISTRY` rendered through `frontend/src/shell/dsIcon.ts`.
- Brand marks are the exception that already existed: the favicon, the
  app icon (`build/appicon.png`, `build/windows/icon.ico`) and
  `build/branding/*` carry the Kenaz mark, as every Kameas product's
  icons do. They are regenerated from the DS geometry and its
  `scripts/render-brand-rasters.py`; they identify the product rather
  than ship DS code.
- Consequence (open, needs an owner decision): an outside contributor
  without `packages:read` on the `kameas-ai` org cannot install the
  frontend's dependencies, so cannot build the UI. Either publish the
  token / font / icon layer of the DS under an OSS licence, or accept
  that the harness UI is source-available-but-not-buildable outside the
  org.
