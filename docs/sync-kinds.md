# Sync kinds — adding a new syncable kind (FR-009)

Mission: `fleet-generic-sync-framework-01NSYNC02`. Architecture background:
`kitty-specs/harness-fleet-sync-architecture/DESIGN.md` §4 (sync patterns),
§5.6 (settings sync). Registry design: the mission's `spec.md` §2.1.

This doc is the "adding a kind" how-to promised by FR-009. It lives here
(not in `DESIGN.md`) because `DESIGN.md` is gitignored in this checkout —
see the note at the top of the file if that changes; migrate this content
there if it does.

## What "generic sync" means today

The harness has one generic *user-scoped* transport:
`PUT/GET /api/v1/sync/{category}`, LWW (last-writer-wins) by timestamp,
already generic on the fleet server side — it does not enforce a
category allowlist. `core/fleet/sync.go`'s `Syncer` implements the
debounced push / LWW pull / backoff poll loop against that endpoint for
*any* category string once one is registered.

`core/fleet/synckind.go` adds the **registration surface** on top of that
transport: a `SyncKind` is a declarative description of one syncable thing
(ID, scopes, transport, collect/apply functions, secret + conflict
policy). A `KindRegistry` holds the set of registered kinds. Registering a
kind through the registry, instead of calling `Syncer.RegisterCategory`
directly with hand-rolled `CategoryConfig`, is what makes a new kind
declarative and auditable (`registry.Kinds()` / `registry.IDs()` enumerate
everything that syncs).

The five kinds shipped before this mission — `provider_profiles`,
`model_prefs`, `mcp_recipes`, `installed_mcp`, `ui_theme` — are registered
this way as of WP01 (`core/rpc/sync_categories.go`). `slash_commands`
(WP05, `core/rpc/sync_categories_slashcmd.go`) is the first kind added
*after* the registry existed, proving the "one `SyncKind` + one
collector/applier, zero endpoint work" claim.

## The `SyncKind` type

```go
type SyncKind struct {
    ID             string          // wire category / org_config key, e.g. "slash_commands"
    Scopes         []Scope         // which layers may carry this kind: user | team | org
    Transport      Transport       // lww_category | bundle_pushdown | catalog | unit
    Collect        KindCollector   // func(ctx) ([]byte, error) — serialize local user-scope state
    Apply          KindApplier     // func(ctx, scope, payload []byte) error — apply one layer
    SecretPolicy   SecretPolicy    // must_not_contain_secrets (only value in v1)
    ConflictPolicy ConflictPolicy  // lww | org_wins_readonly | merge
}
```

- `Scopes` / `Transport` / `SecretPolicy` / `ConflictPolicy` are required —
  `KindRegistry.Register` rejects a kind missing any of them.
- `Collect` may be nil for a kind the harness never originates locally
  (an org-only kind with no personal counterpart). `Apply` may be nil for a
  collect-only kind (none exist yet).
- For a **user-scoped, LWW-transport kind** (the only shape WP01/WP05
  exercise), `SyncKind.CategoryConfig()` adapts `Collect`/`Apply` into the
  `CategoryConfig` shape `Syncer.RegisterCategory` expects, and always
  invokes `Apply` with `ScopeUser` — the LWW transport is per-user by
  construction. Org/team dispatch (via the ConfigBundle `org_config`
  section) is a WP02+ concern with its own composite applier; it is not
  wired through this adapter.

## Recipe: adding a new user-scoped kind

This is the path `slash_commands` took; follow it for the next one
(hooks, keybindings, workflow templates, per spec §2.3's "kinds at
launch" table).

1. **Confirm it's genuinely user-scoped, local, and small.** The registry
   supports team/org scopes too, but a first cut should pick the smallest
   real local store — one that already has a clean read/write API and no
   credential bytes. (`slash_commands` chose `core/slashcmd.Store`'s
   *global*-scope commands; project-scoped commands were excluded because
   they're tied to a project checkout that may not exist on the receiving
   device.)

2. **Write the collector.** A `func(ctx) ([]byte, error)` that reads local
   state and returns a JSON payload. HARD RULE: never include credential
   bytes — if the local store can hold secrets, redact them the way
   `core/fleet/sync_mcp.go`'s `MCPSyncCategory.Collect` strips
   `EnvOverrides` values for keys in `SecretKeys`. Strip any field that's
   purely device-local (e.g. a filesystem path) rather than putting it on
   the wire — `slash_commands` clears `PayloadPath` before marshaling since
   the receiving device recomputes it from its own data directory.

3. **Write the applier.** A `func(ctx, scope, payload []byte) error` that
   unmarshals the payload and writes it to local state. Validate each
   incoming item with the store's own validator before persisting; skip
   (log + continue) rather than abort the whole batch on one bad item — this
   keeps one malformed synced entry from blocking every other entry in the
   same payload, mirroring the existing categories' best-effort posture.

4. **Register it.** Build the `SyncKind` (ID, `Scopes: []Scope{ScopeUser}`,
   `Transport: TransportLWWCategory`, `SecretPolicy:
   SecretPolicyMustNotContainSecrets`, `ConflictPolicy: ConflictPolicyLWW`
   for a v1 user-only kind), call `registry.Register(kind)`, then
   `syncer.RegisterCategory(SyncCategory(kind.ID), kind.CategoryConfig())`.
   Wire this from `core/rpc` (not `core/fleet`) if the collector/applier
   needs to reach a store `core/fleet` can't import — see the OSS-first
   boundary note at the top of `sync_categories.go`.

5. **One `core/fleet/sync.go` change: the Fleet-accepted list.**
   `RegisterCategory` lazily creates the category's runtime state and the
   background poll loop iterates whatever is currently registered (the
   WP01 genericity fix), but the Syncer only sends categories in
   `fleetAcceptedSyncCategories`. Anything else stays on the device:
   Push/Pull return `ErrSyncCategoryLocalOnly` and `Sync_Status` reports
   `local_only`. Add the new id there only after step 6 has shipped.

6. **A Fleet-server change is required first.** This file used to say
   none was needed. That was wrong. Fleet's
   `PUT/GET /api/v1/sync/{category}` accepts only the categories with a
   strict allowlist schema in kenaz-fleet `service/sync_schema.go`
   (`ParseSyncCategory`: PUT answers 422 `unknown_category`, GET 400), and
   migration `0062_user_sync_state` has a matching CHECK constraint. Every
   payload field must also be in that category's schema, or the PUT
   answers 422 `schema_violation`. Contract §2b says it plainly: ship the
   Fleet schema change before the harness change. Today the five built-in
   categories are accepted and `slash_commands` is not.

7. **Test the round trip**, mirroring
   `core/rpc/sync_categories_slashcmd_test.go`:
   - Collector: correct filtering (e.g. scope), credential-free, no
     device-local fields leaked.
   - Applier: valid items land, invalid items are skipped without
     aborting the batch, any scope/ownership fields the payload might
     smuggle are enforced server-side (not trusted from the wire).
   - A two-store "device A collects → device B applies" test proves the
     round trip without needing a live fleet server (see
     `TestSlashCommandsKind_TwoDeviceSync`).

## What this mission does *not* give you yet

- **Deletion / tombstone propagation.** Both WP01's categories and WP05's
  `slash_commands` are additive/upsert-only appliers — a command (or
  MCP install, or theme field) removed on one device is not removed on
  another device by a pull. This is a known, pre-existing gap across every
  LWW category, not something WP05 introduced.
- **A framework-generic conflict RESOLVER.** `ConflictPolicy` is
  registered per kind (e.g. `mcp_recipes` declares `org_wins_readonly`)
  but the actual shadow-vs-delete mechanics for a same-ID collision live
  inside each kind's own `Apply` (`core/mcp/recipes/merged.go`'s org-layer
  precedence, for `mcp_recipes`) — the framework cannot generically
  shadow an arbitrary opaque `[]byte` payload without knowing its
  ID-keyed shape. This was true before WP03 and remains true after it;
  it is not a gap WP03 closes, because there is no buildable v1 version
  of it.

**Landed since the table above was last accurate:**

- **Org/team scopes end-to-end (WP02).** The ConfigBundle `org_config`
  keyed section + `compositeConfigApplier.ApplyBundle`'s dispatch to each
  entry's registered kind (`core/rpc/views/settings/fleet.go`) are live.
- **Central secret-shape rejection (WP06, FR-006).** `core/fleet/
  secretshape.go`'s `SecretShapeReason` gates both `SyncKind.
  CategoryConfig()` (ScopeUser) and the org_config dispatch loop
  (ScopeOrg) — every kind's `SecretPolicy` is now actually consulted,
  not just validated for presence at registration.
- **Generic org-provenance tracking (WP03).** `KindRegistry.
  MarkOrgApplied`/`OrgAppliedAt`/`ClearOrgProvenance` record, per kind ID,
  whether an org_config entry has been successfully applied and when —
  kind-agnostic (unlike `mcp_recipes`' own `Recipe.Source` field, which is
  per-recipe). Cleared on sign-out (`StopFleetBackground`).
- **A UI surface listing registered kinds (WP06).** `Sync_Status` now
  enumerates every kind the `*fleet.Syncer` actually has state for
  (including kinds registered after the five built-ins, like
  `slash_commands`) instead of the earlier fleet-disabled fallback's
  hardcoded five-category list, and `SyncPanel.vue` renders whatever rows
  come back instead of a hardcoded `CATEGORIES` array.

## §6.3 decision — grandfathering `mandated_skills` (resolved, not escalated)

> **Superseded 2026-10-06:** the owner wire-contract ruling replaced
> `mandated_skills` with the `mandated_items` envelope
> (`[{catalog_id, kind, version, payload}]`, kenaz-fleet PR #178), still a
> bespoke signed `Bundle` field rather than an `org_config` kind. Applied by
> `fleet.MandatedApplier` (`core/fleet/mandated.go`). The text below is
> historical.

spec.md §6 item 3 asked review to pick between grandfathering
`mandated_skills` as its existing bespoke `Bundle` field or migrating it
into `org_config`. This did NOT need an owner escalation: plan.md's own
"Decision state" table already resolved it ("open — low stakes; default
grandfather, migrate opportunistically") before WP03/WP06 started. No
code change follows from this — `mandated_skills` stays exactly as
`fleet-skills-sync-01NDFSEX18` WP05 shipped it
(`compositeConfigApplier.ApplyBundle`'s `MandatedSkills` branch,
independent of the `OrgConfig` map WP02 added). Migrating it into
`org_config` remains available as later, purely-opportunistic work (a
`SyncKind` registration + a fleet-side wire-format change) — nothing
in WP03/WP06 forecloses it, and nothing here requires it.

## Known follow-ups

- **`check-codegen.sh` WAILSJS DRIFT.** WP03 added `tools.RecipeListing.
  Source` and WP06 added `sync.SyncStatusView.Scopes` /
  `.OrgAppliedAt` — both are fields on Wails-bound return types, so
  `frontend/wailsjs/go/models.ts`'s generated classes for them are now
  stale. This was deliberately not fixed by running `wails generate
  module` (see CLAUDE.md's "Tooling footguns" — it opens whatever
  database `HOME`/`KENAZ_HARNESS_ENV` resolve to) or by hand-editing
  `frontend/wailsjs/**`. Both endpoints work correctly today because
  their runtime call paths (`Bindings.js`) return raw parsed JSON rather
  than routing through `models.ts`'s conversion classes, and the
  frontend reads the new fields through hand-maintained interfaces
  (`WireRecipeListing` in `harnessClient.ts`; `SyncStatusView` in
  `types.ts` reads the Go json tags directly with no adapter). A `wails
  generate module` pass with the documented safety overrides is needed
  before `check-codegen.sh` goes green again.
- **Provider org-provenance is not renderable yet.** `provider_setups`
  (fleet-org-config-inheritance-01NORGX01 WP04) has no apply pipeline —
  `core/fleet/bundle_knob_coverage.go` still carries its
  `RegisterDeferred` entry, blocked on the same kenaz-fleet dev
  environment / encrypted org-key channel gate as before. WP03's
  "Providers" badge (spec §2.2, mirroring MCP recipes' "Provisioned by
  your org") cannot be built until that lands — there is no data source
  for it yet.

## Fleet-side ask

**Required, open.** Verified on 2026-10-15 against kenaz-fleet `main`
`a528ff4b` (provider-credentials-client-01PCCLI01 A2): Fleet enforces a
category allowlist. The five built-ins each have a strict schema in
`service/sync_schema.go`, and `slash_commands` has none, so Fleet rejects
it (PUT 422 `unknown_category`). The earlier text here guessed that the
endpoint was category-agnostic. It is not.

The harness therefore keeps `slash_commands` on the device
(`fleetAcceptedSyncCategories` in `core/fleet/sync.go`; the Sync panel row
says "Not syncing yet"). To ship it, Fleet first needs:

1. a `slash_commands` schema in `sync_schema.go` that matches
   `slashCommandSyncPayload` (`{items: [UserCommand…]}`, global scope only,
   `PayloadPath` already cleared on the wire), and
2. `slash_commands` added to the `user_sync_state.category` CHECK
   constraint.

Then add the id to `fleetAcceptedSyncCategories`. Fleet config-coverage §2
tracks this as P1.

## Wire and first upload (A2)

The wire is kenaz-fleet contract §2b: `PUT` with body
`{"lww_ts": "<RFC 3339>", "payload": {…}}`, and
`GET ?since=<RFC 3339>`, which answers 200 or 304. Before A2 the harness
sent `POST` with an int64 `lww_ts`, so settings sync had never
round-tripped. Fleet main has no `POST` route and no int64 `lww_ts` form
(no compatibility path, in any release), so only an A2 or later harness
can upload. The contract test is `core/fleet/sync_wire_test.go`, run
against fixtures in `core/fleet/testdata/sync/` (see `PROVENANCE.md`).

`provider_profiles` and `mcp_recipes` have no personal sync yet: their
user collector would send `{}` and their user applier does nothing. They
are registered with `UserSyncNotBuilt: true`, so the Syncer sends and pulls
nothing for them, `Sync_Status` reports `not_syncing_yet`, and the panel
and the upload confirmation never present them as syncing. (The
`mcp_recipes` org arm, applied through `org_config`, is unaffected.)

Because nothing had ever been uploaded, the first upload waits for a
one-time confirmation (owner ruling H1). The answer is stored in
`Settings.SettingsSyncUploadsConfirmed`, and the Syncer's upload gate
reads it. Downloads are not gated.
