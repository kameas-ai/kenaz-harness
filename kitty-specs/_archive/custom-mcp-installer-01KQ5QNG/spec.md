# Spec: Custom MCP server installer — install ANY MCP server from the Tools panel

**Mission ID**: `custom-mcp-installer-01KQ5QNG`
**Status**: draft
**Owner**: alecfeeman
**Planning base**: `main`
**Merge target**: `main`

## 1. Why this mission

The Tools panel today only knows about **shipped** recipes (Brave Search, Filesystem). A user with a custom MCP server — one they wrote, one a colleague published, one from the broader MCP ecosystem outside our curated catalog — has no UI path. They'd have to hand-edit `<DataDir>/mcp/recipes.enabled.json` and restart the harness.

This mission adds an "+ Add custom MCP server" affordance in the Tools panel that lets the user define their own recipe via a form (or paste a recipe JSON) and install it with the same one-click lifecycle as a shipped recipe. The custom recipe persists alongside shipped recipes; install/uninstall/forget-key/status all behave identically.

## 2. Goals

- **"+ Add custom MCP server" button** in `KaneazToolsPanel.vue` that opens a dedicated modal collecting:
  - Display name + ID + description + category.
  - Command (program + args).
  - Environment keys (name, display label, docs URL, required flag) — variable count.
  - Capabilities toggles (tools / resources / prompts / sampling).
  - Optional config options (directory_list / boolean / string), so custom recipes can declare their own configurable parameters the same way the filesystem recipe does.
- **Two creation paths**:
  1. **Form** — guided UI for the typical MCP server.
  2. **Paste JSON** — for users with a recipe definition already in JSON; pasted text is validated against the same `Recipe` schema shipped recipes use.
- **Persisted as user recipes** in a separate `<DataDir>/mcp/custom_recipes.json` file; loaded into the catalog at boot alongside the embedded shipped catalog.
- **Edit + Delete** affordances for custom recipes only (shipped recipes remain immutable).
- **Same lifecycle as shipped**: install/uninstall/forget-key/status pill all work identically.
- **Same security gates**: command path is validated (no shell injection via the command field); env keys go through the keychain; if the recipe declares a `directory_list` ConfigOption, paths still pass through `path_validation.go`.

## 3. Non-goals

- Auto-discovery of MCP servers (scanning npm, GitHub, etc.). User explicitly defines or pastes a recipe.
- A community catalog / marketplace inside the harness. Users share recipes via JSON paste-and-share for now.
- Sandbox enforcement on custom server processes beyond what we already do (process isolation via stdio, no chroot).
- Recipe versioning / update channels. Custom recipes are static once created; user edits manually.
- Importing recipes from URLs (HTTP fetch). Paste-only for v1 to avoid an SSRF surface.

## 4. User stories

- **US1** As a user with a Python MCP server I wrote (`uv run my-mcp-server`), I open Tools → "+ Add custom MCP server" → fill the form (Name: "My Server", ID: "my-server", Command: `["uv", "run", "my-mcp-server"]`, no env keys, capabilities: tools=true) → click Save → recipe appears in the list. Toggle on → server spawns → tools appear to the model.
- **US2** As a user with a recipe JSON from a colleague's GitHub repo, I open the modal → paste the JSON → validation passes → click Save → recipe added.
- **US3** As a user with an existing custom recipe, I want to update the command (`v1.2` → `v1.3`). Right-click the row → "Edit" → modal opens pre-filled → I change the command → Save. The recipe is updated; if it was running, it restarts with the new command.
- **US4** As a user, I want to delete a custom recipe. Right-click → "Delete" → confirmation → recipe removed (server stopped if running, keychain entries forgotten, recipe removed from the catalog).
- **US5** As a user, I try to add a recipe with an ID that collides with a shipped recipe (`brave-search`). Validation rejects with a clear error.
- **US6** As a user, I paste malformed JSON. Validation rejects with the specific parse error in the modal banner.
- **US7** As a user, I declare a `directory_list` ConfigOption in my custom recipe. After install, the path-validation layer enforces the same deny-list as filesystem-mcp's directories. I get the same modal error if I try to allow `/etc`.

## 5. Functional requirements

### 5.1 Custom recipe storage

- **FR-001** New file `<DataDir>/mcp/custom_recipes.json` holding the array of user-defined `Recipe` objects. JSON shape identical to `shipped.json`'s `recipes` array element. Atomic write (tmp + rename + fsync) — same pattern as `recipes.enabled.json`.
- **FR-002** Loaded by `core/mcp/recipes/custom.go::LoadCustom(dataDir)`. Missing file → empty list. Corrupt JSON → log warn + start with empty list (never block boot on a bad custom file).
- **FR-003** Catalog merging: `Catalog` (existing, from stdio-pool WP04) becomes a union of `shipped` (embed.FS) + `custom` (loaded at boot from disk). When a custom recipe has the same ID as a shipped recipe, **shipped wins** and a warn is logged. Custom recipe IDs that collide with shipped are rejected at the validation step (FR-006).
- **FR-004** Custom recipes carry an additional flag in the runtime catalog: `Origin string` ("shipped" | "custom"). The `RecipeListing` wire-shape gains this field so the frontend knows whether to expose Edit/Delete affordances.

### 5.2 RPC surface

- **FR-005** New RPC methods on `ToolsAPI`:
  ```go
  type ToolsAPI interface {
      // ... existing methods ...
      AddCustomRecipe(ctx context.Context, recipe recipes.Recipe) (recipes.Recipe, error)
      UpdateCustomRecipe(ctx context.Context, id string, recipe recipes.Recipe) (recipes.Recipe, error)
      DeleteCustomRecipe(ctx context.Context, id string) error
      ValidateRecipeJSON(ctx context.Context, jsonText string) (recipes.Recipe, error)
  }
  ```
- **FR-006** Validation rules (applied by `Add` and `Update`):
  - ID matches `^[a-z][a-z0-9-]{0,63}$`.
  - ID not in shipped catalog (collision rejected).
  - `Command[0]` non-empty.
  - Each `EnvKey.Name` is uppercase alphanumeric + underscore (matches `^[A-Z][A-Z0-9_]*$`).
  - `Category` is one of the known values; unknown defaults to `"other"`.
  - Each `ConfigOption.Kind` is one of `directory_list | boolean | string`.
  - `Capabilities` is a struct of bools; missing fields default to false.
- **FR-007** `ValidateRecipeJSON` is the paste-mode entry point: takes raw JSON text, runs JSON parse + the FR-006 validation, returns the validated `Recipe` for confirmation in the modal — does NOT persist. The user sees what would be added before committing.
- **FR-008** Bindings: `Tools_AddCustomRecipe`, `Tools_UpdateCustomRecipe`, `Tools_DeleteCustomRecipe`, `Tools_ValidateRecipeJSON`.

### 5.3 Lifecycle

- **FR-009** **Add**: validate → write to `custom_recipes.json` (atomic) → reload catalog → return the recipe. The recipe is now in the catalog but **not enabled** until the user toggles it on.
- **FR-010** **Update**: validate → write → reload catalog → if the recipe is currently enabled, the running server is restarted with the new command. New env-key declarations that weren't present before don't auto-prompt; the user re-toggles or sees a "Re-install required" hint.
- **FR-011** **Delete**: if currently enabled, uninstall first (kills server, persists). Remove keychain entries for all of the recipe's env keys (call `ForgetRecipeKey` for each). Remove from `custom_recipes.json`. Reload catalog. Audit emit `mcp.custom_recipe.deleted`.

### 5.4 Frontend

- **FR-012** **"+ Add custom MCP server" button** in `KaneazToolsPanel.vue`, placed below the "Connected MCP recipes" header, before the recipe rows. Click → opens `AddCustomRecipeModal.vue`.
- **FR-013** **`AddCustomRecipeModal.vue` (new)**:
  - Two tabs: **Form** (default) | **Paste JSON**.
  - **Form** fields (in modal):
    - Display name (text)
    - ID (text, lowercase only, validated client-side as the user types)
    - Description (textarea)
    - Category (select: search / filesystem / memory / fetch / other)
    - Command (chip-list of args; first chip is the program, subsequent chips are positional args)
    - Env keys (variable; "+ Add env key" → row with Name / Display / Docs URL / Required toggle)
    - Capabilities (4 checkboxes: tools / resources / prompts / sampling)
    - Config options (variable; "+ Add config option" → row with Name / Display / Kind dropdown / Default / Required toggle / Description)
  - **Paste JSON** tab: textarea + "Validate" button → calls `ValidateRecipeJSON`; on success, the parsed recipe is shown as a read-only preview + "Save" button; on error, banner displays the validation error.
  - Submit button: calls `Tools_AddCustomRecipe` with the form-built recipe (or the validated paste-recipe).
- **FR-014** **Edit affordance** on existing custom recipe rows in the panel: small ⋯ menu with "Edit" → opens the modal pre-filled with the existing recipe + change-detection on submit (only call `UpdateCustomRecipe` if anything changed).
- **FR-015** **Delete affordance** in the same ⋯ menu: "Delete" → confirmation dialog (lists the env keys that will be forgotten + warns if currently enabled) → calls `DeleteCustomRecipe`.
- **FR-016** Shipped recipes: ⋯ menu shows ONLY the existing affordances ("Open workspace" for filesystem, etc.) — no Edit / Delete (they're immutable).

### 5.5 Wiring

- **FR-017** Catalog construction in `core/rpc/api.go newLLMStack`:
  - On boot: `recipes.LoadShipped()` + `recipes.LoadCustom(c.DataDir())` → `Catalog{shipped: ..., custom: ...}`. Lookup walks shipped first, then custom; collisions logged but never returned (custom is shadowed).
  - The `*tools.API` holds a reference to the catalog AND the custom store, so `AddCustomRecipe` can update both atomically.

## 6. Non-functional requirements

- **NFR-001** `go test -race -count=1 -short ./core/...` ≥ baseline + new tests.
- **NFR-002** Frontend tests + build clean.
- **NFR-003** Custom recipes added at runtime are immediately visible in `Tools_ListRecipes` (no harness restart required). Verified by a test that adds a recipe + asserts it shows in the next `ListRecipes` call.
- **NFR-004** No plaintext keychain values written to `custom_recipes.json` — only env-key DECLARATIONS (name, display, docs URL, required flag). Env values still go through the keychain on install.
- **NFR-005** Adding 50 custom recipes doesn't degrade catalog-load time noticeably (< 100 ms for `LoadCustom`).
- **NFR-006** Validation happens server-side. Frontend client-side validation is best-effort (catches obvious errors fast); the server is the source of truth.

## 7. Acceptance criteria

- **A1** US1 form-add: form-built recipe persisted; toggle on spawns the server; tools surface to the model.
- **A2** US2 paste-add: pasted JSON validated; recipe persisted; toggle on works.
- **A3** US3 edit: change the command; running recipe restarts with new command.
- **A4** US4 delete: keychain entries gone; recipe removed; running server killed.
- **A5** US5 ID collision: form/paste rejection with friendly error.
- **A6** US6 malformed JSON: paste-mode validation rejection with parse error in banner.
- **A7** US7 directory_list ConfigOption deny-list inheritance: same `path_validation.go` logic applies.
- **A8** Catalog merge: a custom recipe with the same ID as a shipped recipe is rejected at add-time; never silently shadowed.
- **A9** Round-trip: harness restart preserves all custom recipes (including their config and enabled state).
- **A10** Edit affordance NOT shown on shipped recipes; ONLY on custom recipes.

## 8. Architecture

```
core/mcp/recipes/
├── recipes.go                 # MODIFIED: Recipe gains Origin string ("shipped"|"custom")
├── shipped.json               # MODIFIED: each entry's Origin is "shipped" (set programmatically on load)
├── custom.go                  # NEW: LoadCustom / SaveCustom / atomic write
├── custom_test.go
└── catalog.go                 # NEW or MODIFIED: union catalog (shipped + custom merge)

core/rpc/views/tools/
├── api.go                     # MODIFIED: AddCustomRecipe / UpdateCustomRecipe / DeleteCustomRecipe / ValidateRecipeJSON
├── impl.go                    # MODIFIED: implementations + audit emit
├── impl_custom_test.go        # NEW
└── path_validation.go         # UNCHANGED — reused for directory_list ConfigOptions

core/rpc/api.go                # MODIFIED: catalog construction merges shipped + custom
core/rpc/bindings.go           # MODIFIED: Tools_AddCustomRecipe + UpdateCustomRecipe + DeleteCustomRecipe + ValidateRecipeJSON
core/rpc/stubs.go              # MODIFIED: stub impls

frontend/src/views/tools/
├── KaneazToolsPanel.vue       # MODIFIED: "+ Add custom MCP server" button + ⋯ menu on custom rows
├── AddCustomRecipeModal.vue   # NEW: form + paste tabs
├── EnvKeysEditor.vue          # NEW: variable-count env-key editor
├── CommandArgsEditor.vue      # NEW: chip-list for command args
├── CustomRecipeRowMenu.vue    # NEW: ⋯ menu (Edit / Delete) for custom rows
└── __tests__/

frontend/src/lib/types.ts      # MODIFIED: Recipe.origin field
frontend/src/lib/harnessClient.ts  # MODIFIED: tools.recipes namespace adds add/update/delete/validate

docs/mcp-recipes.md            # MODIFIED: append "Adding custom recipes" section
```

## 9. Edge cases

1. User adds a recipe with no env keys and no config options → still valid; just spawns the command directly. Common case for trivial MCP servers.
2. User changes a recipe's ID via Edit → reject; ID is the primary key. Force the user to delete + re-create if they want a new ID.
3. User deletes a recipe whose server crashed and entered `failed` state → Delete still cleans up (keychain + custom_recipes.json + catalog).
4. Two concurrent saves of the custom file (rare; single user) → mutex inside `tools.API`; atomic-write guarantees no torn JSON on disk.
5. Recipe with malformed `Capabilities.Sampling: "yes"` (string instead of bool) → JSON parse fails at Validate step; modal shows the parse error.
6. Recipe with negative `init_timeout_ms` → validation rejects (must be > 0).
7. Paste mode receives a recipe array (multiple) → reject in v1: "paste a single recipe object". Multi-paste is a future enhancement.
8. Custom recipe declares `sampling_policy.allowed=true` and `default=true` → server-initiated `sampling/createMessage` works for that recipe (matching shipped behavior). Note in the modal "Sampling allows the server to call your active LLM provider" so user understands cost.
9. Recipe whose command resolves to a path not on `$PATH` at install time → `pool.OpenOne` will fail with a clear error; the modal surfaces it without rolling back the recipe addition (the user can edit + retry).

## 10. Out of scope

- Marketplace / community catalog inside the harness.
- HTTP recipe import (URL-fetch).
- Sandbox enforcement beyond stdio.
- Recipe signing / signature verification.
- Recipe version channels / auto-update.
- Multi-recipe paste in one shot.
- Editing shipped recipes (immutability is intentional — easier user mental model).

## 11. Open questions

1. **Recipe authoring UX** — form vs paste-JSON ratio. Most users will use form; power users paste. Default tab = form.
2. **ID validation regex** — `^[a-z][a-z0-9-]{0,63}$` matches the existing shipped recipe IDs. Open: should we also allow `_`? Current answer: no, keep dash-only for visual consistency.
3. **`Origin` field surfacing** — frontend uses it to gate Edit/Delete. Open: should the status pill render differently for custom vs shipped? Probably no — the user knows because of the ⋯ menu's contents.
4. **Sampling on custom recipes** — allowed when the recipe says so. Default off. UI shows the cost-amplification warning.
5. **Custom recipe count limit** — none in v1. If the catalog gets unwieldy, future filter / search is a follow-up.

## 12. Out-of-band dependencies

None. This mission depends only on the existing stdio-pool + filesystem-mcp infrastructure.
