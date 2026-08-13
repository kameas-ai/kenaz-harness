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

5. **No `core/fleet/sync.go` changes needed.** `RegisterCategory` lazily
   creates the category's runtime state and the background poll loop
   iterates whatever is currently registered — this is the WP01 genericity
   fix. A kind registered after `StartPolling()` has already run still
   gets picked up on the next tick.

6. **No fleet-server changes needed** for a plain user-scoped LWW kind —
   `/api/v1/sync/{category}` accepts arbitrary category strings already.
   *(If fleet enforces a category allowlist that this codebase doesn't see
   — e.g. a DB enum or admin-configured list — add the new category string
   there. See "Fleet-side ask" below for the exact, unverified, one-line
   ask this mission is flagging for `slash_commands`.)*

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
- **Org/team scopes end-to-end.** `Scopes` can declare `ScopeTeam` /
  `ScopeOrg`, and `KindApplier` takes a `Scope` parameter for this reason,
  but nothing dispatches an `Apply(ctx, ScopeOrg, …)` call yet — that's the
  ConfigBundle `org_config` composite applier, WP02 of this mission.
- **A UI surface listing registered kinds.** `KindRegistry.Kinds()` /
  `IDs()` exist and are held on the RPC `API` struct
  (`a.syncKindRegistry`), but the Settings → Sync surface that would
  render them (scope / last-sync / provenance / toggle) is WP06.

## Fleet-side ask (if any)

None verified as required. `/api/v1/sync/{category}` is documented in this
repo (`core/fleet/sync.go` header, `DESIGN.md` §5.6) as already generic
server-side — no per-category schema or allowlist is referenced anywhere
in the harness codebase. `slash_commands` was registered and exercised
end-to-end at the harness layer (collector → applier, and the Syncer's
push/pull/poll plumbing) without touching any fleet contract.

The one thing this mission could not verify from the harness repo alone:
whether `kenaz-fleet`'s `/api/v1/sync/{category}` handler enforces a
category allowlist (e.g. a DB enum, or an admin-configured list of known
categories) that isn't visible here. **If it does, the one-line ask is:
add `"slash_commands"` to that allowlist alongside the five existing
category strings.** If the endpoint is truly category-agnostic as
documented, no fleet-side change is needed at all.
