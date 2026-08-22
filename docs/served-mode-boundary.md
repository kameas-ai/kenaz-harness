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

**Three mechanisms enforce this boundary, not two.** Stated up front now
(`served-mode-is-a-real-mode-01PMZ707` WP10) because stating only the
first two here and burying the third in the fence section below (this
was itself the file's own drift until this WP) is what let ten routed
views get graded against two mechanisms instead of three when this
mission started:

1. **The boundary panel** — a routed view renders
   `components/ui/NotAvailableInServedMode.vue` instead of its real
   content when `useServedMode()` is true. See the view list below.
2. **The unrouted path** — the view's route is absent from
   `main-served.ts` entirely; it never mounts, so it never needs a
   panel. See "The other half" below.
3. **The per-affordance gate** — the VIEW stays routed and renders its
   real content, but one specific affordance inside it (a button, a
   dropdown, a whole sub-panel) is conditioned on `!isServedMode()`
   while the rest of the view works normally. `ProvidersView.vue`'s
   `readOnly = computed(() => servedMode.value)` is the original example
   (write affordances hidden, read-only browsing stays); WP04 added the
   paperclip/attachments, the `/` slash menu, and branch controls inside
   the live chat surface (`ChatInput.vue`, `MessageBubble.vue`), and
   WP07 added the LeftRail memory badge. See the mechanism table below
   for the full current list — the six-view panel list further down is
   NOT exhaustive of mechanism 3's surfaces, and reading it as if it
   were is the mistake this correction exists to prevent.

### Mechanism 1: the boundary panel

Live views render `components/ui/NotAvailableInServedMode.vue` instead of
their real content when `useServedMode()` is true:

- `views/settings/SettingsView.vue`
- `views/artifacts/ArtifactsView.vue`
- `views/tools/ToolsView.vue`
- `views/contexts/ContextsView.vue`
- `views/memory/MemoryView.vue`
- `views/workflows/WorkflowsView.vue`
- `views/audit/AuditView.vue` (`served-mode-is-a-real-mode-01PMZ707` WP05
  — all eleven `Audit_*` RPCs are unrouted; the prior state rendered a
  clean empty compliance trail on a rejected read, fabricating evidence)
- `views/agentgraph/GraphsView.vue`, `views/agentgraph/GraphEditor.vue`,
  `views/agentgraph/RunView.vue` (WP03 — graph authoring/execution is
  explicitly out of scope, D-701: routing it would be new capability
  work, not a parity fix)
- `views/policy/PolicyView.vue` (WP03, same D-701 reasoning — no
  `CedarPolicy_*`/`Policy_*` serve dispatch case exists)
- `views/bundles/BundlesView.vue` (WP04, E-705 — every `Bundle_*` method
  the view calls, including the very first read on mount, is unrouted)
- `views/projects/ProjectLandingPage.vue` (WP04, E-705 — `Projects_Get`,
  the view's first read, is unrouted, and so is nearly everything below
  it: `Contexts_*`, `Attachments_*`, `Artifacts_*`, `Memory_*`,
  `ProjectSync_*`)

Each of these imports `NotAvailableInServedMode` directly and conditions
its real template on `useServedMode()`'s value — this is a deliberate
product decision (these surfaces need Wails-only capabilities: local
filesystem, OS-level bindings, or Go-only subsystems that have no served
equivalent — or, for the WP03/WP04/WP05 additions, out-of-scope
capability work / a served RPC surface that genuinely does not exist yet),
not an oversight or an unfinished migration.

**`/permissions` (`views/permissions/PermissionsView.vue`) is
deliberately NOT on this list** — decision D-710, WP05. It hosts TWO
different halves: the pending-permission-prompt queue, which genuinely
works in served mode (`Permissions_ListPending` and `Permissions_Resolve`
are both served), and the permission-MODE dial
(`components/settings/PermissionDialsPanel.vue`), which does not
(`Settings_GetPermissionMode`/`SetPermissionMode` are unrouted). Panelling
the whole view would have broken the working half; instead the dial
renders an explicit inline unavailable state on a failed read rather than
defaulting to a value that looks like a real posture (AC-713). See the
mechanism table below.

### Mechanism 2: the unrouted path

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

### Mechanism 3: the per-affordance gate

The view stays ROUTED and renders its real content; one specific
affordance inside it is conditioned on `!isServedMode()` (or an
equivalent explicit-failure-state read, noted below) while the rest of
the view works normally. This is the mechanism `check-serve-dispatch-drift.sh`
and `entrypoint.routes.test.ts` structurally cannot see — the first diffs
*methods*, the second diffs route *tables*, and neither can observe that
a routed, unpanelled view hides one button. G-702
(`served-mode-is-a-real-mode-01PMZ707` WP03) is the gate written
specifically to close that blind spot.

| Surface | Affordance | Gated in | Mission |
|---|---|---|---|
| Provider management | Add/remove/test/update provider, local-runtime autoconfigure, custom-endpoint probing | `views/providers/ProvidersView.vue`'s `readOnly = computed(() => servedMode.value)` | WP03 |
| Paperclip / drag-drop attach | 7 `Attachments_*` methods | `components/chat/ChatInput.vue`'s `multimodalEnabled` dial (now correctly defaults to hidden on a `ServedUnsupportedError`, not "assume enabled" — AC-711) | WP04 |
| `/` slash menu | `Slash_Execute`, `Slash_List` | `components/chat/ChatInput.vue` — both the autocomplete fetch AND the independent "type /foo, press Enter" send-path branch (the second one was found and closed during WP07's own caller-site pass; WP04's first landing gated only the former) | WP04, WP07 |
| Branch controls | 10 `Branches_*` methods | `components/chat/BranchSidebar.vue`, `CreateBranchModal.vue`, `ReintegrationPreviewModal.vue` (all un-mounted under served mode from `views/sessions/SessionsView.vue`), plus the "branch from this turn" button in `components/chat/MessageBubble.vue` | WP04 |
| Permission-mode dial | `Settings_GetPermissionMode`, `Settings_SetPermissionMode` | `components/settings/PermissionDialsPanel.vue` — not a literal `isServedMode()` check but the same outcome via an explicit inline-unavailable-state render on a failed read (D-710, AC-713); the pending-permission-prompt half of the SAME view genuinely works and is intentionally NOT gated | WP05 |
| Memory chunk-count badge | `Memory_HealthSnapshot`, `Memory_ListChunks` | `shell/LeftRail.vue`'s `MemoryBadge` mount — found during WP07's caller-site pass: the badge is shell chrome (not a "view"), so no per-view scan had ever looked at it, and it rendered "Loading memory count…" permanently rather than an honest unavailable state | WP07 |

`docs/unwired-ledger.md` names the affordances found gate-able but NOT
yet gated during WP07's triage (a live chat-surface caller with a
still-open port-or-gate question — artifact save/view from a message,
context-attachment management outside the paperclip, session resume,
title-clear, bash `!command`, user slash-command execution, memory
"remember this message", session/fleet handoff) — that list is the
`untriaged` class in `scripts/ci/allowlists/i15-serve-dispatch-gap.txt`,
not repeated here.

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
   one of the mechanism-1 views in the list above that already renders
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

## The session-scoped WebSocket fan-out is also a boundary mechanism

Added `served-mode-is-a-real-mode-01PMZ707` WP10 (spec §5.10 point 4).
Everything above is about which RPCs a browser can CALL. This section is
about a different question: once a served client is connected, WHICH
SESSION'S PUSHED EVENTS does it receive — and until WP01, the answer was
"every session's, unscoped."

**The defect (fixed by WP01).** `GET /ws`'s `handleWS` decoded
`Sessions_Stream`'s request but never read `params.id`, so every
WebSocket connection subscribed to the SAME process-wide event stream
regardless of which session the browser tab had open. A served client
watching session A received session B's `tool:confirm-pending`,
`elicit:pending`, all four `*:permission-pending` families, and raw
`llm:stream-chunk` model output — an authorization leak, not a UX
inconsistency (`docs/escalation-register-2026-08-19.md:2011-2022`).

**The fix, three parts, all in `core/serve/`:**

1. `handleWS` now decodes `{"id": "..."}` from the WS request and
   refuses the connection with a hard error frame if it is absent or
   empty (AC-703) — no silent fall-back to the old global behaviour.
2. `wsstream.go`'s `frameFor` takes the connection's session id and
   returns `forward=false` for a payload belonging to a different
   session, for every gate-family topic AND `llm:stream-chunk`/
   `llm:stream-closed`. A payload whose topic carries no session at all
   (e.g. `cost.threshold.crossed`, a calendar-month aggregate) is NOT
   forwarded until it does — D-705, fail closed, rather than "probably
   fine to broadcast."
3. Both reconnect snapshots (`tool:confirm-pending:snapshot`,
   `elicit:pending:snapshot`) scope to the connection's session too, so
   a served client that reloads its WS still only sees its own session's
   parked confirmations — through the real `confirm.API.ListPending`
   session-scoping branch, not a fake.

**What did NOT change:** `ConfirmToolModal.vue`'s cross-session queue on
DESKTOP is unchanged and is a documented product decision (one process,
one user, one window — a background session's parked call still needs
answering from somewhere, and the modal is the only thing that can).
Session scoping is a SERVER-side, per-connection filter; it does not
touch the desktop transport at all (AC-705 pins this: the existing
`ConfirmToolModal` test suite passes with zero edits). No method left or
joined `servedMethods` — the fan-out was never keyed off the
allowlist.

**What this does NOT answer:** served mode still authenticates with one
shared bearer token and no per-user principal
(`core/serve/server.go:80-81`). Session scoping is the right unit for the
cross-session LEAK, but it does not by itself answer whether two humans
may point browsers at the same served harness and see each other's
session LIST (as opposed to each other's push events, which is now
closed). Recorded, not resolved — E-701, owner: alec.

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
target (see `frontend/src/main-served.ts`), and the mechanism 1/2/3
lists above are the current, correct account of every served-mode-
unavailable surface — not just the panelled views.

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
- One of the panelled views listed above no longer needing the boundary
  (its served-mode gap closed) — remove it from the list here.
- `useServedMode`'s detection mechanism changing (e.g. no longer keyed off
  `window.go.rpc.Bindings`).
- A new per-affordance gate (mechanism 3) landing without an entry in
  the mechanism table above — the table exists specifically so that list
  does not read as exhaustive of mechanism 1 the way the six-view list
  alone did before `served-mode-is-a-real-mode-01PMZ707`.
- `scripts/ci/allowlists/i15-serve-dispatch-gap.txt`'s `untriaged` class
  growing instead of shrinking release over release (spec AC-717) — that
  allowlist, not this doc, is the live per-method triage; this doc
  should summarise it, not duplicate its reasoning line by line.
- `passthroughTopics` (`core/serve/wsstream.go`) or `SERVED_STREAM_TOPICS`
  (`frontend/src/lib/harnessClient.ts`) gaining a topic without the
  session-scoping question above being asked for it (D-705: a topic
  whose payload carries no session must not forward until it does).

If any of those happen, update this file in the same change — that is
cheaper than the next sweep re-deriving the whole boundary from scratch.
