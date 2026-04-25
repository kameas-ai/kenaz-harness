# ADR 0002 — Bundle Geist + Geist Mono under SIL OFL 1.1

**Status**: Accepted
**Date**: 2026-04-25
**Mission**: `frontend-foundations-01KQ2H3P` (WP03)

## Context

Plan §1 / §5.1 names Geist + Geist Mono as the harness's sans + mono
families, matching Kenaz. CSP `font-src 'self'` (plan §4.3 invariant #1)
forbids any external font request, so the woff2 builds MUST be self-hosted.

## Decision

Bundle Geist Regular/Medium/SemiBold and Geist Mono Regular/Medium under
`frontend/src/assets/fonts/`. Ship the SIL Open Font License 1.1 text at
`frontend/src/assets/fonts/OFL.txt` and append a NOTICES entry at
`/NOTICES`.

`@font-face` declarations live in `frontend/src/styles/fonts.css` and
resolve through `var(--font-ui)` / `var(--font-mono)` from `tokens.css`.

The woff2 builds are sourced from `https://github.com/vercel/geist-font`
releases (or copied from the Kenaz vendored copies if available).

## Consequences

- **Pros**: zero outbound font traffic; CSP-compliant; visual parity with
  Kenaz; OFL 1.1 is enterprise-procurement compatible.
- **Cons**: ~230 KB binary commit per bump.
- **Risk R-3** (font licensing audit, plan §8): mitigated by shipping
  `OFL.txt` + NOTICES entry alongside the binaries.
- **Risk R-8** (bundle-size budget, plan §8): mitigated by subset-to-Latin
  builds and `font-display: swap`.

## Vendoring note

If the woff2 binaries are missing from a checkout (e.g., the harness
worktree was created before the vendoring step ran), the system fonts
declared in `--font-ui` / `--font-mono` provide a usable fallback. The
release gate requires the binaries to be present.
