# Agents RPC — served-mode exposure (fix/served-agent-profiles-sep17)

**Status:** DRAFT — local to kenaz-harness; not yet mirrored into workspace `CONTRACTS.md`
**Owner:** `golang` (Go surface), `frontend` (Vue view + TS client)
**Last updated:** 2026-09-17
**Upstream:** the four `Agents_*` methods already exist on desktop
(`core/rpc/views/agents`, mission `branch-subagent-interactive-01KZNP3B` WP01)
and are Wails-bound in `core/rpc/api.go` / `core/rpc/bindings.go`. This note
gates porting them to served mode — nothing below may be implemented
differently without changing this file first (same discipline as
`contracts/documents-rpc.md`).

## 1. Scope

Expose the existing `Agents_ListProfiles` / `Agents_LoadProfile` /
`Agents_SaveProfile` / `Agents_DeleteProfile` family over `POST /rpc` so a
served workbench can configure named sub-agent profiles (model, autonomy
tier, budgets, merge policy) — the same CRUD desktop already has. No new
methods, no new fields, no new UI beyond linking the existing `AgentsView.vue`
into served Settings.

**Out of scope:** provider/credential configuration (Secrets_*, LLM
provider setup) — not touched, not exposed. A profile's `model` field stays
a plain string the operator types (already true on desktop —
`AgentProfileEditor.vue`'s Model field is a bare text input, not a provider
picker); this change does not add a model catalog or validate the string
against one. Desktop's own Settings navigation currently has **no route to
AgentsView at all** (found during this change — it is reachable from
neither `SettingsTabs.vue` nor `SettingsView.vue`'s tab switch on any
platform); fixing that is a separate, larger nav-IA decision and is
explicitly not part of this fix.

## 2. Authorization and visibility

1. **Transport auth is unchanged.** Served mode: the single
   `HARNESS_SERVE_TOKEN` bearer on `/rpc`, exactly as for `Sessions_*` /
   `Documents_*`. No per-method gradation.
2. **Storage scope is per-DataDir, not per-session.** Profiles live at
   `<DataDir>/agents/*.yaml` (bundled profiles ship in the binary via
   `//go:embed`). A served workbench's single bearer token is already the
   entire trust boundary for everything under its DataDir (sessions,
   documents, settings); this family adds nothing new to that model.
3. **Bundled profiles stay read-only.** `SaveProfile`/`DeleteProfile` both
   reject a bundled id with `ErrBundledReadOnly` — unchanged, verified by
   existing `core/agents` tests and re-verified against the served
   transport in this change (`served_agents_rpc_test.go`).
4. **ID traversal guard, closed as part of this change, not carried
   forward.** `Delete`'s `id` was joined straight into
   `<dataDir>/agents/<id>.yaml` with **no format validation** — unlike
   `Save`, which validates via `Validate(p)` (id = lowercase alphanumeric +
   hyphens) before ever touching the filesystem. Only reachable via the
   trusted desktop Wails bridge before this change; exposing the same
   method over a network-reachable HTTP transport is exactly the kind of
   change that turns a latent gap into a real one, so `Delete` now
   validates the id up front too (`core/agents/loader.go`). Proven with a
   real path-traversal regression test
   (`core/agents/loader_test.go::TestDeleteRejectsPathTraversal`) that
   fails against the pre-fix code (verified locally by reverting the fix
   and re-running).

## 3. Methods

All params and results are JSON objects with camelCase keys, matching the
existing Wails wire shapes in `core/rpc/views/agents` verbatim — no
re-shaping for the transport.

| Method | Params | Result |
|---|---|---|
| `Agents_ListProfiles` | `{}` | `ProfileSummaryWire[]` |
| `Agents_LoadProfile` | `{id}` | `ProfileWire` |
| `Agents_SaveProfile` | `ProfileWire` (whole object, no wrapper) | `null` |
| `Agents_DeleteProfile` | `{id}` | `null` |

`ProfileSummaryWire` / `ProfileWire` are exactly `core/rpc/views/agents`'
existing Go structs (see that package's doc comment) — served mode adds no
new fields and no new wire shape.

## 4. Errors

Errors travel as the transport's bare error string (served `{"error":
"..."}`), same as desktop's Wails rejection — this family has no
`family: code: message` convention like `Documents_*`; it forwards the
underlying Go error text (e.g. `"agents.SaveProfile: agents: bundled
profiles are read-only"`, `"agents.LoadProfile: agents: profile not
found: \"x\""`). Callers that need to branch on bundled-vs-not can match on
the `ErrBundledReadOnly`/`ErrProfileNotFound` substrings, same as the
existing desktop caller does today (`AgentsView.vue` currently shows the
raw message, not a parsed code).

## 5. Binding rules (enforced)

- Go: `*rpc.API.Agents()` ↔ `core/serve/server.go` dispatch cases ↔
  `core/serve/methods.go` `servedMethods` — `TestServedMethodsMatchDispatchSwitch`
  (`core/serve/methods_test.go`) parses `server.go`'s switch and fails on
  drift; no separate script needed beyond the existing generic
  `check-serve-dispatch-drift.sh`.
- TS: `HarnessClient.agents` (`AgentsClient`), `createServedHarnessClient().agents`
  — `harnessClient.agents.test.ts` asserts the served overlay calls exactly
  the methods `core/serve/methods.go` lists, with the contract param shapes
  (same drift-guard pattern as `harnessClient.documents.test.ts`).
- Caller: `views/settings/AgentsView.vue`, linked into
  `SettingsView.vue`'s served-mode branch (alongside `FleetTelemetryPanel`)
  — not routed separately; served Settings has no tab rail, only the
  stacked-panel layout that branch already uses.

## 6. What would make this stale

Adding a provider/credential picker to the Model field, changing the wire
shape of `ProfileWire`, adding per-method auth, or wiring AgentsView into
desktop's Settings navigation (a real, separate gap noted in §1 but not
fixed here). Each of those changes this file and must land here first.
