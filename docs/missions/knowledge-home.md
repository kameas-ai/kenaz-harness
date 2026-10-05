# One Knowledge home — decision record

Mission `knowledge-home-01DOGF0E` (WP01). Owner ruling (2026-10-04, binding):
one **Knowledge** nav tab with **Curated** (ContextsView) and **Learned**
(MemoryView) sections; the memory switch moves out of Tools into Learned;
**stores stay separate** — an IA merge, not a data merge. The mission's
spec/plan/tasks live in the gitignored `kitty-specs/`; this file is the
committed copy of the WP01 decisions. Citations re-read against
`release/v0.87.0` @ `872c0240` (which already carries nav-ia-sweep F and
artifacts-as-units C; fleet-session-truth A is not merged).

## D1 — Route shape (FR-1, FR-2)

- `/knowledge/:section(curated|learned)`, route name `knowledge`. The
  section is a path segment, not a query param, mirroring C's
  `/library/:view(captured|authored)` so the two merged homes read the same.
- `/knowledge` → `/knowledge/curated` (default section), query preserved.
- `/contexts` → `/knowledge/curated`, `/memory` → `/knowledge/learned`,
  `/corpora/*` → `/knowledge/curated`. All are **redirects that keep the
  query string** — MemoryView reads `?scopeKind=` / `?scopeId=` (the project
  landing page's deep link) and MemoryBadge pushes `?project=`. Same shape as
  C's `/artifacts` / `/documents` redirects; identical in `main.ts` and
  `main-served.ts` (the parity test in `entrypoint.routes.test.ts` covers it).
- `/memory/:rest*` also redirects to Learned: `CrossReferenceLink.vue` has
  linked audit rows to `/memory/<chunk id>` since before this mission, and no
  such route ever existed (it fell through to the catch-all). The redirect
  lands the user in Learned instead of nowhere; deep-linking to one chunk is
  not in scope.
- A persisted `lastRoute` of `/contexts` or `/memory` from a previous release
  resolves because `restoreLastRoute` is a plain `router.replace` and the
  redirect is a route record — nothing to migrate.
- Why redirects rather than mount-within: F's `legacyRoutes.ts`
  (`redirectLegacySettingsTab`) is a `beforeEnter` guard because the old
  Settings URLs differ only by query on a route that still exists. Here the
  old *paths* stop being homes, so a `redirect` record (C's pattern) is the
  simpler, already-tested form. `legacyRoutes.ts` is not touched.

Rail: one `RailEntry` "Knowledge", icon `BookOpen` (existing set),
`to="/knowledge/curated"`, `match-prefix="/knowledge"` (F's segment-bounded
prefix). The Contexts and Memory entries are removed. Palette: `nav.knowledge`
plus `nav.knowledge.curated` / `nav.knowledge.learned` with hints that say
what each section is; `nav.contexts` / `nav.memory` are removed (their old
hint "Memory capture settings" was wrong — the view never showed the
setting).

## D2 — Section switcher

The spec asks for a generic switcher C can reuse. C already shipped its own
inline switcher (`LibraryView.vue`, `<nav data-testid="library-switcher">`).
This mission builds `components/ui/SectionSwitcher.vue` — a route-linked
tab row with the **same markup and classes** as C's — and uses it in
KnowledgeView. C's LibraryView is **not** refactored here.

**Follow-up (not this mission):** swap LibraryView's inline `<nav>` for
`SectionSwitcher` (pass `test-id-prefix="library"` so its existing
`library-tab-*` / `library-switcher` test ids survive). Owner: whichever
mission next touches LibraryView; the LibraryView header comment already
names this integration point.

Section bodies mount the existing views unchanged in behaviour with an
`embedded` prop (C's convention) that suppresses each view's own CanvasHead;
KnowledgeView owns the page head and the one-line mechanic explainer (FR-3).

## D3 — Tools memory row disposition (FR-4)

**One-release pointer, then delete.** The toggle and its `getMemory` /
`setMemory` / `installStarterMemory` / `removeStarterMemory` calls leave
`KenazToolsPanel.vue` entirely; the row becomes a single non-interactive
line, "Memory moved to Knowledge › Learned", with a link. There is no
second control (P-3). Reason for the pointer: the owner's own habit
(dogfood F6) was to look under Tools; a user upgrading finds a signpost
instead of a silently missing row. Removal trigger: the first release after
v0.87.x deletes the pointer (`data-testid="memory-moved-pointer"`); dated
2026-10-04, owner: alec.

The toggle logic moves to `views/memory/MemoryCaptureToggle.vue` with the
**same calls in the same order** (`setMemory(next)` then install/remove
starter hooks; optimistic flip with rollback on error). The stale header
comment ("unhides the Memory tab" — the rail entry was never conditional) is
deleted with the row.

## D4 — FR-7 folder-level promote: OWNER QUESTION — OPEN

Question for the owner (asked 2026-10-04, **no answer recorded yet**):

> With a context *folder* selected (e.g. `kameas-ai`), should "Share…/
> Promote…" open a batch dialog that publishes every entry in the module
> (root `context.md`/`agents.md` + files, per-entry checkboxes, per-entry
> results), or should the folder pane say "Share files individually —
> select a file"?

Facts for the call: there is no folder-level publish/promote in the API —
`ContextPublishRequest{NodeID, Layer, Kind, Title, Body}` is one entry
(`core/rpc/views/contexts/api.go`); a dialog would be a client-side batch
over per-entry `contexts.publish` / `contexts.promote`. The plan says do not
resolve this by picking the cheaper option silently.

**Disposition for this mission:** the batch dialog is **deferred, not
rejected**. WP05 does not build it. What ships instead is the honest interim
folder state the spec requires either way (P-6): with a folder selected the
sharing controls still render, disabled, with the reason "Sharing works per
file today — select a file in this folder to share it. Sharing a whole
folder is pending a product decision." This is *not* the rejection branch of
FR-7 (that would record a dated rejection in the spec); it is the
placeholder until the owner answers. Owner: alec. Whoever records the answer
either builds the dialog (WP05 as specced, pins P-6/P-7) or rewords the
interim sentence to the rejection copy and dates it.

**As shipped (WP05):** ContextsView now listens to ContextTree's
`select-folder` event (it was emitted but ignored, so a folder click only
expanded the row and `selectedPath` was never a folder — §2.3 of the spec
assumed otherwise). The last-clicked folder drives the sharing section only;
preview, "+ Folder" and import targets are unchanged. The reason names the
folder ("select a file in “kameas-ai”") and appends the fleet reason when
the cap is off. P-6 pins this interim state; P-7 (per-entry batch results)
does not apply until the dialog exists.

## D5 — Sharing reason source (FR-6)

A (`fleet-session-truth-01DOGF0A`) is not merged, so reasons derive from
`syncStatus` (`team_cap_enabled`, `client.contexts.syncStatus()`). The only
distinction `syncStatus` can honestly make is *loaded with cap off* vs
*could not be read*; signed-out vs degraded vs capability-missing needs A's
`FleetSession`. Switch-over: whichever of A/E lands second replaces
`sharingDisabledReason` in `ContextsView.vue` with the FleetSession-derived
sentences; the gate location (`teamCapEnabled`) is unchanged.

## D6 — Health chip (FR-8)

Collapsed by default; the expanded/collapsed preference is a per-device
`localStorage` key (`harness.knowledge.contextHealthExpanded.v1`), the same
mechanism LeftRail uses for its sidebar collapse. Not a backend Setting —
no migration, no settings schema change. An upgraded profile has no key, so
it gets the default (collapsed). Re-run is disabled with the reason "No
sources connected — connect a source to scan" when `connected_sources` is
empty. The "Latest run" row is relabelled "Latest bootstrap scan" — the run
is a fleet context-bootstrap scan (`core/fleet/context_bootstrap.go`
`BootstrapLatestRun`), which the old copy never said.

## WP-PI — Persistence integrity

**Ran, verbatim.** `go test -count=1 -race -short -run KnowledgeHome -v
./core/rpc/views/settings/` → 2 passed
(`TestKnowledgeHome_MemorySetting_RoundTripsFromPreviousReleaseFile`,
`TestKnowledgeHome_LegacyLastRoute_StoredVerbatim`, both seeded from the
committed `testdata/upgrade/v0.64.0/settings.json`, reopening a fresh
`FileStore` after every write). `vitest run
src/__tests__/lastRoute.knowledgeRedirect.test.ts` → 8 passed (persisted
`/contexts`, `/memory`, `/memory?scopeKind=…`, `/corpora` restored through
the real `restoreLastRoute` over both real route tables). Per WP:
WP01 — docs only, no storage. WP02 — no table, no migration, no setting;
touches the *persisted* `lastRoute` values `/contexts` / `/memory` (pinned
above). WP03 — the existing `memoryEnabled` setting via the unchanged
`Settings_SetMemory` / `Settings_GetMemory` bindings (pinned above); starter
hooks are the existing registry install/remove, semantics unchanged. WP04 —
no local storage; fleet publish/promote state is remote. WP05 — no
storage; no local publish bookkeeping was added, so AC-PI-1's SQL clause
does not apply. WP06 — one new per-device `localStorage` key
`harness.knowledge.contextHealthExpanded.v1` (not settings.json, no
schema); the upgraded-profile case (no key) is pinned to the default,
collapsed, in `ContextHealthCard.spec.ts`. No migration, no FTS index, no
sqlite table anywhere in the mission, so `TestUpgradePath` has nothing new
to cover and AC-PI-3 does not apply.

**Fixtures.** Examined and changed: `src/__tests__/corpora-redirect.test.ts`
built a hand-copied router that mirrored the redirect it claimed to test —
it now drives both real route tables. Examined and deliberately NOT
changed: the in-memory `createFakeHarnessClient` in
`MemoryCaptureToggle.test.ts` (it pins RPC call order, not storage; a
one-line comment now says so and points at the Go round trip), and the
`provide()` fake in `ContextsView.test.ts` (sharing state comes from the
remote fleet sync status; there is no local row to round-trip). jsdom's
`localStorage` is a real Storage implementation, not a stub, so the chip
preference test exercises the actual read/write path.

**Falsifiability — RUN, not predicted.** (1) Mutated `FileStore.SaveMemory`
to drop the assignment: `TestKnowledgeHome_MemorySetting_…` failed
("LoadMemory after SaveMemory(true) + reopen = false"); reverted. (2)
Removed the `/memory/:pathMatch(.*)*` redirect from both `main.ts` and
`main-served.ts`: 4 of 8 `lastRoute.knowledgeRedirect` cases failed (both
bundles, `/memory` and `/memory?scopeKind=…`); reverted. (3) Swapped the
toggle's order to install/remove hooks before `setMemory`: both P-2 order
tests failed; reverted. AC-PI-5: this mission is not the last before a
tag; no snapshot run.
