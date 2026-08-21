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

## Self-update: desktop-only

Settled 2026-08-18 by `self-update-repair-01PMUP01` §5, following the
`/sites` + `/marketplace` precedent above rather than leaving it as drift
for the next sweep to re-find. **The three `update:download-*` broker
topics are deliberately absent from `passthroughTopics`
(`core/serve/wsstream.go`)** — this is a decision, not a gap.

Three independent mechanical facts, all re-verified against this tree:

1. **The RPCs are not served.** `git grep -n "Update_" -- core/serve/`
   returns nothing. The served surface is the explicit allowlist in
   `core/serve/methods.go`; it carries no `Update_*` method. A served
   client cannot call `Update_StartDownload`, `Update_Apply`, or even
   `Update_Status`.
2. **The only surface is already served-blocked at a higher level.**
   `UpdatesPanel.vue` mounts inside `views/settings/SettingsView.vue` —
   one of the six views in the list above that already renders
   `NotAvailableInServedMode` in served mode. There is no route to
   reach the panel in a served build in the first place.
3. **The semantics are actively wrong for served mode, not merely
   unimplemented.** `core/update/service.go`'s `ApplyAndRestart` swaps
   the **server process's own binary** and restarts **that process**. A
   browser client triggering it would restart the host out from under
   every other connected user of that served instance. Forwarding
   progress frames to a client that can neither start nor apply a
   download would be a chrome over a dead backend — the same
   anti-pattern the `/sites` + `/marketplace` section above names — and
   wiring the RPCs so that button could be *clicked* would additionally
   be a capability that must not exist server-side at all, not just one
   that's missing.

If `core/serve/methods.go` ever gains a genuinely served-safe update
surface (e.g. "notify connected clients a new server build shipped" with
no client-triggerable apply), that is a new capability with its own
review — not a reason to add the existing desktop topics to
`passthroughTopics`.

## Crash reporting: desktop-only, and a correction to a mission finding

`entry-points-and-crash-reporting-01PMZD13`'s spec (finding N-2) read
`SettingsView.vue:1224`'s `v-else-if="showCrashReportingTab"` in isolation
and concluded it mounts `CrashReportingPanel` with no `!servedMode` guard,
citing `servedMode` being available at `:64` and used "elsewhere in the
file" without being applied there. **Re-verified against this tree and
found stale, in the same shape as the Self-update section above**:

1. **The RPCs are still not served.** `grep -c Sentry core/serve/methods.go`
   → 0. `Sentry_TestDSN`, `Sentry_GetLastFive` and
   `Sentry_GenerateLocalReport` are all unknown methods to a served client.
2. **The only surface is already served-blocked at a higher level, and
   was before this mission started.** `SettingsView.vue`'s own template
   root is `<NotAvailableInServedMode v-if="servedMode" .../>` /
   `<SettingsShell v-else>…</SettingsShell>` (`:1057-1063`), and
   `CrashReportingPanel`'s mount point (`:1224-1229`) is nested inside
   `SettingsShell`'s slot content, closing at `:2176` — well after it.
   `SettingsView.vue` is already the first view named in this doc's list
   above. A per-tab `!servedMode` guard at `:1224` would be dead weight:
   the tab is unreachable in served mode by construction, the moment
   `SettingsShell` itself is.
3. **No client-triggerable-only-on-the-server semantics issue here**
   (unlike Self-update's restart-the-host-process concern) — this is
   simply a desktop-only settings surface, same as `ArtifactsView` /
   `ToolsView` / `ContextsView` / `MemoryView` / `WorkflowsView` above.

**No code change was needed for N-2's guard half.** UNIT-7's real fixes
here are the two things that were genuinely wrong regardless of served
mode: the Test button's result string claimed a single unqualified
verdict for a check that only ever exercises the Go process
(`core/sentry/test_dsn.go`'s `client.Head`, which has no CSP), and now
names the process it tested plus a second, separately computed line for
whether the *renderer* SDK could transmit under the active page CSP —
see `CrashReportingPanel.vue`'s `browserCanTransmitUnderCurrentCSP`.

## The fence that catches the case none of the three mechanisms did

Added 2026-08-16 by the adversarial review of the capability-gate wiring.

Note that the boundary above is enforced three different ways depending on
the surface — an unrouted path, a `NotAvailableInServedMode` panel, or a
`!isServedMode()` guard on the affordance itself. That is fine per surface
and unreliable in aggregate: `BundlesView` had none of them. `/bundles` is a
served route, the view renders no boundary panel, and its "Publish to team"
button gates on `signedIn` alone. `AppInfo` IS in the served allowlist and
answers with the desktop process's real capability map, so the moment the
capability snapshot was actually wired, a browser client of a signed-in
harness rendered that button — over a `Catalog_Publish` served mode refuses.

So `lib/featureFlags.ts` now closes `signedIn` and `capability()` outright
when `isServedMode()` is true. There is no fleet method anywhere in the
`servedMethods` allowlist (39 entries as of served-mode-is-a-real-mode-01PMZ707
WP08, up from the 33 this line originally cited — this count moves every
time a method is ported; `TestServedMethodsCountMatchesDoc`,
`core/serve/wp08_served_count_test.go`, fails and names both citations to
update the next time it drifts), so no fleet gate can legitimately open in
served mode, and the fence needs no per-surface knowledge. Its scope is
also narrower than "everything": it closes exactly `signedIn` and
`capability()`, both FLEET gates — every finding this doc's own history
above describes (the boundary panel, the unrouted path, the per-affordance
gate) is a non-fleet RPC this fence structurally cannot see, because
`AppInfo` (which feeds it) carries no non-fleet capability information at
all. The per-surface mechanisms above stay — they are better UX than an
invisible control — but they are no longer the only thing standing between
a served build and a dead RPC.

Pinned by `src/lib/__tests__/featureFlags.servedFence.spec.ts`. When
`core/serve/methods.go` does gain a fleet surface, this fence is the first
thing to revisit.

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
