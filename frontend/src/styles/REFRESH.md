# Refreshing Kenaz design tokens

`tokens.css` is vendored verbatim from the Kenaz frontend (plan §1, C-008).
The file is the harness's only source of color truth; privacy CI invariant
#4 (plan §4.3) blocks any other file under `frontend/src/` from declaring a
raw color literal.

## When to refresh

Whenever the Kenaz frontend bumps `frontend/src/styles/tokens.css`. The
canonical source path is recorded in `tokens.source.json`.

## Procedure

1. Diff the upstream Kenaz `tokens.css` against the vendored copy.
2. Copy the upstream `:root { ... }` block byte-for-byte over the vendored
   block, preserving the harness vendor header and the `HARNESS-LOCAL`
   motion fence (the harness adds `--motion-fast/-base/-slow` until Kenaz
   publishes them — see plan §5.1 footnote).
3. Update `tokens.source.json` with the new source SHA and date.
4. Run `npm run typecheck && npm run test && npm run check:css-tokens`.
5. Open an ADR follow-up if Kenaz introduced a new token group; map it
   into `tailwind.config.ts` `theme.extend` in the same PR.

## Future migration

Plan §7 v2 commits to migrating from vendored CSS to a published
`@kenaz/design-tokens` npm package once Kenaz ships one. R-1 in plan §8
calls for a CI alert when the upstream `tokens.css` hash drifts from the
vendored copy.
