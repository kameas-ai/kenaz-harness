# The served-mode product boundary

Written 2026-08-14 by the frontend orphan-deletion sweep, because this
boundary is intentional and implemented but was documented nowhere outside
`docs/unwired-ledger.md` — every unwired sweep was re-finding it as if it
were a new gap. It is not a gap. This note is the fixed address; update it
in place rather than re-discovering the pattern each release.

## What it is

The harness ships in two modes:

- **Desktop (Wails)** — the default build, with the full Go core wired in
  behind `window.go.rpc.Bindings`.
- **Served** — a browser-hosted frontend with a reduced or absent backend
  surface, detected client-side by `frontend/src/lib/useServedMode.ts`
  checking for the *absence* of `window.go.rpc.Bindings`.

Six live views render `components/ui/NotAvailableInServedMode.vue` instead
of their real content when `useServedMode()` is true:

- `views/settings/SettingsView.vue`
- `views/artifacts/ArtifactsView.vue`
- `views/tools/ToolsView.vue`
- `views/contexts/ContextsView.vue`
- `views/memory/MemoryView.vue`
- `views/workflows/WorkflowsView.vue`

Each of these imports `NotAvailableInServedMode` directly and conditions
its real template on `useServedMode()`'s value — this is a deliberate
product decision (these surfaces need Wails-only capabilities: local
filesystem, OS-level bindings, or Go-only subsystems that have no served
equivalent), not an oversight or an unfinished migration.

## The other half: surfaces served mode does not route at all

Two views are excluded a step earlier — their **routes are absent from
`main-served.ts`**, so they never mount and never need the boundary panel:

- `views/sites/SitesView.vue` (`/sites`)
- `views/marketplace/MarketplaceView.vue` (`/marketplace`)

This was settled on 2026-08-16 rather than left as drift. The reason is
mechanical: the served RPC surface is the explicit allowlist in
`core/serve/methods.go`, and it carries no `Sites_*` and no `Catalog_*`
method. Every call either view makes would come back "unknown method", so
routing them in served mode would render a chrome over a dead backend —
strictly worse than not offering them. Registering the routes is therefore
blocked on a served-mode fleet RPC surface, which is a mission, not a
wiring fix.

Consequences, all three of which must move together:

- `shell/LeftRail.vue` gates both nav entries on `!isServedMode()` in
  addition to their capability predicates. Before the fleet capability
  gate was wired (see `docs/dead-code-audit-2026-08-16.md` finding A4)
  the entries never rendered anywhere, which is why this gap was
  invisible.
- `lib/useCommandPalette.ts` carries the same predicate on `nav.sites`
  and `nav.marketplace` via `PaletteAction.visible`.
- `main-served.ts` has a `/:pathMatch(.*)*` → `NotFoundView` catch-all
  (audit finding B4), so a bookmarked or hand-typed `#/sites` lands on a
  page that explains itself instead of a blank `<router-view>`.

`src/__tests__/entrypoint.routes.test.ts` diffs the two route tables and
fails if they differ by anything other than the two paths named above, so
the next route added to one entry point forces a decision about the other.

## Why it looks like unwired code if you don't know this

A naive import-graph or "is this branch ever taken" pass will flag
`NotAvailableInServedMode` usage as suspicious — a component that hides
real content behind a boolean that's always false in desktop builds reads
like dead-code plumbing. It isn't: served mode is a real, shippable build
target (see `frontend/src/main-served.ts`), and the six views above are
the current, correct list of served-mode-unavailable surfaces.

## Test-side wiring

`frontend/src/test-setup.ts:8` stubs `window.go.rpc.Bindings` so the
default Vitest environment reports desktop mode — otherwise every test
would see served mode and render the boundary panel instead of the view
under test. Tests that specifically want to exercise served-mode behaviour
mock `useServedMode` directly or use `dispatchServedEvent`.

## What would make this stale

- A new view added to the served bundle without wiring the boundary (a
  regression the ledger would need to catch, not this doc).
- `core/serve/methods.go` gaining `Sites_*` / `Catalog_*` dispatch — at
  which point the two routes above should be registered in served mode
  and removed from the allowlist in `entrypoint.routes.test.ts`.
- One of the six views listed above no longer needing the boundary (its
  served-mode gap closed) — remove it from the list here.
- `useServedMode`'s detection mechanism changing (e.g. no longer keyed off
  `window.go.rpc.Bindings`).

If any of those happen, update this file in the same change — that is
cheaper than the next sweep re-deriving the whole boundary from scratch.
