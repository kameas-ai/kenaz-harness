# Dead-code / unwired audit — 2026-08-16

**Base tree.** Every `file:line` in this document resolves against
`.claude/worktrees/int-v0.63.0`, which holds branch `main` at
`146d9e54` ("feat: v0.63.0 — model moves in the transcript, and the unwired
sweep that found what was lying"). It does **not** resolve against the main
checkout at `/Users/alecfeeman/PycharmProjects/kameas-ai/kenaz-harness`, which
is on `release/v0.59.0` (`9d9ebbce`), is 7 commits behind main, and does not
contain `docs/unwired-ledger.md` at all. Two candidate findings died purely on
that drift (see *Ledger entries these findings prove false*). Before acting on
anything here, confirm your tree is current main.

**Scope note.** 34 findings survived adversarial verification. Several are the
same defect found independently by different passes — `initFeatureFlags` was
reported three times, the Cedar `AllowAll` gates twice. Consolidated, that is
**28 distinct defects plus one clean-bill-of-health note**. Duplicates are
marked where they occur; the independent agreement is corroboration, not volume.

---

## 1. What state the tree is actually in

Broadly healthy. This is residue, not rot.

The 2026-08-14 sweep did real work: the frontend file-level orphan question is
**drained** — a BFS over 498 files found no unledgered orphan, and there are
zero dynamic-registration escape hatches in `frontend/src`
(`import.meta.glob`, `require.context`, `defineAsyncComponent`,
`app.component()` all return no matches), which is what makes static
reachability analysis sound here in the first place. Nineteen orphaned
components were checked against the ledger and every one is already recorded.

What remains falls into three shapes, and only the first is urgent:

1. **Trust surfaces pinned open.** Five Cedar gate sites are wired to
   `AllowAll{}` or `nil` under comments promising a swap that has never
   existed. A user can author, validate, save and see-as-loaded a policy that
   nothing enforces. This is the only cluster where the product actively
   misleads someone about its own safety posture.
2. **Boot-sequence gaps of one line.** `initFeatureFlags` has no production
   caller; `searchview.Config.Enabled` is never assigned; `cfg.Cedar` is
   omitted from two view constructors. In each case the producer is live, the
   consumer is written and unit-tested, and the wiring line is simply absent.
   The unit tests seed the state themselves, which is exactly why these
   shipped green.
3. **Docstrings that outlived their subject.** Roughly a dozen comments name a
   frontend consumer, an audit emit, or a component that does not exist —
   several of them naming components the 2026-08-14 sweep itself deleted. These
   mislead only the next engineer, but they are the cheapest thing in this
   document to fix and the most likely to send someone down a dead end.

The single structural lesson: **the gate that would have caught the biggest
finding is an exported-symbol scan, not an orphan-file scan.**
`docs/unwired-ledger.md:38` already registers that gate concept as I10
("exported control-flow function with zero non-test call sites") — it is
Go-only. `initFeatureFlags` would have tripped a `frontend/src` equivalent on
day one.

---

## 2. Findings, ranked

Ranked by whether a user or the model can be misled, then by cost to fix.
Disposition classes are quoted from CLAUDE.md's "Disposition: delete vs. finish"
rubric.

### A. A user is told something false about safety, policy, or consent

---

#### A1 — Three Cedar gate sites are hardcoded `AllowAll` under comments promising a swap that does not exist
*(reported twice: as the websearch site alone, and as the full three-site set)*

**User sentence.** Write a Cedar policy forbidding the harness from writing to
memory, from using a particular model, or from fetching a blocked host during a
web search, and the harness ignores it. The editor validates it, shows it as
loaded, and the check stays pinned open.

**Evidence.**
- Memory writes — `core/rpc/api.go:1300` `gs.SetGate(&memoryGateAdapter{gate: cedar.AllowAll{}})`, under the comment at `:1295-1299` ("a future engine-load path swaps in a real Cedar engine without touching this wiring"). `SetGate` has exactly one non-test call site: this one.
- Model selection — `core/rpc/api.go:3670` `cedarGuard := cedar.NewLLMPolicyGuard(cedar.AllowAll{})`, passed as `Policy:` into `llmregistry.New`. The guard is genuinely consulted: `core/policy/cedar/llmguard.go:43-53` evaluates `ActionModelSelect`; `core/llm/registry/registry.go:321,445-452` calls it as step 3 of the pipeline. `core/llm/registry` exposes no policy setter — `Options.Policy` at `registry.go:46` is the only door.
- Websearch network — `core/rpc/builtins_wiring.go:554-556` `func constructWebSearch()` takes no engine and hardcodes `PolicyGate: cedar.AllowAll{}`; sole caller `:98`. Its sibling 300 lines earlier does it correctly: `:267-269` `var webFetchGateEngine cedar.Gate = cedar.AllowAll{}; if cedarEngine != nil { webFetchGateEngine = cedarEngine }`. `cedarEngine` is in scope at the websearch call site; it is simply not passed.
- A real engine builder exists and is used four times — `buildCedarGate` at `core/rpc/api.go:7131`, called at `:1148`, `:1588`, `:2627`, `:5681`. `:1148` is 150 lines above the pinned memory gate **in the same constructor**.
- The shipped bundle defines all three actions: `core/policy/cedar/policies/default_policy.cedar` carries `action == Action::"network_request"`, `"model_select"`, `"memory_write"`.
- The doc asserts the opposite: `docs/production-wiring.md:82-85` says production "swaps in a real `Engine`", and the fire-site table at `:89`/`:91`/`:92` lists all three as *Wired in*.
- The user's override path is reachable: `PolicyView.vue` is routed at `/policy` in both `frontend/src/main.ts` and `frontend/src/main-served.ts`; `views/policy/CedarEditor.vue` calls `validatePolicy` (:219), `savePolicy` (:233, :307), `reloadPolicies` (:336); the editor is on by default (`core/rpc/api.go:363`).

**Disposition.** **Wire** — "Spec it and finish it", class *trust-/compliance-relevant (permissions, denials)*, compounded by *its absence makes something else lie*.

**Cheapest honest fix.** `buildCedarGate(c.DataDir())` at `api.go:1300` and `:3670`; thread `cedarEngine` into `constructWebSearch()` exactly as `builtins_wiring.go:267-269` already does for `web_fetch`. Correct the three in-source comments and `docs/production-wiring.md:83-85` in the same commit. **One caveat that makes this more than a parameter:** the two search backends bypass the gate entirely — `core/tools/websearch/duckduckgo.go:88-95` and `wikipedia.go:79-86` build and issue requests with no `CheckNetwork` call — so query traffic stays ungated even after `constructWebSearch` receives the engine. Escalate only the `DefaultDeny` posture question (`api.go:7137-7145` deliberately keeps `DefaultDeny: false`); the wiring is not a product call.

---

#### A2 — The workflow and scheduled-chat Cedar gate family is called with a nil engine at every site, and its strict-mode attribute has no producer anywhere

**User sentence.** The harness ships an installable policy meant to stop it from
saving or running workflows that execute shell commands. The workflow surface
never consults any policy, and the "strict mode" that policy branches on does
not exist anywhere in the app.

**Evidence.**
- `core/rpc/api.go:1799-1806` constructs `workflowsview.New(Config{Engine, Catalog, Publisher, Disabled, Store, Scheduler, WorkflowCatalog})` — `Cedar` and `CedarMode` absent, so nil and `""`. `core/rpc/api.go:1990-1992` constructs `scheduledchatview.New(Config{Store: chatStore})` — `Cedar` absent.
- No second producer: `workflowsAPI =` / `scheduledChatAPI =` each appear once outside tests; the nil-safe getter fallbacks at `:6535`/`:6610` pass an empty Config.
- The fields are read: `core/rpc/views/workflows/impl.go:150,154` declared, consumed at `:303` `GateWorkflowRun` and `:484` `GateWorkflowSave`; `core/rpc/views/scheduledchat/impl.go:40` read at `:65`, `:105`, `:144`, `:196`.
- Nil gate ⇒ unconditional permit: `core/policy/cedar/hooks.go:511-534` — `if g == nil { return Decision{Outcome: Allow, ..., Reason: "no engine wired (default-allow)"} }`. Identical blocks at `:545-556` and `:566-577`.
- `CedarMode` has **zero producers repo-wide** — four hits total, all in `workflows/impl.go` (decl, doc, two reads), plus two test-only lines. Nothing in `frontend/src` matches `cedarMode`. So the strict arm of `core/policy/cedar/policies/default_workflows_policy.cedar` is unreachable, and its header at `:10-14` asserting "the chassis sets 'strict' when the user has opted in via the Settings → Workflows panel" describes a panel that does not exist.
- **Correction to an earlier framing:** this template does *not* ship active. `core/policy/cedar/engine.go:25-26` embeds only `default_policy.cedar`; the workflows policy is a template in `PoliciesFS` (`core/policy/cedar/embed.go:13-14`) that a user installs via `core/rpc/views/cedarpolicy/impl.go:328-344`, after which `LoadFromDisk: true` would load it. The defect is unchanged — an installed template is still never consulted — but the blast radius is "users who installed it", not "everyone".

**Disposition.** **Wire** — same class as A1, one layer out. Land them together; they are one defect class and one mission.

**Cheapest honest fix.** `Cedar: buildCedarGate(c.DataDir())` at `api.go:1799` and `:1990`. Then either build the strict-mode dial or delete the strict arm of `default_workflows_policy.cedar` plus the header claim at `:10-14`. Do not leave a shipped policy template asserting a posture with no producer.

**Gate owed.** Nothing today inspects a `cedar.Gate`-typed Config field for a non-nil production assignment. Against a surface this small (9 `Gate*` helpers, 2 view Configs) that check is constructible, with a planted-violation proof in `scripts/ci/gates_can_fail_test.go` per the sweep rule. Note `check-no-unwired-gates.sh`'s I10 vocabulary cannot see this: all nine helpers *have* call sites; the defect is a nil argument *at* the call.

---

#### A3 — The "Enable cross-session search" privacy toggle is inert, and the audit trail the same screen promises is never emitted

**User sentence.** Turning off "Enable cross-session search" in Settings changes
nothing — ⌘F still queries your whole message index — and the search-activity
log that screen promises has never recorded a single entry.

**Evidence.**
- `core/rpc/api.go:6799-6811`: `cfg := searchview.Config{}` then only `cfg.ArtifactsDB` (:6801), `cfg.CorpusDB` (:6802), `cfg.MemoryStore`, `cfg.AuditLister` (:6809) are assigned before `a.searchAPI = searchview.NewManagerAPIWithConfig(rawDB, cfg)` (:6811). `cfg.Enabled` and `cfg.Audit` are never set.
- `searchAPI` has exactly five non-test occurrences (`:487` decl, `:6789`, `:6790`, `:6811`, `:6812`). No `SetSearchAPI`. `NewManagerAPIWithConfig` has one call site.
- Consumer: `core/rpc/views/search/impl.go:113` `m := &managerAPI{db: db, audit: cfg.Audit, enabled: cfg.Enabled}`; `:286` `if a.enabled != nil && !a.enabled() { return nil, nil }` never fires; `:302-304` `if a.audit == nil { return }` — `search.executed` is never emitted.
- The dial has no reader: `core/rpc/views/settings/api.go:239-246` (decl + doc) and `:683-686` `func (s Settings) SearchEnabled() bool { return !s.SearchDisabled }` — zero callers.
- The surface is live and both sentences of its copy are false: `frontend/src/views/settings/SettingsView.vue:1548-1555` renders `data-testid="search-enabled-toggle"` bound to `toggleSearchEnabled` (:933-941), under copy at `:1557-1565`: *"Turning this off short-circuits the search and never touches the index"* and *"Search activity is logged with a truncated query_hash"*.
- Reachable, not a dead panel: `core/rpc/bindings.go:2678,2690` → `harnessClient.ts:3892-3894` → `components/search/SearchPalette.vue` (the ⌘F palette).

**Disposition.** **Wire** — *trust-/compliance-relevant (audit)*, compounded by *its absence makes something else lie*.

**Cheapest honest fix.** Two lines at `core/rpc/api.go:6799-6810`: `cfg.Enabled = func() bool { ... SearchEnabled() }` and `cfg.Audit = <the emitter already in scope for cfg.AuditLister>`.

**Gate owed.** Invisible to `check-knob-coverage.sh` because `settings.Settings` sits outside `core/wiring/knobcoverage` — already ledgered at `docs/unwired-ledger.md:693-710`.

---

#### A4 — `initFeatureFlags` has no production caller, so every fleet capability gate is permanently false — and Settings contradicts itself on one screen
*(reported three times, by three independent passes, in two trees)*

**User sentence.** Sign in to a fleet subscription and Settings → Account shows
you signed in while Settings → Sync on the same screen tells you to "Sign in to
fleet". The Publish-to-team buttons on Workflows, Bundles and Slash Commands
never appear, and the Sites and Marketplace nav sections never show up at all.

**Evidence.**
- `frontend/src/lib/featureFlags.ts` (read whole): `const _appInfo: Ref<AppInfo|null> = ref(null)` at `:31` is module-private; its **only** writer is `initFeatureFlags` at `:40-42`. `signedIn` (`:51-56`) returns false when `capabilities` is falsy; `capability(key)` (`:74-76`) returns `=== true` against a null map.
- **Zero non-test callers, re-verified in this pass.** `/usr/bin/grep -rn "initFeatureFlags\|useFeatureFlags" frontend/src` filtered of `__tests__` and `.spec.` returns seven lines, all inside `featureFlags.ts` itself — four comment lines (`:13`, `:14`, `:30`, `:34`), the declaration (`:40`), and `useFeatureFlags` (`:79`, `:83`). Every other hit in the tree is a test.
- The escape hatch is closed too: `useFeatureFlags()` at `:83-89` returns the raw writable ref, so a consumer *could* have written `_appInfo` without the setter. It has no consumer. `_appInfo` is provably never written in production.
- The file's own docstring at `:12-15` asserts the invariant nothing enforces: *"populated by `initFeatureFlags(client)` at app boot. Components that need capability gating call `initFeatureFlags` from `onMounted`."* No component does.
- The boot path holds the data and drops it: `frontend/src/main.ts:198` `const info = await client.appInfo();` inside the Sentry IIFE, consumed only for `info.build` / `info.commit`. `main-served.ts` never fetches AppInfo at all.
- The producer is live on both transports: `core/rpc/api.go:6503` `info.Capabilities = capView.Enabled` (guarded on `capView.Source != "default-deny"`), fed by `core/rpc/views/settings/fleet.go:593` and the poller started at `fleet.go:96-99` inside `SetFleetClient`, whose sole production caller is `core/rpc/api.go:1253`. Served mode dispatches the same call at `core/serve/server.go:541-542`. The wire type exists at `frontend/src/lib/types.ts:697,731,737`.
- Fleet is **on by default**: `core/fleet/flags.go:17-26` — `Disabled()` returns false when `HARNESS_FLEET_DISABLED` is unset. Default-build defect.
- The wiring never existed: `git log -S "initFeatureFlags" -- frontend/src/main.ts frontend/src/main-served.ts frontend/src/App.vue` is empty.
- **The self-contradiction.** Both panels mount in the same view (`SettingsView.vue:33,1166` AccountPanel; `:38,1197` SyncPanel) and read different sources. `AccountPanel.vue:43` `const signedIn = await client.settings.fleetSignedIn();` — a live RPC (`fleet.go:377`), so Account correctly shows signed-in. `SyncPanel.vue:172` reads the never-fed module ref and renders *"Sign in to fleet to enable cross-device settings sync. Go to Settings → Account to sign in."* — pointing the user at the panel that already shows them signed in. The Sync tab is a plain query switch with no capability gate (`SettingsTabs.vue:119`, `SettingsView.vue:172-175`).
- The other dead consumers: `shell/LeftRail.vue:992` (Sites nav, `signedIn && capability('sites_hosting')`) and `:998` (Marketplace nav); `SlashCommandsView.vue:46` `canPublishToTeam`; `WorkflowsView.vue:713` and `BundlesView.vue:197` publish buttons; `MarketplaceView.vue:129` → permanent *"Sign in to fleet to access the team catalog."*; `CedarEditor.vue:29`.
- **Two shipped views are reachable by nobody.** Route/nav references to `/sites` and `/marketplace` in `frontend/src` total six lines: `main.ts:135,136,142,143` and `LeftRail.vue:995,1001`. No `router.push`. `lib/useCommandPalette.ts:26-39` enumerates eleven `nav.*` actions; neither is among them.
- Nothing refreshes after sign-in either: `harnessClient.ts:2090,2092` `fleetRefreshCapabilities` has zero non-test callers.

**Disposition.** **Wire** — "built but unwired" (live producer, never-fed consumer). Do **not** delete: SitesView, MarketplaceView and SyncPanel are finished surfaces over live backends, and `lib/capability-keys.ts` is codegen-gated by `scripts/ci/check-codegen.sh`.

**Cheapest honest fix — and it is not one line.** Three parts:
1. `initFeatureFlags(info)` after `main.ts:198`, plus a `client.appInfo()` + `initFeatureFlags(...)` in `main-served.ts`.
2. **Re-init after sign-in.** `AccountPanel.vue:97` `identity.value = await client.settings.fleetSignIn();` mutates fleet state long after boot. A boot-only call leaves a user who signs in mid-session fully gated until restart — a subtler version of the same lie.
3. A regression test **that asserts from the entry point**. The existing suite is what let this ship: every spec seeds `_appInfo` itself (e.g. `shell/__tests__/LeftRail.sites-nav.test.ts:100` calls `initFeatureFlags(makeAppInfo({ sites_hosting: true }))` then asserts the nav item exists — a green test for a permanently invisible link).

Fix B4 (served catch-all) in the same PR: wiring this gate makes `LeftRail.vue:995,1001` visible in served mode, where neither route exists.

**Severity is contested.** One pass called it P0, one P1. It fails **closed** — it hides capability, never grants it — which argues P1. Against that: fleet is on by default, so every signed-in user on a default build hits it, and the copy actively tells a signed-in user to sign in. Someone with product context on how much of the paid tier routes through these seven surfaces should make the call.

---

#### A5 — Every MCP server row's Edit button opens a form whose Save always throws, and Test Connection contacts nothing

**User sentence.** Every MCP server in your tools list has an Edit button.
Clicking it opens a filled-in form that refuses to save with "not yet
implemented", and the Test Connection button prints a canned line without
contacting the server.

**Evidence.**
- `frontend/src/views/tools/KenazToolsPanel.vue:1247` renders an unconditional row-actions div inside the `v-for="listing in visibleRecipes"` loop opened at `:1120`, with `@click="openEditModal(listing)"` at `:1252`. No `v-if` guards it.
- `openEditModal` at `:561-564` sets `addModalEditRecipe` and opens the modal, bound at `:1383-1387`.
- `AddMCPServerModal.vue:45` `const activeTab = ref<TabId>(props.editRecipe ? 'custom' : 'registry');` — Edit lands directly on the Custom tab, rendered at `:147-152`.
- `CustomRecipeTab.vue` `save()` at `:146-163`: comment `// BACKEND GAP: client.mcp.saveCustomRecipe does not exist yet (WP10).` at `:152`, then `throw new Error(...)` at `:156`, caught into `saveError` at `:159`.
- `testConnection()` at `:125-143` sets a canned string at `:135-137` with no `await` inside the `try`, so the `catch` at `:138` is unreachable.
- **Correction to an earlier report:** `testRecipe` is *not* missing. `harnessClient.ts:1610` declares it and `:3435` live-wires it to `MCP_TestRecipe`, which exists at `core/rpc/bindings.go:499`. The component's "backend testRecipe RPC not yet available" comment is stale; the real constraint is that `MCP_TestRecipe` takes a recipeID and cannot test an unsaved draft.
- Mount chain: `ToolsView.vue:74` → `KenazToolsPanel.vue:1383` → `AddMCPServerModal.vue:147`. ToolsView is routed in both entry points.

**Disposition.** **Escalate, then finish** — either hide the Custom tab **and** the per-row Edit button until `saveCustomRecipe` exists, or land the WP10 backend. Do not delete the component.

**Cheapest honest fix.** Hiding the tab alone is insufficient: the per-row Edit button is a second, more prominent entry point. Both must go behind the same flag.

---

#### A6 — MCP "Paste config" import reports success, and the desktop build never reads what it wrote

**User sentence.** In the desktop app you can paste an MCP server config, click
Import, and see "Imported successfully" — but the server never appears in your
tools list.

**Evidence.**
- Write + toast: `frontend/src/views/tools/PasteConfigTab.vue:148` calls `importClaudeDesktopConfig` with `dry_run:false`, `:153` sets `importSuccess`, rendered as "Imported successfully." at `:330`. Backend writes to `<DataDir>/mcp/recipes/_imports/<id>.{yaml,json}` per `core/rpc/views/mcp/import.go:10-13`.
- **Correction to the original finding, which was materially wrong.** The claim "`CatalogWithUserRecipes` has zero callers" is false — that grep was scoped to `core/` and missed two production callers: `main.go:376` (inside `runServeMode`, declared `main.go:329`) and `cmd/harness-served/main.go:123`, both passing `Catalog: connectors.CatalogWithUserRecipes(dataDir, log)` into `connectors.NewSupervisor`. `core/mcp/connectors/catalog.go:29-41` constructs `recipes.NewUserStore` and calls `store.Load()` per snapshot. **The reader exists and is wired in served mode.**
- What survives: (a) the desktop/Wails path never wires it — `core/rpc/api.go:1156-1160` `mergedCat := recipes.NewMergedCatalog(..., nil, // user source wired by WP10 boot sequence)`; (b) the **Tools list** has a nil user source in *both* modes — `mergedRecipeCatalog()` at `api.go:3266-3273` passes nil as the third source and is used at `api.go:3387` and via `importCatalogReader` at `:3244-3250`, wired at `:1168`; (c) `UserStore.StartWatch` (`core/mcp/recipes/user.go:293`) has zero production callers, so no import is ever picked up live.
- The docstring at `core/rpc/views/mcp/import.go:15-19` is false in both modes: the collision check runs through `importCatalogReader` → `mergedRecipeCatalog` (nil user source), so it never sees prior imports, and no watcher is started anywhere.

**Disposition.** **Finish** — the reader half is written, tested, and already wired in served mode; only the desktop chassis wiring is missing.

**Cheapest honest fix.** Pass a UserStore-backed source at `core/rpc/api.go:1159` and in `mergedRecipeCatalog` (`api.go:3270`) so the Tools list resolves imports in both modes; correct the watcher claim in the `import.go` docstring.

---

#### A7 — The Update Install button can never succeed

**User sentence.** Clicking Install on an available update always fails with an
error, on every platform, and the "click ⬆ to install" toast points at a button
that does not exist anywhere in the app.

**Evidence.**
- `frontend/src/lib/updateClient.ts:110-118` — `installLatest` is `await bridge().Update_StartDownload(); await bridge().Update_Apply();`.
- `core/rpc/views/update/impl.go:177` sets `m.hasStaged = false` **before** calling `m.svc.Download` (:180) and spawning `go m.drainProgress(...)` (:184). `core/update/service.go:248-258` returns immediately behind a 32-slot channel with `go s.downloadPump(...)`. `hasStaged = true` is set only on the final tick inside `drainProgress` (`impl.go:216`). `Apply` returns `ErrNothingStaged` when `!hasStaged` (`impl.go:254-256`). The second `await` always throws.
- The repeat-click escape is closed: while in flight `StartDownload` returns `ErrDownloadInFlight` (`:163-166`) and throws before `Apply`; once staged, `StartDownload` re-clears `hasStaged` (`:177`).
- The advertised affordance does not exist: `composables/useEventToasts.ts:161` pushes *"Kenaz {ver} is available — click ⬆ to install."* and a repo-wide search for `⬆` in `frontend/src` returns only that line. There is no update banner or rail affordance — `frontend/src/components/updates/` contains only `useUpdateStore.ts` and `__tests__`.
- Sole production caller: `views/settings/UpdatesPanel.vue:148` inside `onInstallAvailable`, which renders the thrown error into `errorMessage`. Mounted at `SettingsView.vue:1065`.

**Disposition.** **Wire** — *the only surface for a real capability*, trust-relevant (self-update on a desktop app).

**Cheapest honest fix.** Make `installLatest` await completion — subscribe to `update:download-complete` / `update:download-failed`, or poll `Update_Status` until `downloadState === 'staged'`. That is the same subscriber A8 needs; land them together.

**Gate owed.** No existing check pairs an RPC's async contract with its caller's await sequence.

---

#### A8 — The three update-download broker topics publish into a void, and the docstring names a listener and a component that do not exist

**User sentence.** While the app downloads an update there is no progress bar
and no failure message anywhere.

**Evidence.** `core/rpc/views/update/api.go:89-91` asserts *"Frontend's update banner / settings panel listen on these via the existing useEventStream composable."* The literal `update:download` appears on exactly three lines tree-wide, all definitions — `api.go:95`, `:98`, `:101`. Zero subscribers in either language. Absent from `core/serve/wsstream.go:129-162` `passthroughTopics`, so served mode never forwards them. They *are* published in production: `impl.go:202-206` (progress), `:222` (complete), `:239` (failed). The mirrored status fields are equally unrendered — `downloadState`/`downloadProgress` appear in `frontend/src` only as a type (`updateClient.ts:34-35`) and two default literals (`updateClient.ts:144`, `useUpdateStore.ts:137`). The "update banner" the docstring names does not exist.

**Disposition.** **Wire**, in the same commit as A7 — they are the same missing subscriber. Delete is wrong: the emit path is exercised by `core/rpc/views/update/impl_test.go`. Downgraded to P2 because with Install unable to reach Apply at all, this is the second-order half of one defect.

---

#### A9 — Guided onboarding never delivers its system prompt, and marks itself done anyway

**User sentence.** On first launch you pick "Set me up for code work", the
harness opens an empty chat with that name, none of the promised setup guidance
reaches the assistant, and the first-run dialog is marked complete so you are
never offered it again.

**Evidence.**
- `core/rpc/onboarding_wiring.go:117` docstring: *"It spawns a kind=onboarding session with the chosen starter's system prompt."* The function body `:123-144` is `name := starter.Title` (:130) then `a.sessionMgr.CreateWithKind(ctx, name, nil, session.SessionKindOnboarding)` (:134-139). `starter.SystemPrompt` is never referenced — re-verified in this pass.
- The field's only non-test references tree-wide are its declaration (`core/mcp/builtin/harness/starters.go:25`) and two parser writes (`:103`, `:117`).
- A delivery mechanism exists and is unused: `core/session/manager.go:580` `SetSystemPrompt` is live and is called by the ordinary new-session path (`frontend/src/shell/NewSessionDialog.vue:230`).
- One-shot: `core/rpc/views/onboarding/impl.go:359-361` calls `MarkOnboardingCompleted` right after `StartOnboardingSession`.
- No downstream injection by kind: `SessionKindOnboarding` has exactly two non-test occurrences (`core/session/migrations_kind.go:36` definition, `onboarding_wiring.go:138` use).
- Surface is live: `frontend/src/App.vue:116-117` opens it on `state.firstRun`, `:136-140` renders `OnboardingDialog`; `views/onboarding/OnboardingDialog.vue:167` calls `restartPhase2({ starterId: s.id })`.

**Disposition.** **Finish** — *the backend is live and only the delivery step is missing* plus *its absence makes something else lie*. Do not delete; this is the only first-run surface in the product.

**Cheapest honest fix — but sequence it.** The one-liner is persisting `starter.SystemPrompt` via `SetSystemPrompt` at `onboarding_wiring.go:134`. **Do not land it alone.** The prompt it would deliver (`core/mcp/builtin/harness/onboarding/code.md:15-23`) names five `harness_*` tools, one of which (`harness_write_propose_cedar_policy`, `code.md:23`) no longer exists — the 2026-08-14 sweep deleted it — and the other four belong to an MCP server that is never attached to any session (see B10). Fixing A9 in isolation converts a dead prompt into a live model-facing lie.

---

#### A10 — The telemetry onboarding modal tells every user, including Team/Enterprise, that Aggregate and Full "Require Pro+/Team+"

**User sentence.** On first launch the telemetry consent dialog greys out
Aggregate and Full and stamps them "Requires Pro+" / "Requires Team+" for
everyone — including customers who already pay for those tiers — while the same
three options are freely selectable in Settings.

**Evidence.** `components/onboarding/TelemetryOnboardingModal.vue:47` declares `accountTier?: 'pro'|'team'|'enterprise'|''` with default `''`; `TIER_RANK[''] = 0` and the two gates at `:72-77`. `App.vue:141-144` renders `<TelemetryOnboardingModal v-if="telemetryOnboardingOpen" @close=... />` — **no `:account-tier` binding**, and no other mount exists. Shown to every user with no fleet-enrolment gate: `App.vue:124` `if (!s.hasSeenFleetTelemetryOnboarding) telemetryOnboardingOpen.value = true;`. Badges at `:188` ("Requires Pro+") and `:215` ("Requires Team+") with `:disabled="!aggregateAllowed"` / `!fullAllowed`. The component's own TODO concedes it at `:29`. The dependency exists and is live: `harnessClient.ts:2088` `fleetCapabilities()` wired at `:3610` to `Settings_FleetCapabilities`, which returns tier.

**Severity note.** Downgraded from the original P1: the sibling surface `views/settings/FleetTelemetryPanel.vue` is entirely ungated (its only `disabled` is `:disabled="saving"` at `:88`), so every tier remains selectable in Settings. The harm is a false entitlement claim in a first-run dialog plus two surfaces disagreeing — not a lockout. The "None" copy is honest ("No telemetry is sent", `:163-165`).

**Disposition.** **Finish — one prop, or delete the gate.** Either pass the resolved tier from `App.vue`, or remove the entitlement gate so the modal matches `FleetTelemetryPanel`. Shipping the disagreement is the outcome to avoid.

---

#### A11 — `Permissions_ListPending` has no caller, so a permission prompt lost to a reload stalls the turn for five minutes and then fail-closed denies

**User sentence.** If the app reloads or reconnects while the assistant is
waiting for you to approve something, the approval box never comes back — the
request hangs silently for five minutes and is then refused.

**Evidence.** `core/rpc/views/permissions/api.go:97-100` asserts *"The frontend uses this to reconcile its modal queue on app start / after a hot reload."* The binding exists (`core/rpc/bindings.go:781-784`); `Permissions_ListPending` has zero hits in `frontend/src` and zero in `core/serve`, so served mode cannot call it either. The consequence: `core/policy/cedar/prompt.go:45` `PromptTimeout = 5 * time.Minute` with fail-closed `SourceTimeout` (`:481-482`).

**The strongest evidence is the sibling comparison.** The only other `*Pending` bindings are `Elicit_ListPending` (`bindings.go:2936`) and `Confirm_ListPending` (`:3007`), and **both are consumed on mount** — `components/dialogs/AskUserQuestion/AskUserQuestion.vue:106` and `components/chat/ConfirmToolModal.vue:246`, whose header comment at `:18` spells out exactly this reload-rebuild contract. The permission family is the one interactive gate with no rehydration path.

**Disposition.** **Wire** — *trust-/compliance-relevant (consent)*. Call `ListPending` from the permission-modal store on boot/reconnect, copying the `ConfirmToolModal.vue:246` pattern two directories away.

**Distinct from the known "inert permission gates" item** (`EvaluateToolGate` never called). That is about gates never running; this is a prompt that *did* run losing its UI.

---

### B. Only an engineer is misled — but cheaply fixable, and several will mislead the next implementer badly

---

#### B1 — ⌘K is bound by two independent window listeners; both palettes open and the command palette paints over the search palette

**User sentence.** ⌘K stacks two overlays, and the search box the app documents
as the primary ⌘K surface is hidden behind the command list.

**Evidence.** Listener A: `shell/Shell.vue:131-136` inside `onGlobalKeydown`, registered at `:158-160`. Listener B: `lib/useCommandPalette.ts:85-94` `onKey`, registered at `:97-101` behind the module-level `installed` flag (`:83`). `preventDefault()` does not stop a sibling listener on the same target. Layering: neither component uses `<Teleport>`; both paint at `z-50` (`CommandPalette.vue:63`, `SearchPalette.vue:284`); `App.vue:132-133` renders `<Shell />` before `<CommandPalette />`, and `<SearchPalette />` lives inside Shell's root fragment at `Shell.vue:259`. Equal z-index plus later DOM position means the command palette wins — contradicting the app's own comment at `Shell.vue:260-261` ("the ⌘K palette is the primary entry point").

**Correction.** An earlier report claimed `UserMenu.vue` still advertises a Search row. That is a misread: `UserMenu.vue:5` is a historical changelog line ("v0.20.0: non-account rows … have moved to the OS native menu bar") and is accurate.

**Disposition.** **Finish** — dead-control wiring. Pick one owner for ⌘K. **Do not fix by z-index** — two overlays would still both be open.

---

#### B2 — The branch-advisor master switch is read by nothing, so a feature documented as default-OFF ships permanently ON

**User sentence.** The branch-suggestion banner in the chat box is documented as
off by default with a master on/off switch. The switch is read by nothing and
the banner is always live.

**Evidence.**
- `core/rpc/views/settings/api.go:190-193`: *"BranchAdvisorEnabled is the master on/off for the branch advisor (FR-010). When false the banner never mounts, regardless of confidence score. Default false."* Go-side hits are only `:190` and `:193` — declaration and doc, **no reader**. Frontend hits are only `components/settings/BranchAdvisorSettings.vue:47,61` and the type at `lib/types.ts:870`.
- The live consumer ignores it: `components/chat/ChatInput.vue:174-181` reads only `branchAdvisorMinConfidence`; `runAdvisorDetector` (`:184-215`) gates on `props.streaming`, `sessionAdvisorDismissed`, the regex signal set, and the threshold — never the master switch.
- It genuinely fires: `ChatInput.vue:149` `const ADVISOR_CONFIDENCE = 0.9` vs `DefaultBranchAdvisorMinConfidence = 0.85` (`settings/api.go:869`); banner renders at `ChatInput.vue:1202-1203`.
- Token-budget sibling: `settings/api.go:209-212` documents a budget whose accessor `EffectiveBranchReintegrationMaxTokens` (`:893-905`) has zero callers; `core/rpc/views/branches/impl.go:473-476` hardcodes `const maxTokens = 2000`. The docstring's "reintegration model" is also false — `ProposeReintegrationSummary` (`impl.go:438-490`) concatenates the last ≤8 assistant rows, rune-truncates, and returns `Model: "rule_based"` (`:489`). `EffectiveBranchAdvisorDefaultModel` (`:910-914`) likewise has zero callers.
- **A third unenforced invariant:** `api.go:195-196` claims "Range [0, 1]; Save rejects values outside this range". `core/rpc/views/settings/impl.go` contains exactly three validators (`:160`, `:181`, `:254`); none covers these fields. There is no Save-time validation.

**Severity split.** The token half is P3-grade on its own — `BranchAdvisorSettings.vue` is **not mounted**, so no user can set either value, and the hardcoded 2000 happens to equal the documented default. The P2 rests on the always-on banner and on the ledger naming the wrong blockers (see §5).

**Disposition.** **Wire, and wire before mounting.** CLAUDE.md is explicit: *"Mounting a panel whose Go knob is inert just moves the lie from the backend to the UI: wire the consumer first, in the same PR."*

**Cheapest honest fix.** Read `branchAdvisorEnabled` in `ChatInput.vue`'s `runAdvisorDetector` gate (`:196`) alongside the existing checks; replace `branches/impl.go:475`'s `const maxTokens = 2000` with `EffectiveBranchReintegrationMaxTokens()`; correct the `api.go:209-211` docstring; enforce or delete the `api.go:195-196` "Save rejects" claim.

---

#### B3 — Local OTel exporters write three SQLite tables with no reader and no retention sweep

**User sentence.** Every log line, metric, and trace the app produces is written
into your local database and never deleted, and no screen ever shows them.

**Evidence.** Producers: `core/telemetry/exporter_local_span.go:183`, `exporter_local_metric.go:87`, `exporter_local_log.go:184` — the three `INSERT INTO telemetry_{spans,metrics,logs}` sites. Unconditional in production: `core/telemetry/telemetry.go:139-150` builds all three before any `if cfg.OTLPEndpoint != ""` branch; `core/core.go:355` calls `initTelemetry()` inside `Start`, which sets `InstallSlogBridge: true` with no consent or settings gate; the bridge's own comment at `telemetry.go:329-332` states each slog line lands in `telemetry_logs`. No reader: outside `core/session/migrations_telemetry.go:25-69` (DDL) and `_test.go` files, the three table names appear only in the three INSERTs — zero non-test SELECT, zero `frontend/src` hits. No retention: no `DELETE FROM telemetry`, no `VACUUM` anywhere in `core`; the only retention mention is the unlanded plan at `core/telemetry/instance.go:7-10` ("WP03 lands retention sweep + per-span attribute allowlist").

**Lie class corrected.** This is **not** a consent problem. The modal says "No telemetry is sent" and the settings panel says "Nothing is sent to the fleet endpoint"; both stay true because nothing local is transmitted. The defect is unbounded local growth of the unified DB.

**Disposition.** **Escalate, then finish.** Whether local telemetry persistence is a wanted feature (a future trace viewer) is a product call. Under either answer the retention sweep named at `instance.go:9` is owed. Do not delete the exporters on orphan grounds.

---

#### B4 — Served-mode router has no catch-all route

**User sentence.** In the browser-served build, a stale or mistyped link gives
you a completely empty page instead of a "page not found" message.

**Evidence.** `frontend/src/main.ts` ends its table with `:148` `path: '/:pathMatch(.*)*'` → `:150` `component: () => import('@/views/NotFoundView.vue')`. `frontend/src/main-served.ts` has no `pathMatch` entry outside the `/corpora/:pathMatch(.*)*` sub-route at `:94` — re-verified in this pass. Neither entry installs a `beforeEach`/`afterEach`. `shell/Shell.vue:243-247` is a bare `<router-view v-slot="{ Component }">`, so an unmatched hash renders nothing. `NotFoundView.vue` is absent from the served bundle entirely — `main.ts:150` is its only reference.

**Severity: P3 standalone.** Nothing in the app can currently generate an unmatched served-mode URL, so the only trigger is a hand-typed or bookmarked hash. It becomes P1-shaped the moment A4 is wired, because `LeftRail.vue:995,1001` then render in served mode and click into the void.

**Cheapest honest fix.** Add the `/:pathMatch(.*)*` → `NotFoundView` catch-all to `main-served.ts` — unconditionally correct under either answer to the sites/marketplace question in §4. Then an owner call on `/sites` + `/marketplace`: route them, or gate the two rail entries on served mode using the seam already imported at `shell/LeftRail.vue:26,68` (`isServedMode`). A CI tripwire asserting the two route tables agree except for a named allowlist would be cheap; no gate sees this drift.

---

#### B5 — `CedarPolicy_RecentDecisions` is a fully-wired read path with zero frontend readers, and three Go comments assert a consumer that does not exist

**User sentence.** The app records every policy allow/deny decision and will
hand them to the UI on request, but no screen asks — so a blocked action still
shows a bare error with no reason.

**Evidence.** `harnessClient.ts:2689` declares `recentDecisions(limit)`, adapted at `:3880`, stubbed to `[]` in the served/fake client at `:5440`. No `.vue` hit anywhere. `frontend/src/views/policy/` contains only `PolicyView.vue` and `CedarEditor.vue`; between them they call `listPolicies`, `getPolicy`, `validatePolicy`, `savePolicy`, `deletePolicy`, `reloadPolicies` — never `recentDecisions`. The backend is not a stub: `core/rpc/bindings.go:745-747` → `core/rpc/views/cedarpolicy/impl.go:123-130` → `core/policy/cedar/engine.go:381-384`. The ring is fed on **every** evaluation: `engine.go:425-428` `e.decisions.Append(out)`. Constructed with a real engine at `core/rpc/api.go:1922`.

**Three stale comments** naming consumers that do not exist:
`core/rpc/api.go:5721-5722` and `:5729` (both name "the frontend's run-trace replay"), and `core/policy/cedar/engine.go:434-435` ("The frontend pattern-matches on this type so it can render a structured denial notice") — that notice was `DenialNotice.vue`, which the 2026-08-14 sweep deleted.

**Disposition.** **Escalate + amend the ledger.** Do not delete `recentDecisions`: it is the cheap half of a trust-relevant surface. Correct the three comments as a drive-by.

**Ledger amendment owed.** `docs/unwired-ledger.md:600-625` ("The denial UX gap") states "the harness has no denial-aware UI at all" and prescribes wiring `policyAPI` (the `&stubPolicy{}` at `api.go:1094`) and defining a `policy:event` contract **first**. That is not wrong about `policyAPI`, but it omits that a second, separately-wired, non-stub, **pull-based** decision feed already reaches the client boundary. Add a line so the mission that closes the gap scopes a pull-based panel against the existing ring rather than assuming a push topic must be built first.

---

#### B6 — Three `PermissionsClient` shortcut methods are a dead second door, and their Go handlers document an audit event with zero emitters

**User sentence.** There are two ways to save a customised keyboard shortcut and
only one is used; and although the code says every shortcut change is written to
the audit log, no shortcut change is ever recorded.

**Evidence.** `harnessClient.ts:2147,2152,2157` (`getShortcuts` / `setShortcut` / `setShortcuts`), their adapters at `:3706-3708`, their fakes at `:4965-4967`, and three mock lines in `components/permissions/__tests__/BasePermissionModal.spec.ts:36-38` — that is the complete reference set. Zero production callers.

**They back the same field**, so this is unambiguously rival infrastructure: `core/rpc/bindings.go:1210-1230`, `:1235-1262`, `:1267-1285` each resolve `store := b.storeFn()`, prefer `LoadShortcuts`/`SaveShortcuts`, and fall back to `store.LoadAll()` → `s.KeyboardShortcuts` → `store.SaveAll(s)`. `core/rpc/views/settings/impl.go:561-570` `SaveShortcuts` does `got.KeyboardShortcuts = m; return s.saveLocked(got)` — same field, same file. The live path is a full-settings round-trip: `components/settings/KeyboardShortcuts.vue:91-92`, `:202-207`.

**The audit lie.** `bindings.go:1234` says "Emits KindShortcutOverridden audit event on success"; `:1266` says "Emits one … per changed entry". Neither function body (`:1235-1262`, `:1267-1285`) emits anything. `core/event/kind/registry.go:63-64` documents the kind as "emitted on every successful keyboard shortcut binding write" with a specified payload shape; `settings.shortcut.overridden` appears on exactly one non-test line tree-wide — the constant's own declaration at `registry.go:68`. It is in the `builtIn` validation slice at `:133`, so it validates fine and is simply never produced. The live save path emits nothing either.

**Disposition.** **Delete** — class *rival infrastructure*, now resolvable without further research. Delete the three client methods, adapters, fakes, the three spec mock lines, and the three bindings. **Handle the audit lie in the same change** — either implement the emit or delete `KindShortcutOverridden` and correct the three docstrings. A documented-but-never-emitted audit kind is exactly the failure mode the sweep exists to end.

**One check before deleting the bindings:** `core/serve/` has no "Shortcut" hits, but nobody read its dispatch mechanism to confirm it is an explicit method table rather than something reflective over the Bindings surface. The client-side methods are safe to delete regardless.

---

#### B7 — `useChatStream` is a permanent stub whose comment cites "downstream consumers" that never existed

**Evidence.** `lib/useHarnessAPI.ts:304-321` returns `ref([])` for `events`, `ref(false)` for `paused`, and a `stop` that resolves undefined; the parameter is already dead-named `_sessionId`. The comment at `:305-307` reads "The full streaming implementation arrives with WP12's useStream composable + WP11's streamBroker. This stub keeps the shape stable for downstream consumers." `useChatStream` appears on exactly two lines tree-wide, both inside the definition (`:294` docstring, `:304` declaration). The docstring at `:294` is also mis-attached — it sits above the `UseStreamResult` interface at `:296-302`, not the function it names.

This is CLAUDE.md blind spot #1 exactly: the file is live (`useSessions`/`useProjects`/`useArtifacts`/`useShellStatus`/`useToolsRecipes` from the same module are imported by `shell/LeftRail.vue:24`, `views/sessions/SessionsView.vue:48`, `views/tools/KenazToolsPanel.vue:18`), so no orphan-file scan can see it.

**Disposition.** **Delete** — *rival infrastructure*. The live substitutes shipped as `lib/useSession.ts` + `lib/useEventStream.ts`, and the same module's `useEventLogStream` (`:447`) is the real implementation of the same shape. Delete `:304-321` and the mis-attached docstring `:293-295`; **keep** `UseStreamResult` (`:296-302`), which `useEventLogStream` returns. No tests to delete alongside.

---

#### B8 — `useMemory` is a fully-implemented 70-line composable with zero consumers; `MemoryView.vue` re-implements a strict superset

**Evidence.** `lib/useHarnessAPI.ts:436-506` builds refs plus `refresh`/`setFilter`/`remember`/`promoteScope`/`forget` against `client.memory.*`. `useMemory`, `MemoryFilterPill` and `UseMemoryResult` appear on nine lines tree-wide, all inside `useHarnessAPI.ts:407-461`. The live substitute is strictly richer: `views/memory/MemoryView.vue` calls eight distinct `client.memory.*` methods — `listChunks` (:119), `forget` (:138), `pin` (:149), `prunePreview` (:171), `runPruneNow` (:185), `promoteScope` (:281), `narrativeFailedCount` (:315), `narrativeFailedList` (:317) — versus `useMemory`'s five, with no pin, prune or narrative-jobs surface. `MemoryView.vue:13,18` documents calling the client directly as intent, not drift.

**Disposition.** **Delete** — *a live substitute does the same job*, and it is a superset. Delete `useMemory` (`:436-506`), `UseMemoryResult` (`:416-433`), `MemoryFilterPill` (`:414`). No tests read them.

---

#### B9 — `eventLog`'s error ring is write-only while its comments call it "canonical" and name a consumer; two chat toasts declare emits no parent binds

**Evidence.** `lib/eventLog.ts` (79 lines, read whole): `const buffer: ReportedError[] = []` at `:19` is written only by `reportError` (`:54`, capped at `:56`). `recentErrors()` (`:73-75`) and `clearErrors()` (`:77-79`) are its only readers and have **zero callers, including tests**. Meanwhile `:3-4` says "downstream mission `event-log-01KQ1A3M` consumes via Reader" and `:44-45` calls the buffer "the canonical artifact that persists across the session". The durable trail is the other path: `backendLog` (`:29-40`) → `Diag_LogClientEvent`, called at `:57` and exported as `logEvent` (`:65-71`), which is live.

Separately: `components/chat/AuthFailureToast.vue:36-39` declares `rotated` / `dismissed` emits, fired at `:120`, `:129`, `:153`; its sole mount is `views/sessions/SessionsView.vue:2047` `<AuthFailureToast :auto-resume-enabled="autoResumeOnKeyRotation" />` — no listeners. `components/chat/FallbackActivePill.vue:24` declares a `dismissed` leg fired at `:43`; mounted bare at `SessionsView.vue:2049`.

**Honest severity.** The unbound emits are **not** a lie — Vue treats an unlistened emit as a no-op, and offering an event a mount site declines to use is ordinary component API. The genuine lie is the `eventLog` docstring pair, and it is dev-facing only.

**Disposition.** Two classes. For `eventLog`: **delete** — *live substitute* — remove `recentErrors` and `clearErrors` and correct `:3-4` and `:44-45`. For the emits: **no producer/consumer pair** — either delete the `defineEmits` legs and the `emit(...)` calls, or bind them at `SessionsView.vue:2047`/`:2049` if the parent should refresh on key rotation. That second option is a product call; do not resolve it by deleting if anyone wants post-rotation refresh.

---

#### B10 — The harness-self MCP server is constructed, logged, and attached to nothing

**Evidence, re-verified in this pass.** `core/rpc/api.go:2584-2588` constructs `a.harnessServer` and logs its tool count; those are the only uses of the field declared at `:551-555`. `harnessServer.Server()` (`core/rpc/harness_wiring.go:290`) has zero callers. `harness.NewTransport` (`core/mcp/builtin/harness/transport.go:34`) has zero non-test callers. `api.go:2570` concedes "the in-process transport wiring (WP09) will attach it to the session pool". Thirteen tools are registered (`core/mcp/builtin/harness/register.go`, 13 `Name:` entries).

**Largely a re-report.** `docs/unwired-ledger.md:865-892` already records this unattachment ("CORRECTION 2026-08-14"), names `harness_wiring.go:290`, and classes it *dead code, not a live lie*. Three things are genuinely new:
1. **A dangling tool name.** `core/mcp/builtin/harness/onboarding/code.md:23` still instructs the model to use `harness_write_propose_cedar_policy`, which the same sweep deleted — confirmed: it appears in `code.md` and in a comment at `core/event/kind/registry.go:78`, and nowhere else in `core/`.
2. **Three never-emitted event kinds.** Four kinds are declared at `core/event/kind/registry.go:84-87`; the single emit site is `core/mcp/builtin/harness/audit.go:57`, inside the unattached server, and it covers only `KindHarnessSelfToolCalled`.
3. **An I11 scope gap.** `scripts/ci/check-builtin-tool-registration.sh:61` sets `TOOLS_ROOT="core/tools"`, so `core/mcp/builtin/` is outside it.

**Refuting one framing.** The claim that "the onboarding prompts instruct the model to call these tools" is **not a live lie**: `code.md` is reachable only through `starter.SystemPrompt`, which A9 proves is never delivered. Both halves are inert, so nothing is model-visible today.

**Disposition.** **Escalate** — product intent unresolved; the ledger already parks it. Either attach `harnessServer.Server()` via `harness.NewTransport` for `kind=onboarding` sessions and fix `code.md:23` in the same commit, or retire the server, transport, three unemitted kinds and tool-naming starter prompts together. **The one thing that must not happen is fixing A9's prompt delivery alone.** Also extend I11 to `core/mcp/builtin/`.

---

#### B11 — Fifteen Wails bindings with zero consumers in either mode, three carrying docstrings that name a consumer

**Evidence.** Extracting every `func (b *Bindings) NAME` from `core/rpc/bindings.go` and grepping `frontend/src` for each yields: `CedarPolicy_ListPlanModeActions`, `Contexts_ContextExport`, `Contexts_ContextSearch`, `Diag_LogPath`, `LLM_UpdateProviderCredential`, `MCP_HealthSnapshot`, `MCP_SubscribeHealthChanges`, `Permissions_ListPending` (= A11), `Sessions_DeleteWithOptions`, `Sessions_StartCapture`, `Sessions_StopCapture`, `SetContext`, `SetSettingsStore`, `Unit_PromoteAsMergeRequest`, `Unit_ResolveLoadable`. The last two plus `SetContext`/`SetSettingsStore` are Wails lifecycle hooks and expected. Each of the twelve substantive names also has zero occurrences in `core/serve/`.

Docstring lies: `bindings.go:2662-2664` ("Used by the Cedar editor UI's plan-mode reference panel"), `:351-352` ("so the settings UI can surface 'logging to ~/.kenaz/harness.log'"), `:375-379` ("The frontend ONLY calls this when the user has typed a new key value").

**The delete candidate has a live substitute, and it is not the one previously named.** `LLM_UpdateProviderCredential`'s substitute is not `TestAndRotateKey` (that is the auth-failure recovery path, called only from `components/chat/AuthFailureToast.vue:104`) — it is `LLM_UpdateProvider`, whose payload carries `PlaintextAPIKey string` (`core/rpc/views/llm/api.go:88-91`) and whose contract at `:197-201` states "PlaintextAPIKey is OPTIONAL — when empty, the keychain entry is left untouched", i.e. it already implements the exact leave-blank-to-keep flow the orphan claims. That same field falsifies `core/rpc/views/llm/impl.go:1228-1230` ("This is the ONLY RPC that accepts plaintext") on its face.

MCP health is half-landed: `views/settings/MCPHealthSettingsPanel.vue` touches only `getMCPAutoRestart`/`setMCPAutoRestart` (`:22`, `:37`), and nothing in `frontend/src` references `mcp:health-changed`.

**Disposition, mixed per item.**
- **Delete** `LLM_UpdateProviderCredential` + its view method (*live substitute*), and correct the false ONLY-RPC comment at `impl.go:1229` in the same commit.
- **Correct in place** the docstrings at `bindings.go:2662-2664` and `:351-352` — the comment lies, the binding is fine.
- **Escalate** the rest (`Sessions_Start/StopCapture`, `Contexts_Context{Export,Search}`, `MCP_HealthSnapshot`/`SubscribeHealthChanges`): half-landed features whose consumer half is a product call. Deleting the reachable half of a wanted feature destroys tested work.

**Gate note.** No gate walks binding → frontend consumer; the existing tripwires walk registered-tool → predicate only.

---

#### B12 — Four `HarnessAPI` surfaces are permanently `&stub{}` behind 13 bindings, and one collides by name with the live workflows feature

**Evidence.** `core/rpc/api.go:1087-1094` — one constructor literal: `a2aAPI: &stubA2A{}`, `workflowAPI: &stubWorkflow{}`, `trustAPI: &stubTrust{}`, `contextAPI: &stubContext{}`, `policyAPI: &stubPolicy{}`. Per-field greps over non-test Go return exactly three lines each — declaration, this assignment, getter: `a2aAPI` (`:415`, `:1089`, `:6526`), `workflowAPI` (`:416`, `:1090`, `:6527`), `trustAPI` (`:419`, `:1092`, `:6540`), `contextAPI` (`:420`, `:1093`, `:6541`), `policyAPI` (`:423`, `:1094`, `:6549`). No reassignment path.

Thirteen bindings sit on top: `A2A_ListCards` `:538`, `A2A_StartStream` `:542`, `A2A_StopStream` `:546`, `Workflow_ListJobs` `:553`, `Workflow_StartStream` `:557`, `Workflow_StopStream` `:561`, `Trust_ListSecretReferences` `:568`, `Trust_GetSecretReference` `:572`, `Context_List` `:579`, `Context_StartStream` `:583`, `Context_StopStream` `:587`, plus `Policy_Explain` `:712` / `Policy_StartStream` `:716` / `Policy_StopStream` `:720` (already ledgered). `harnessClient.ts:3516-3534` wires the runtime surfaces `a2a`, `workflow`, `trust`, `context` (and `policy` at `:3575`); zero production callers, zero routes in `core/serve`.

**The naming trap is real.** The live workflows feature is the separate `Workflows_*` family, bound against `a.workflowsAPI` (`api.go:1799`) — a completely different field from the stubbed singular `workflowAPI` (`api.go:416`).

**Disposition.** **Escalate, then delete.** Class *the whole subsystem has no producer and no product intent*, but the rubric's caveat applies: a2a (agent cards) and trust (secret references) are plausibly wanted, and deleting the consumer half of a wanted feature is the wrong call. Record the question rather than resolve it by deleting. **One exception needs no escalation:** the singular `Workflow_*` trio (`bindings.go:553-563` + the `workflow` surface at `harnessClient.ts:3521-3525` + `stubWorkflow`) should go regardless — `Workflows_*` is the live substitute, a clean delete class.

---

#### B13 — `/search` is a dead route whose comment cites a route guard that does not exist, and `/hooks` has no in-app entry point

**Evidence.** `frontend/src/main.ts:103-111` registers `/search` → `SessionsView.vue` with the comment "The modal is rendered by Shell.vue and triggered by the route-change guard below." There is no such guard: filtering tests, `beforeEach` appears once in `frontend/src`, as a comment at `lib/workflowRunsStore.ts:266`. No `router.beforeEach` exists anywhere. Nothing watches the route; `Shell.vue`'s `searchOpen` ref is set only by the ⌘F branch (`:141-145`). Reachability is narrower than first claimed: `/search` appears only in the two route registrations (`main.ts:108`, `main-served.ts:113`) — no nav link, no palette action — so `restoreLastRoute` (`lib/routing.ts:19-46`) can only replay it if the user hand-typed the hash.

`/hooks`: `main.ts:59-61` → `views/hooks/HooksView.vue` (15.1K) with zero in-app links. The Hooks nav entry at `SettingsTabs.vue:93` points at `/settings?tab=hooks`, which `SettingsView.vue:25,1091` mounts as the **different** component `HooksSettingsView.vue` (6.6K); the palette action `settings.hooks` (`useCommandPalette.ts:45`) goes to the same tab. `SettingsTabs.vue:5-7` still documents itself as rendered on "HooksView".

**Disposition.** **Delete-or-link.** `/search`: drop the route and the comment, or write the guard it promises. `/hooks`: a rival-infrastructure call — pick one of the two implementations, delete the other, correct `SettingsTabs.vue:5-7`. Note the reachable one is the smaller.

---

#### B14 — `MessageList` never passes `streaming-failure-kind`, and `MessageBubble`'s docstring describes copy that does not exist

**Evidence.** `components/chat/MessageBubble.vue:117` declares `streamingFailureKind?: string`, branched at `:415` and rendered at `:689`. Its only mount site is `components/chat/MessageList.vue:341`, whose 21 props and 6 handlers (`:341-370`) include the two siblings `:streaming-failed-at` (`:362`) and `:streaming-recoverable` (`:363`) but not this one, and there is no `v-bind` spread. The field is already on the object the template holds: `MessageList.vue:39` types `messages: ReadonlyArray<Message>` and `lib/types.ts:1263` declares `streamingFailureKind` on `Message`. Backend live: `core/rpc/views/agentgraph/chat/chat_runner.go:1491-1505` `classifyPartialFailureKind`, called at `:1406`, persisted at `:1412-1413`, mirrored at `core/rpc/views/sessions/impl.go:579`.

**Severity downgraded to P3, honestly.** A live substitute already carries the diagnosis: `components/chat/AuthFailureToast.vue:58` subscribes to `provider:auth-failed`, whose producer is real (`chat_runner.go:1248`), it is mounted at `SessionsView.vue:2047`, and per its header at `:6-11` it lets the user paste a replacement key inline. The missing binding costs a 12-character parenthetical, not the diagnosis.

**The actual lie is dev-facing.** `MessageBubble.vue:112-114` documents the prop as tailoring copy `"transient" → "Network blip"; "auth" → "Re-authenticate"`. Neither string exists at `:411-418`, where `case 'transient'` and `default` are byte-identical.

**Cheapest honest fix.** Add `:streaming-failure-kind="item.message.streamingFailureKind"` at `MessageList.vue:341-363`. In the same edit reconcile the docstring with the real strings, and either give `'transient'` distinct copy or collapse the arm.

---

#### B15 — The memory hook journal has no read path, and the interface docstring names an implementer that does not exist

**Evidence.** `HookJournalView` is not mounted — its only references are the component, its test, and a doc mention at `lib/types.ts:1540`; `MemoryView.vue` never imports it. The reader is nil: `core/rpc/views/memory/impl.go:341-344` short-circuits on `a.journal == nil`, and the sole production construction site `core/rpc/api.go:1560-1565` omits `Journal:` — `Journal:` has no non-test hit in `core/`.

**New:** `JournalSnapshot` appears only as the interface declaration (`impl.go:60`) and its call site (`:348`). **Nothing in the tree implements it**, so the docstring at `impl.go:56-57` — "The kernel's HookManager satisfies this" — is false: `core/agentgraph`'s HookManager holds `journal []HookRecord` (`hooks.go:141`), has no such method, and the types differ anyway.

The write half genuinely works: `core/rpc/api.go:6516-6533` `buildJournalWriter` returns `coreag.NewSQLJournalWriter(rawDB)`, wired at `:5125-5126` into `deps.JournalWriter` and applied at `:4103-4104`.

**Already ledger-tracked** as backlog task #27 ("memory journal") and at `docs/unwired-ledger.md:849-851`. Only the false `JournalSource` docstring is new.

**Disposition.** **Finish or delete — both halves in one change.** A SQL-backed reader satisfying `JournalSource` passed as `Journal:` at `api.go:1560`, **and** a mount for `HookJournalView`. Mounting alone would move the lie into the UI (it renders an empty state telling the user to "try sending a message in the chat surface"). At minimum, correct `impl.go:56-57` now.

---

#### B16 — The branch merge-suggestion toast subscribes to a topic nothing emits, and the kernel `Env` field documenting it is never assigned

**Evidence.** `composables/useEventToasts.ts:124` subscribes to `branches:merge-suggested`, live via `SessionsView.vue:80`. `merge-suggested` has **zero** matches in `core/`. `MergeSuggester` matches, besides the suggester's own file, exactly two lines: `core/agentgraph/executor.go:297-299`, a kernel Env/Config field whose docstring reads "MergeSuggester powers the kernel's 'merge?' toast. nil disables the suggestion stream". The field has no assignment and no read anywhere in `core/`, so it is nil on every run. `NewMergeSuggester()` (`branch_merge_suggester.go:45`) and `Inspect()` (`:97`) have no non-test caller.

**On main the subscriber outlived its component:** `MergeSuggestionToast.vue` was deleted by the 2026-08-14 orphan sweep while `useEventToasts.ts:123` still subscribes.

**Disposition.** **Finish or delete.** Finish = assign the suggester into the kernel Env and emit from the child-run completion path. Delete = remove `core/agentgraph/branch_merge_suggester.go`, the `executor.go:297-299` field, and `useEventToasts.ts:123-151`. Either way the `executor.go` docstring must not survive unchanged.

---

#### B17 — The post-key-rotation retry-failure toast subscribes to a topic no Go code emits

**Evidence.** `composables/useEventToasts.ts:86-88` subscribes to `provider:retry-after-rotation-failed`, live via `SessionsView.vue:80`. Enumerating every `"provider:` literal in `core/`, the complete emitted set is `provider:auth-resumed` (`chat_runner.go:804`), `provider:auth-failed` (`:892`), `provider:capability-missing` (`:921`). No failure topic. The resume path returns the error bare: `chat_runner.go:794-797` `newSubID, err = r.StartStream(...); if err != nil { return "", err }`, and the only Emit on that path (`:803-808`) is the success topic. The wire type asserts a producer it cannot back: `lib/types.ts:1398-1403` "payload of the `provider:retry-after-rotation-failed` broker event. Emitted when …". `RetryAfterRotateToast.vue` was deleted by the 2026-08-14 sweep, so on main the subscriber and the type survive with no component, and `chat_runner.go:392-394` and `:799-801` now name a component that no longer exists.

**Disposition.** **Finish or delete.** Finish = emit from the `StartStream` error branch at `chat_runner.go:795-797`. Delete = drop `useEventToasts.ts:84-121` and the payload type, and fix the two `chat_runner.go` comments.

---

#### B18 — The slash-command "coming soon" chain has no producer

**Evidence.** `core/slashcmd/cmd_stubs.go:17` `func (s stubCommand) ComingSoon() bool { return true }` is the sole true-returning implementation, and `stubCommand` appears only in `cmd_stubs.go`, `cmd_stubs_test.go` and `registry_test.go:107`. Every registered command returns false (`cmd_model.go:18`, `cmd_effort.go:37`, `cmd_secret.go:35`, `cmd_skill.go:25`, `cmd_help.go:17`, `cmd_clear.go:22`, `cmd_memory.go:26,63,127`, `cmd_branch.go:20`, `cmd_wf.go:29`), matching the registration set at `registry.go:36-48`. Unreachable consequences: `command.go:63` `comingSoonTemplate`, `:378` `comingSoonResult`, the `/help` tag branch at `cmd_help.go:35`, the wire field at `core/rpc/views/slashcmd/api.go:24` populated at `impl.go:109`, and the badge at `components/chat/SlashAutocomplete.vue:82`.

**Disposition corrected from "delete" to "justify".** A keep-decision already exists in-tree at `core/slashcmd/cmd_stubs_test.go:9-15`: *"The stubCommand type itself is kept as a building block for future 'registered but not yet wired' commands."* Blanket deletion would override a documented, tested intent for zero user benefit. **Stamp that decision with a date and owner** — it currently has neither — and fix the stale template text at `command.go:63` while you are there (it still names the shipped agent-kernel-graph memory pipeline).

---

### C. Not a defect — recorded so the next sweep skips this pass

#### C1 — The frontend file-level reachability baseline is clean

Verified in this pass: `import.meta.glob`, `require.context`, `defineAsyncComponent` and `app.component(` have **zero matches** across `frontend/src`, `frontend/index.html` and `frontend/served.html`. There is no dynamic-registration escape hatch, which is what makes static reachability analysis sound here. All nineteen named orphan components are present in `docs/unwired-ledger.md`. `lib/tasks.ts` has exactly five importers plus its own spec, all inside the already-parked background-task cluster.

**Not verified in this pass** (flagged honestly): the 498-file BFS itself, the 19-site `<component :is>` enumeration, and the imported-but-unrendered scan across 300+ `.vue` files. Those rest on the original computation.

**Action:** append a dated "file-level orphan question in `frontend/src` is DRAINED" note to the ledger with those three items flagged, so the next sweep spends its budget on dead symbols inside live files — which is where A4 came from — rather than a fourth orphan-file pass. The actionable spin-off belongs on existing backlog task #23: make the frontend orphan gate an **exported-symbol** scan, flagging any exported symbol in an entry-reachable module whose only importers are test files.

---

## 3. Partially implemented features — what the missing half is

Sized as **mount** (render an existing component), **one `if`** (a line or two of wiring at an existing seam), or **mission** (real design + build).

| Feature | Missing half | Size |
|---|---|---|
| Fleet capability gating (A4) | Call `initFeatureFlags` at both entry points **plus** re-init on the sign-in path **plus** an entry-point regression test | **one `if` × 3** — the third part is what makes it not a one-liner |
| Sites / Marketplace views | Served routes + a nav or palette entry (or a written decision that they are desktop-only) | **mount**, blocked on an owner call (§4) |
| Cedar memory/model/websearch gates (A1) | Pass `buildCedarGate(...)` at three sites | **one `if` × 3**, plus a **mission** for the two search backends that bypass `CheckNetwork` entirely |
| Cedar workflow/scheduled-chat gates (A2) | `Cedar:` in two Config literals | **one `if` × 2**; the strict-mode dial behind it is a **mission** (or delete the strict arm) |
| Search privacy toggle + audit (A3) | Assign `cfg.Enabled` and `cfg.Audit` | **one `if` × 2** |
| Update install + progress (A7/A8) | One subscriber that waits for `staged`, serving both the await and the progress UI | **mission (small)** — the progress surface does not exist at all |
| Guided onboarding (A9) | `SetSystemPrompt` at `onboarding_wiring.go:134` — **sequenced behind** the harness-self decision | **one `if`**, gated on a **mission** decision (B10) |
| MCP custom-recipe create/edit (A5) | `saveCustomRecipe` backend (WP10); interim, hide the Custom tab **and** the per-row Edit button | **mission**; interim fix is **one `if`** |
| MCP paste-config import (A6) | UserStore-backed source at `api.go:1159` and `:3270`; `StartWatch` has no caller anywhere | **one `if` × 2** + a small **mission** for live pickup |
| Permission prompt rehydration (A11) | Call `ListPending` on boot/reconnect, copying `ConfirmToolModal.vue:246` | **one `if`** |
| Telemetry tier gate (A10) | Pass the resolved tier from `App.vue`, or delete the gate | **one `if`** |
| Branch advisor (B2) | Read `branchAdvisorEnabled` in `ChatInput.vue:196`; use the effective-token accessor at `branches/impl.go:475`; **then** mount `BranchAdvisorSettings` | **one `if` × 2**, then **mount** |
| Memory hook journal (B15) | A SQL-backed `JournalSource` + a mount for `HookJournalView` — both, or neither | **mission (small)** |
| Local telemetry (B3) | Retention sweep (`instance.go:9`); a reader is a product question | **mission** |
| Denial UX (B5) | A pull-based panel over the existing `RecentDecisions` ring | **mission**, but cheaper than the ledger currently scopes it |
| MCP health (B11) | Consumer for `HealthSnapshot` / `SubscribeHealthChanges` | **mission**, product call |
| Session capture, context export/search (B11) | Consumer half | **mission**, product call |
| Harness-self MCP (B10) | Attach via `harness.NewTransport`, or retire the stack | **mission**, product call |
| a2a / trust / context stubs (B12) | Entire producer half | **mission**, product call |
| Branch merge suggestion (B16) | Assign `MergeSuggester` into kernel Env + emit | **mission (small)** or delete |
| Retry-after-rotation toast (B17) | Emit from `chat_runner.go:795-797` | **one `if`** or delete |

---

## 4. Open questions nobody could resolve

Verbatim, as recorded by the verifying passes.

> Whether the absence of `/sites` and `/marketplace` from `frontend/src/main-served.ts` is intentional or drift. I confirmed the routes are absent (route table ends at `main-served.ts:130-131` `/policy`) and that neither view uses the served-mode boundary component. But `docs/served-mode-boundary.md` already names SettingsView and WorkflowsView — both fleet-publish surfaces — as deliberately served-unavailable, so "fleet surfaces are desktop-only" is a plausible unwritten boundary rather than a bug. Code cannot settle it; it needs an owner call.

> Whether the served-mode `AppInfo` response actually carries a populated `capabilities` map at runtime. I traced the dispatch (`core/serve/server.go:541-542` calls the same `s.api.AppInfo(ctx)`), so the field is structurally present, but I did not verify that a served-mode deployment has a fleet session whose `settingsAPI.FleetCapabilities` returns a non-`default-deny` view. If served mode always resolves default-deny, the served half of the fix is a no-op and only the desktop entry needs wiring.

> Whether `initFeatureFlags` being called once at boot is sufficient, or whether a signed-in user's capabilities change often enough mid-session to require polling. I established that a boot-only call is definitely insufficient for the sign-in transition (`AccountPanel.vue:97` mutates fleet state after boot), but I did not determine whether tier upgrades or admin-side capability revocations also need to propagate live. Note there is an in-flight worktree named `fix/capability-poller-refresh`, which suggests someone is already working this seam; I did not enter that worktree per the read-only constraint. Worth checking before dispatching work.

> Whether any of these findings are already fixed on a branch other than main. `git worktree list` shows 70+ live worktrees, and at least two have names that bear directly on the capability finding — `fix/capability-poller-refresh` and `feat/harness-fleet-sync`. Others named `fix/nav-settings-ia-cleanup` and `fix/harness-ui-polish-cluster` may be touching exactly these files.

> Whether P0 vs P1 is the right call on the capability finding. I downgraded it because the failure is fail-CLOSED — it hides capability rather than falsely granting it. Against that: fleet is on by default, so every signed-in user on a default build hits it, and the copy tells a signed-in user to sign in. Someone with product context on how much of the paid tier routes through these seven surfaces may reasonably put it back at P0; I did not have that context.

> Whether deleting the three `Settings_*Shortcut*` bindings is safe for served mode. `grep -rn "Shortcut" core/serve/` returned nothing, but I did not read core/serve's dispatch mechanism to confirm it is an explicit method table rather than something reflective over the Bindings surface. Confirm before deleting bindings (the client-side methods are safe to delete regardless).

> Whether served mode reaches the search path by some route other than a named handler. I confirmed `core/serve` contains no reference to `searchview` or `Search_*`, and its dispatch lives in `methods.go`, but I did not read `methods.go` end-to-end to rule out a generic reflective dispatch.

> Whether any consumer outside this repo calls the twelve orphan bindings. I proved zero references in `frontend/src` and zero in `core/serve`, but I did not audit the fleet control-plane, Remote Control, or any external WS client that might dispatch method names by string.

> Whether `openMemoryStore`'s production return value actually satisfies `corememory.GateSetter`. I read the `SetGate` call and its type assertion but did not trace `openMemoryStore` to confirm the concrete type under a real `HARNESS_MEMORY`-on configuration. If the assertion fails at runtime the gate is nil rather than AllowAll — the same permit-everything outcome, so the finding stands either way, but the mechanism differs.

> Whether anyone has ever installed `default_workflows_policy.cedar` in practice. I confirmed the mechanism exists but there is no telemetry or default that would tell me whether the template is surfaced prominently enough in PolicyView for a user to reach it. That affects how many users the workflow-gate finding actually touches.

> Whether the search-backend gate bypass in `core/tools/websearch/{duckduckgo,wikipedia}.go` is intentional (search-engine queries deliberately exempt from network policy) or an oversight. No comment in either file addresses it.

> Whether `kenaz__websearch`'s default-on/off state changes the blast radius of the AllowAll gate. I confirmed the enable predicate reads `store.LoadWebSearch()` (`core/rpc/builtins_wiring.go:601-606`) but did not trace the persisted default value.

> Whether `Update_StartDownload` could ever complete before the following `Update_Apply` in a real run (tiny asset, warm local mirror). My conclusion is static: StartDownload sets `hasStaged=false` and returns after spawning the pump, so the race is lost by construction, but I did not execute the app to observe a real failure.

> Whether the four never-emitted harness-self event kinds have consumers waiting on them (an audit view filter, a fleet exporter). I verified the emit side only.

> Whether the harness-self MCP server is deliberately parked by an owner decision rather than merely unfinished. `docs/unwired-ledger.md:865-892` records the unattachment as known and "housekeeping", but names no owner and no mission. This is a product call, not a code question.

> Product intent for the a2a / trust / context stub subsystems. The rubric requires an owner decision here and I have no basis to guess one — no kitty-specs mission referencing them was searched for.

> Whether the branch-advisor work has an open mission that already owns the `BranchAdvisorEnabled` gap. I checked `docs/unwired-ledger.md` (which does not record it) but did not search `kitty-specs/` for the mission's current state.

> Whether local telemetry persistence is intended product surface (a planned diagnostics/trace viewer) or an artefact of the half-landed WP03 — a product call I cannot make from the code.

> Whether the entitlement gate in `TelemetryOnboardingModal` is the actual product intent at all. `FleetTelemetryPanel` has no gate, so the two surfaces cannot both be right, but nothing in-tree states which one is.

> Whether a freshly imported MCP recipe actually spawns end-to-end in served mode: the code path exists (`main.go:376` / `cmd/harness-served/main.go:123` → `CatalogWithUserRecipes` → `UserStore.Load`), but it also requires the imported id to be present in `KENAZ_MCP_ALLOWLIST` and requires a restart (`Load` runs at the supervisor's boot snapshot; `StartWatch` is never called). I read the paths but ran nothing.

> Whether `parseAttachmentError`'s prefix branches ever actually fire at runtime. I established it is reachable but not that it fires: the producer is `gate.CheckAttachments` (`core/llm/capabilities/gate.go:57`), whose sole caller is `core/llm/registry/registry.go:394`. I did not trace whether that runs synchronously inside `StartLLMStream` — in which case `friendly()` sees it — or asynchronously on the stream, in which case it takes a different route.

> I could not verify whether the fleet server ever emits a `settings_sync` capability key. `core/fleet/capability_poller.go:316-318` copies the server's map through unvalidated, so an out-of-tree key could in principle arrive. Moot for the verdict — main already uses `context_sync` — but it means the harness has no client-side validation that a received capability key is one it knows.

---

## 5. Cross-reference with `docs/unwired-ledger.md`

### New — not recorded anywhere in the ledger

Grepping the ledger for each finding's identifying token returns **zero** for:
`featureFlags`, `signedIn`, `SearchDisabled`, `searchview`, `AllowAll`,
`installLatest`, `Update_Apply`, `onboarding_wiring`, `starter`,
`SystemPrompt`, `PasteConfig`, `importClaudeDesktopConfig`,
`TelemetryOnboarding`, `telemetry_logs`, `CommandPalette`,
`AddMCPServerModal`, `CustomRecipeTab`, `Permissions_ListPending`,
`BranchAdvisorEnabled`, `A2A_`, `recentDecisions`, `useChatStream`,
`useMemory`, `eventLog`, `merge-suggested`, `JournalSource`, `NotFoundView`,
`comingSoon`, `shortcut`.

That is **A1–A11, B1–B4, B6–B9, B12–B14 and B16–B18** — new material.

### Already recorded

- **B10 (harness-self MCP)** — `docs/unwired-ledger.md:865-892` records the unattachment verbatim and classes it *dead code, not a live lie*. Only the dangling `code.md:23` tool name, the three unemitted event kinds, and the I11 scope gap are new.
- **B15 (memory hook journal)** — ledgered at `:849-851` and as backlog task #27. Only the false `JournalSource` docstring is new.
- **B12's `policyAPI` arm** — the `stubPolicy` assignment is ledgered at `:609` and `:958`.
- **B5's user-facing half** — "The denial UX gap", `:600-625`, already owns the no-denial-reason problem.
- **A3's gate gap** — "knob-coverage tracks one struct out of the several that need it", `:693-710`, already explains why `settings.Settings` is invisible to CI.

### Ledger entries these findings prove **false** or incomplete

1. **`:852-854` names the wrong blockers for `BranchAdvisorSettings`.** The ledger lists it under P1 "slated to be finished", "blocked on inert Go knobs (see 'Settings fields that are stored, bound, and inert' above)". That list (`:661-690`, read in full) names `BranchAdvisorUseLLM` and `BranchAutoMode` — filed under *"self-documented as reserved (fine)"*. It does **not** name `BranchAdvisorEnabled` or `BranchReintegrationMaxTokens`, which are the two knobs that actually block the mount. Anyone following the ledger would mount the panel and ship an inert toggle. **Amend `:852-854` to name the real blockers.**
2. **`:600-625` (denial UX) is incomplete in a way that inflates the mission.** It states "the harness has no denial-aware UI at all" and prescribes wiring `policyAPI` and defining a `policy:event` contract first. It omits that `CedarPolicy_RecentDecisions` is a separately-wired, non-stub, pull-based decision feed that already reaches the client boundary (`core/rpc/views/cedarpolicy/impl.go:123-130` → `core/policy/cedar/engine.go:381-384`, ring fed at `engine.go:425-428`). The cheapest denial panel does not need a new push topic.
3. **`:958` cites a stale line.** The `stubPolicy` assignment is at `core/rpc/api.go:1094`, not `:1109` — and the same file already says `:1094` at `:609`. Trivial, but this is exactly the kind of stale citation that gets a finding retracted.
4. **Two candidate findings died as already-fixed**, which is worth recording so they are not re-raised:
   - *"SyncPanel gated on nonexistent capability `settings_sync`"* — fixed on main. `SyncPanel.vue:176` reads `capability('context_sync')`, and `:21-23` documents the change: "It was `settings_sync` until 2026-08-14 — a key that never existed on the wire."
   - *"Tasks panel permanently empty + bash advertises `run_in_background`"* — both halves fixed or parked by the 2026-08-14 sweep, and verified in code, not just in the ledger: `core/tools/bash/bash.go:263-267` only returns the background schema when `t.backgroundSpawn != nil`, so the model is offered the knob only when the seam is wired; `SettingsTabs.vue:98` carries the removal comment and offers no Tasks entry.

### Gates owed (append to the ledger's gate inventory)

| Class no gate can see | Where it bit us |
|---|---|
| Exported frontend symbol whose only importers are test files | A4 — the I10 shape exists but is Go-only |
| Module-level state whose only writer is a test | A4 |
| A `cedar.Gate`-typed Config field with no non-nil production assignment | A2 |
| An RPC's async contract vs. its caller's await sequence | A7 |
| Binding → frontend consumer reachability | B11 |
| `settings.Settings` knob coverage | A3, B2 — already ledgered at `:693-710`, still open |
| Builtin tool registration outside `core/tools` (`check-builtin-tool-registration.sh:61`) | B10 |
| Two route tables agreeing except by allowlist | B4 |

---

## 6. What this audit could not see

Name the limits so nobody over-reads the result.

- **Nothing was executed.** No build, no test run, no app launch. Every claim is static reading of the working tree. Notably unverified by observation: the ⌘K double-open (derived from two live listeners, equal `z-50`, no `<Teleport>`, and DOM order), the update-download race (lost by construction, but never observed), and whether the memory hook journal actually inserts rows in a real profile.
- **Runtime-only wiring is invisible.** A value assigned through reflection, a topic subscribed by string concatenation, or a handler registered from a config file would not appear in any grep here. The one place this was actively checked is `frontend/src`, where zero dynamic-registration escape hatches exist — that check does **not** extend to `core/serve`'s dispatch, which is why three findings carry an explicit "confirm `core/serve` is not reflective" caveat.
- **Served vs. desktop divergence was only partially modelled.** Both route tables were diffed and `core/serve` was grepped per finding, but no served-mode deployment was exercised. Whether served mode ever resolves a non-`default-deny` capability view is unresolved (§4) and materially changes half of A4's fix.
- **The import graph cannot model intent.** It can prove a symbol has no reader; it cannot distinguish "abandoned" from "deliberately parked". Every finding whose disposition is *escalate* is there because the graph ran out of things to say, not because the evidence was weak. B18 is the cautionary case: a keep-decision existed in a test file, and a graph-driven verdict would have deleted it.
- **Cross-repo and out-of-tree consumers were not audited.** The fleet control-plane, Remote Control, and any external WS client that dispatches method names by string could call the twelve "orphan" bindings. Zero references were proven within this repo only.
- **~70 in-flight worktrees were not inspected.** At least four have names suggesting they touch these exact files. Some of what is here may already be fixed on a branch. Check before dispatching.
- **Configuration-dependent paths were not enumerated.** Defaults were traced where they mattered (fleet on by default, policy editor on by default), but no matrix of env-var combinations was walked — `HARNESS_MEMORY`, `KENAZ_MCP_ALLOWLIST`, and `HARNESS_FLEET_DISABLED` each gate a finding's blast radius.
