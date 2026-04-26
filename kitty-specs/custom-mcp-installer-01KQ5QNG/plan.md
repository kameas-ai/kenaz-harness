# Implementation Plan: Custom MCP server installer

**Branch**: `custom-mcp-installer-01KQ5QNG` (lane allocated at WP-implement time)
**Date**: 2026-04-26
**Spec**: `kitty-specs/custom-mcp-installer-01KQ5QNG/spec.md`

## Summary

Add a "+ Add custom MCP server" affordance to the Tools panel that lets users define their own MCP-server recipe via a form or by pasting JSON. Custom recipes persist in `<DataDir>/mcp/custom_recipes.json` alongside the embedded shipped catalog. Same install/uninstall/forget-key/status lifecycle as shipped. Edit + Delete affordances exposed only on custom recipes (shipped stay immutable).

## Technical Context

- **Language/Version**: Go 1.22+; TypeScript 5.x.
- **Primary Dependencies**: stdlib only on the Go side; `core/mcp/recipes` (existing) for the schema; reuse `path_validation.go` for `directory_list` ConfigOptions.
- **Storage**: `<DataDir>/mcp/custom_recipes.json` — atomic write (tmp + fsync + rename + parent fsync). Same pattern as `recipes.enabled.json`.
- **Testing**: Go `-race -count=1 -short`; vitest for the modal + form components.
- **Performance**: NFR-005 — 50 custom recipes load in < 100 ms.
- **Constraints**: NFR-004 — no plaintext keychain values in `custom_recipes.json`.

## Charter Check

- DIRECTIVE_001 (no cyclic imports): `core/mcp/recipes/custom.go` only depends on stdlib + the existing `recipes` package types. No new cycles.
- C-001 (no third-party SDK in `core/`): stdlib only.
- Privacy CI: custom recipes carry only declarations — env-key NAMES, not values. Audit covered.

## Project Structure

```
core/mcp/recipes/
├── recipes.go                 # MODIFIED: Recipe.Origin string field
├── custom.go                  # NEW: LoadCustom / SaveCustom + atomic-write helpers
├── custom_test.go             # NEW
├── catalog.go                 # NEW: union of shipped + custom; LookupRecipe; ListAll
├── catalog_test.go            # NEW

core/rpc/views/tools/
├── api.go                     # MODIFIED: 4 new methods (Add/Update/Delete/Validate Custom Recipe)
├── impl.go                    # MODIFIED
├── impl_custom_test.go        # NEW

core/rpc/api.go                # MODIFIED: catalog construction merges shipped + custom
core/rpc/bindings.go           # MODIFIED
core/rpc/stubs.go              # MODIFIED

frontend/src/views/tools/
├── KaneazToolsPanel.vue       # MODIFIED: + Add custom button + ⋯ menu on custom rows
├── AddCustomRecipeModal.vue   # NEW: tabbed Form / Paste-JSON
├── EnvKeysEditor.vue          # NEW
├── CommandArgsEditor.vue      # NEW
├── CustomRecipeRowMenu.vue    # NEW
└── __tests__/

frontend/src/lib/types.ts      # MODIFIED: Recipe.origin
frontend/src/lib/harnessClient.ts  # MODIFIED: tools.recipes.{add,update,delete,validate}Custom

docs/mcp-recipes.md            # MODIFIED: append "Adding custom recipes"
```

## Phase 0 — Research summary

- Existing `Recipe` struct in `core/mcp/recipes/recipes.go` has all the fields needed (`Command`, `EnvKeys`, `Capabilities`, `ArgsTemplate`, `ConfigOptions`). One additive field needed: `Origin string`.
- Atomic-write pattern: see `core/mcp/recipes/enabled.go`. Same code copied (or refactored to a shared helper if both diverge).
- ID-collision check: walk shipped catalog first; if ID matches, reject. Custom-vs-custom collisions caught by checking the existing custom list.
- Shadowing semantics: in the merged catalog `LookupRecipe(id)`, shipped wins on collision — but since we reject collisions at `Add` time, this only matters when a future shipped catalog update introduces the same ID as an existing custom recipe. In that case we log a warn at boot and let the user resolve manually (delete the custom or rename).

## Phase 1 — Schema + storage

**Targets**: `core/mcp/recipes/recipes.go` (Origin field), `custom.go` + `custom_test.go`.

- Add `Recipe.Origin string` (`"shipped"` | `"custom"`). Default `""` parses; loader fills in.
- `custom.go`:
  - `LoadCustom(dataDir) (*CustomRecipes, error)` — returns empty on missing file; logs warn + returns empty on corrupt.
  - `(*CustomRecipes).Add(recipe) (Recipe, error)` — validates via `validateRecipe` (FR-006); rejects ID collision; appends.
  - `(*CustomRecipes).Update(id, recipe) (Recipe, error)` — replaces in place; same validation.
  - `(*CustomRecipes).Delete(id) error` — removes by ID.
  - `(*CustomRecipes).Get(id) (Recipe, bool)`, `List() []Recipe`.
  - `Save(dataDir) error` — atomic write.
- Tests: round-trip; missing file; corrupt file; ID collision; concurrent Save (mutex).

**Dependencies**: none.

## Phase 2 — Catalog union

**Targets**: `core/mcp/recipes/catalog.go` + `catalog_test.go`.

```go
type Catalog struct {
    shipped *ShippedCatalog
    custom  *CustomRecipes
    mu      sync.RWMutex
}

func NewCatalog(shipped *ShippedCatalog, custom *CustomRecipes) *Catalog
func (c *Catalog) Lookup(id string) (Recipe, bool)
func (c *Catalog) List() []Recipe  // shipped first, then custom; both annotated with Origin
func (c *Catalog) ReloadCustom(custom *CustomRecipes)  // for after Add/Update/Delete
```

Tests: lookup hits shipped first; collision logs warn; ReloadCustom replaces custom side without touching shipped.

**Dependencies**: Phase 1.

## Phase 3 — Validation rules

**Targets**: `core/mcp/recipes/recipes.go` (move existing per-field checks into a single `ValidateRecipe(r) error` if not already there).

Validation rules per FR-006:
- ID matches `^[a-z][a-z0-9-]{0,63}$`.
- ID not in shipped catalog (check via injected `shippedIDs` set).
- `Command[0]` non-empty.
- Each `EnvKey.Name` matches `^[A-Z][A-Z0-9_]*$`.
- Each `ConfigOption.Kind` is one of the known kinds.
- `Capabilities` is a struct of bools (parse-time guarantee from JSON).
- `init_timeout_ms` and `ping_period_ms` ≥ 0; 0 means default.

Tests: every rejection case + happy path.

**Dependencies**: Phase 1.

## Phase 4 — RPC view methods

**Targets**: `core/rpc/views/tools/api.go`, `impl.go`, `impl_custom_test.go`.

`AddCustomRecipe(ctx, recipe)`:
1. Lock the API mutex.
2. Validate (Phase 3). If ID collides with shipped → reject. If ID collides with existing custom → reject.
3. `customStore.Add(recipe)` → save to disk.
4. `catalog.ReloadCustom(customStore)`.
5. Audit emit `mcp.custom_recipe.added` with `{id, display_name}`.
6. Return the inserted recipe (now with `Origin: "custom"`).

`UpdateCustomRecipe(ctx, id, recipe)`:
1. Lock.
2. Reject if `id != recipe.ID` (force ID immutability).
3. Reject if recipe is shipped (only custom is editable).
4. Validate.
5. `customStore.Update(...)`; save.
6. `catalog.ReloadCustom(customStore)`.
7. If currently enabled, restart: `pool.CloseOne(ctx, id)` then re-resolve env + config + `pool.OpenOne(spec)`.
8. Audit emit `mcp.custom_recipe.updated`.
9. Return the updated recipe.

`DeleteCustomRecipe(ctx, id)`:
1. Lock.
2. If running, uninstall first.
3. For each EnvKey on the recipe, call `secrets.Backend.Delete(KeychainLocator(id, envName))`.
4. `customStore.Delete(id)`; save.
5. `catalog.ReloadCustom(customStore)`.
6. Audit emit `mcp.custom_recipe.deleted`.

`ValidateRecipeJSON(ctx, jsonText) (Recipe, error)`:
1. `json.Unmarshal` into `Recipe`. Wrap parse errors with friendly messages.
2. Validate (Phase 3). Surface field-specific errors.
3. Set `Origin: "custom"` on the returned recipe (so the modal preview reflects it).
4. Does NOT persist.

Tests for each method: happy path, validation rejection, ID collision, sample edit-while-running flow.

**Dependencies**: Phases 1-3.

## Phase 5 — Bindings + chassis wiring

**Targets**: `core/rpc/bindings.go`, `core/rpc/api.go`, `core/rpc/stubs.go`.

- `Tools_AddCustomRecipe`, `Tools_UpdateCustomRecipe`, `Tools_DeleteCustomRecipe`, `Tools_ValidateRecipeJSON` bindings.
- `core/rpc/api.go newLLMStack`: instantiate `customStore = recipes.LoadCustom(c.DataDir())`; build `catalog = recipes.NewCatalog(shipped, customStore)`. Pass both to `tools.API`.
- Stub the new methods in `core/rpc/stubs.go`.

**Dependencies**: Phase 4.

## Phase 6 — Frontend modal + form + paste tab

**Targets**: `AddCustomRecipeModal.vue` (new), `EnvKeysEditor.vue` (new), `CommandArgsEditor.vue` (new), `CustomRecipeRowMenu.vue` (new), `KaneazToolsPanel.vue` (modified), `lib/types.ts`, `lib/harnessClient.ts`.

- `lib/types.ts`: add `Recipe.origin: 'shipped' | 'custom'`. Update existing `RecipeListing` shape.
- `lib/harnessClient.ts`: `tools.recipes.{addCustom, updateCustom, deleteCustom, validateRecipeJSON}`.
- `AddCustomRecipeModal.vue`:
  - Two tabs: Form (default) | Paste JSON.
  - **Form**: structured fields per FR-013. Client-side validation that mirrors backend rules (best-effort fast-feedback; backend is source of truth).
  - **Paste JSON**: textarea + Validate button → calls `validateRecipeJSON` → preview block (read-only summary) + Save button.
  - Submit dispatches to `addCustom` (create) or `updateCustom` (edit). Surfaces backend errors in a banner.
- `EnvKeysEditor.vue`: variable-row editor (Name / Display / Docs URL / Required). v-model: `EnvKey[]`.
- `CommandArgsEditor.vue`: chip-list for argv. First chip is required (program path).
- `CustomRecipeRowMenu.vue`: ⋯ button → small dropdown with Edit / Delete. Wired into `KaneazToolsPanel.vue` per-row, conditional on `recipe.origin === 'custom'`.
- `KaneazToolsPanel.vue`: "+ Add custom MCP server" button below the Connected MCP recipes header.

Tests:
- `AddCustomRecipeModal.test.ts`: tab switching; form-validate-on-blur; paste-validate path; submit.
- `EnvKeysEditor.test.ts`: add/remove rows; v-model contract.
- `CommandArgsEditor.test.ts`: chip add/remove/edit.
- `CustomRecipeRowMenu.test.ts`: open menu; emits Edit / Delete.
- `KaneazToolsPanel.test.ts` extension: "+ Add" button opens modal; ⋯ menu visible on custom rows only.

**Dependencies**: Phase 5.

## Phase 7 — Polish + docs

**Targets**: `docs/mcp-recipes.md`.

Append "Adding custom recipes" section:
- Walkthrough: form-mode end-to-end with a hello-world Python MCP server example.
- Walkthrough: paste-mode with a sample JSON.
- Edit + Delete flow.
- Security notes (env keys still go through keychain; deny-list still applies to directory_list configs).
- "Sharing recipes" — paste-export-to-clipboard pattern.

Manual verification checklist for A1-A10.

**Dependencies**: Phases 1-6.

## Work-package breakdown (proposed)

- **WP01 — Schema + storage + validation + RPC** (Phases 1, 2, 3, 4, 5). Backend additive. Lands the four new RPC methods, the custom recipes file, the catalog union, the bindings. After this WP, custom recipes work via direct rpc calls (no UI yet).
- **WP02 — Frontend modal + form + paste tab + per-row menu** (Phase 6). Lands the user-facing UI.
- **WP03 — Polish + docs** (Phase 7). Walkthroughs + manual checklist + sample recipe gallery.

DAG: WP01 → WP02 → WP03.

## Risk register

| Risk | Phase | Mitigation |
|---|---|---|
| User adds a recipe that points at a malicious binary | 4 | Out of scope to defend against; same trust model as filesystem MCP allowed_directories. Document: "Custom recipes run with your user privilege; only add servers you trust." |
| ID collision with a future shipped recipe (catalog grows post-user-adoption) | 2, 4 | Boot-time merge logs warn for collisions; user sees a Tools-panel banner "Custom recipe `X` collides with a shipped recipe; rename or delete." |
| Atomic write race (two concurrent Adds) | 1, 4 | API mutex inside `tools.API` serializes; atomic-write semantics guarantee no torn JSON. |
| Edit while running causes corrupt restart | 4 | Validate BEFORE persisting; if restart fails, the recipe's persisted definition is the new one but `pool` shows the recipe in `failed` state. User can fix or revert. |
| Recipe with `Capabilities.Sampling: true` + bad sampler config crashes the LLM | 4 | Sampling already gated per-server (default off); the sampling adapter handles errors. The custom recipe inherits the same protection. |
| Frontend type drift from Wails autogen | 6 | Hand-edit `frontend/wailsjs/go/{models.ts,rpc/Bindings.{d.ts,js}}` for the new bindings (matching the WP06/WP03 stdio-pool/filesystem pattern). |
| Paste-mode XSS via display fields | 6 | All Recipe fields render via `{{ }}` text interpolation, never v-html. Validation rejects HTML in display fields anyway. |
| Custom recipe with `directory_list` ConfigOption containing path traversal | 4 | Reuses `path_validation.go`'s deny-list at install time. Same guarantee as filesystem-mcp. |
| Catalog memory growth with many custom recipes | 1 | Reasonable upper bound documented (1000 recipes); catalog list query stays O(n) which is fine at that scale. |

## Open questions

1. ID validation regex — keep `^[a-z][a-z0-9-]{0,63}$` (no `_`).
2. Sampling toggle on custom recipes — allowed; default off; modal warns about cost.
3. Multi-recipe paste — defer to a future enhancement.
4. Recipe icons — custom recipes use the category-icon mapping (search → Search, etc.). Custom recipes with `category: "other"` get the default `Wrench` icon.
5. **NEW** — Should custom recipes be exportable as a JSON file (download from the ⋯ menu)? Useful for sharing. Probably yes for v1.1; not v1 mission scope.
