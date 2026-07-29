# Refreshing Kenaz design tokens

`tokens.css` is no longer a vendored copy. As of the `feat/design-system-adoption`
change it `@import`s `@kameas-ai/kenaz-design-system/tokens.css` directly from
the published npm package (GitHub Packages — see `../../PACKAGES_AUTH.md`).
Importing the package **is** the drift check: there is nothing to diff or
copy by hand, and nothing can silently go stale the way the old vendored
copy did (it was still pointing at a contributor's local filesystem path
from the original vendoring commit).

## When to refresh

Whenever you want the harness to pick up a new DS token release, bump the
`@kameas-ai/kenaz-design-system` version in `frontend/package.json` (and
`package-lock.json` via `npm install`). That's the entire procedure —
then run `npm run typecheck && npm run test && npm run check:css-tokens`.

## Theme model

The DS's `tokens.css` defines:

- a base `:root` block (the "ink" — near-black — palette), and
- four named themes selected via `[data-theme="ink|linen|azure|paper"]`.

The harness previously maintained its own **fifth**, bespoke light theme
(`:root:not(.dark)`) with a diverging `--ink-subtle` value (`#8C8C96`,
picked because it AA-passed on the harness's own palette) instead of the
DS's AA-checked `#6F6F78`. That divergent theme was folded/dropped in the
design-system-adoption change — see that PR's description for the
decision record. The harness now offers exactly the DS's four named
themes, nothing more:

- Standalone harness (no host embedding): `useTheme.ts`'s light/dark/system
  toggle now drives `data-theme="ink"` (dark) or `data-theme="linen"`
  (light) directly, instead of a `.dark` class paired with hand-rolled
  light-mode overrides.
- Embedded-in-workbench-chrome: unchanged — `?theme=<ink|linen|azure|paper>`
  (spec 073, `CONTRACTS.md` § `kenaz.workbench.surface-theme-param`) sets
  `data-theme` directly to whichever of the four the host chrome is using.

The `.dark` class is still synced alongside `data-theme` purely so
Tailwind's `dark:` variant keeps working for any call site that hasn't
been migrated to a token utility (`bg-surface-0`, `text-ink`, etc., which
resolve automatically per the active theme and don't need a `dark:` twin
at all — prefer that where you can).

## Future

If the DS ever ships its own `--motion-*` tokens, drop the HARNESS-LOCAL
block at the bottom of `tokens.css` and consume the DS's instead.
