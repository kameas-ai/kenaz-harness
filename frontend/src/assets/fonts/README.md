# Bundled fonts (SIL OFL 1.1)

Geist + Geist Mono are vendored here per plan §7 v1.0 item 3 / WP03. The
woff2 files are loaded via `@font-face` rules in `src/styles/fonts.css`
and referenced through `var(--font-ui)` / `var(--font-mono)`. CSP
`font-src 'self'` (plan §4.3 invariant #1) forbids any external font
request — the binaries MUST sit alongside this README.

Expected files:

- `Geist-Regular.woff2`
- `Geist-Medium.woff2`
- `Geist-SemiBold.woff2`
- `GeistMono-Regular.woff2`
- `GeistMono-Medium.woff2`
- `OFL.txt` (full SIL OFL 1.1 text — present)

If any of the woff2 files is missing, the browser falls back to the
system fonts declared in `--font-ui` / `--font-mono`. The fallback chain
keeps the UI usable but visually drifts from Kenaz; restore the files
before any release.

## Source

Either copy from `/Users/alecfeeman/PycharmProjects/kenaz/frontend/src/assets/fonts/`
(if Kenaz is checked out locally) or download the OFL 1.1 woff2 builds
from <https://github.com/vercel/geist-font/releases>.

## Refresh

Bump procedure: replace the woff2 files in place; bump the source SHA
manifest at `frontend/src/assets/fonts/source.json` if/when one exists;
re-run `npm run check:css-tokens` and `npm run typecheck`.
