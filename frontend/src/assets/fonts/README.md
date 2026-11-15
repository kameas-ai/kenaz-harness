# Bundled fonts (SIL OFL 1.1)

Geist (400/500/600/700) and Geist Mono (400/500) are loaded via
`@font-face` rules in `src/styles/fonts.css`, referenced through
`var(--font-ui)` / `var(--font-mono)` / `var(--font-heading)`. (DS 0.12
retired the Newsreader serif and `--font-serif`.) CSP `font-src 'self'` (plan §4.3
invariant #1) forbids any external font request.

As of the design-system-adoption change, the woff2 binaries themselves are
**not vendored in this directory** — `fonts.css` resolves them via a
relative path into `@kameas-ai/kenaz-design-system`'s own bundled copies
(`node_modules/@kameas-ai/kenaz-design-system/src/assets/fonts/`), which
are the same byte-identical Geist files this directory used to duplicate.
Vite still copies the resolved file into the build output at build time,
so the served asset stays same-origin — CSP invariant #1 holds exactly as
it did when the files lived locally.

`OFL.txt` here is the SIL OFL 1.1 text covering the Geist family.

If the DS package is ever missing or its font paths change, the browser
falls back to the system fonts declared in `--font-ui` / `--font-mono`. The fallback chain keeps the UI usable but visually
drifts from Kenaz; a broken `fonts.css` reference is a build-time/CI
concern, not a runtime crash.

## Refresh

Bump `@kameas-ai/kenaz-design-system` in `frontend/package.json`; if that
release renames or restructures its font assets, update the relative
paths in `src/styles/fonts.css` to match. Re-run
`npm run check:css-tokens && npm run typecheck && npm run build`.
