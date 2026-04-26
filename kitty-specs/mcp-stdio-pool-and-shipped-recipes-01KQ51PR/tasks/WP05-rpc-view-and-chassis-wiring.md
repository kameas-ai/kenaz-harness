---
work_package_id: "WP05"
title: "Tools RPC view + chassis wiring (newLLMStack, core.Start/Shutdown)"
dependencies:
  - "WP01"
  - "WP02"
  - "WP03"
  - "WP04"
planning_base_branch: "main"
merge_target_branch: "main"
branch_strategy: "Branch wp05-tools-rpc-and-chassis off main; merge back when WP05 acceptance gate passes."
subtasks:
  - "T024"
  - "T025"
  - "T026"
  - "T027"
  - "T028"
  - "T029"
  - "T030"
phase: "Phase 7+8 — RPC view + chassis wiring"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-26T16:35:00Z"
    agent: "system"
    action: "WP prompt authored manually after Plan agent could not write files."
---

# Work Package Prompt: WP05 — Tools RPC view + chassis wiring

## Goal

Land the rpc view that lets the frontend list/install/uninstall recipes, then wire the real `*stdio.Pool` into `newLLMStack` (replacing the fixture pool in production) and have `core.Start` spawn every persisted enabled recipe at boot. After this WP, toggling a recipe on from the frontend's existing key-prompt path actually spawns a process and the toolloop sees its tools.

## Spec / plan references

- Spec: §FR-021..FR-025, FR-030..FR-032, NFR-006.
- Plan: Phase 7 (RPC view) + Phase 8 (Wire into chassis).

## Prerequisites

WP01 + WP02 + WP03 + WP04 merged.

## Subtasks

- **T024 — `core/rpc/views/tools/api.go`** — `ToolsAPI` interface:
  ```go
  type ToolsAPI interface {
      ListRecipes(ctx context.Context) ([]RecipeListing, error)
      InstallRecipe(ctx context.Context, id string, env map[string]string) (RecipeStatus, error)
      UninstallRecipe(ctx context.Context, id string) error
      ForgetRecipeKey(ctx context.Context, id, envName string) error
      RecipeStatus(ctx context.Context, id string) (RecipeStatus, error)
  }

  type RecipeListing struct {
      Recipe       recipes.Recipe
      Enabled      bool
      Status       stdio.RecipeStatus  // zero value when not enabled
      KeysPresent  bool
  }
  ```

- **T025 — `core/rpc/views/tools/impl.go`** — concrete `*API`:
  ```go
  type API struct {
      catalog  *recipes.Catalog
      enabled  *recipes.EnabledRecipes
      pool     *stdio.Pool
      secrets  secrets.Backend
      dataDir  string
      mu       sync.Mutex
  }
  ```
  - `InstallRecipe`:
    1. Lookup recipe in catalog. Validate every required env key present + non-empty.
    2. For each env key, write via the existing keychain writer pattern (look at `core/rpc/api.go::keychainWriter` from the storage-consolidation work — reuse it). Locator: `recipes.KeychainLocator(id, envName)`.
    3. Add to `EnabledRecipes`; `Save`.
    4. Resolve env via `recipes.ResolveEnv`; build `mcp.ServerSpec` via `recipe.ToServerSpec(resolvedEnv)`.
    5. Call `pool.OpenOne(ctx, spec)` (new method on `*stdio.Pool` — single-server add to a running pool; mirrors the multi-server `Open` from WP01).
    6. Zero out the plaintext env values in the input map before return (`for k := range env { env[k] = "" }`).
    7. Emit audit `mcp.recipe.installed`.
    8. Return `RecipeStatus` snapshot.
  - `UninstallRecipe`: `pool.CloseOne(ctx, id)` (SIGTERM grace), remove from `EnabledRecipes`, `Save`. Emit `mcp.recipe.uninstalled`. Keychain entries persist.
  - `ForgetRecipeKey`: `secrets.Backend.Delete(...)` for that locator. Emit `mcp.recipe.key_forgotten`.
  - `RecipeStatus`: read-through to `pool.RecipeStatus(id)`; if not running, return `{Enabled:false, State:"stopped"}` from the enabled list + catalog.
  - `ListRecipes`: walk catalog × enabled list × pool status.

- **T026 — `*stdio.Pool` extensions** — add `OpenOne(ctx, spec) error` and `CloseOne(ctx, id) error` to the WP01 pool. These are struct-only (NOT on `core/mcp.Pool`) — they're for the rpc view to dynamically add/remove servers without re-Opening the whole pool. Tests live in this WP, not WP01: `core/mcp/stdio/pool_dynamic_test.go`.

- **T027 — Bindings** — `core/rpc/bindings.go` adds: `Tools_ListRecipes`, `Tools_InstallRecipe`, `Tools_UninstallRecipe`, `Tools_ForgetRecipeKey`, `Tools_RecipeStatus`. `core/rpc/api.go` adds `func (a *API) Tools() tools.ToolsAPI` accessor + `newToolsAPI(c *core.Core, pool *stdio.Pool, secretsBackend secrets.Backend)` constructor. Update `core/rpc/stubs.go` with the no-op stub for nil-core test paths.

- **T028 — `core/rpc/api.go newLLMStack` swap** —
  - Remove `fixture.New()`.
  - Construct `*stdio.Pool` with `stdio.NewPool(stdio.PoolOptions{ Sampler: NewLLMSamplingHandler(reg, activeProvider), Roots: stdio.DefaultRoots(c.DataDir(), nil), Broker: brokerAdapter, Logger: logging.L() })`.
  - The `mcpPoolAdapter` (already in api.go) wraps `core/mcp.Pool` for the toolloop — it accepts `*stdio.Pool` unchanged because `*stdio.Pool` implements `core/mcp.Pool`.
  - The fixture pool stays only in test files: `core/toolloop/loop_test.go`, `core/toolloop/hooks_test.go`, `core/rpc/views/llm/impl_toolloop_test.go`, `core/rpc/api_test.go`. Verify the build still uses fixture in those paths only.

- **T029 — `core/core.go Start/Shutdown`** —
  - `Start`: after `c.Storage()` opens, load `recipes.LoadEnabled(c.DataDir())`, walk entries, resolve env via `recipes.ResolveEnv`, build specs, call `pool.Open(ctx, specs)`. Failed spawns log at warn + degrade gracefully; chat surface still works without their tools. Wire `c.MCP = pool` so existing `Shutdown` (lines 174-176) closes it.
  - `Shutdown` already calls `c.MCP.Close(ctx)`. Verify no change needed.

- **T030 — Tests** —
  - `core/rpc/views/tools/impl_test.go`:
    - Stub `secrets.Backend` + a fake `mcp.Pool` (use a small interface + fake; don't spawn real subprocess).
    - Install with missing required env → error, nothing persisted.
    - Install + uninstall round-trip; ServerSpec passed to pool matches recipe.
    - ForgetRecipeKey deletes the locator.
    - RecipeStatus pass-through honors not-installed (returns `Enabled: false, State: "stopped"`).
  - `core/rpc/views/tools/no_plaintext_test.go` (NFR-006):
    - Install with `BRAVE_API_KEY=test_secret_marker_NFR006_xyz` (sentinel string).
    - Walk `<DataDir>` recursively (skip the in-tree fake-server testdata, skip the lockfile, etc.).
    - Assert no file contains the sentinel literal.
  - `core/rpc/api_mcp_e2e_test.go` (`-tags integration`): boot a `*core.Core` against a temp `DataDir`, install the in-tree fake recipe (a tiny test-only catalog override), run a tool call through the toolloop, assert response. This is the A1 walkthrough.

## Acceptance

- `go test -race -count=1 -short ./core/...` ≥ baseline + new tests.
- A1 from spec: install → search → assistant cites the result. Verified end-to-end in the integration test (using the fake recipe — Brave Search itself requires `npx` + a real key, gated under `-tags=npx_e2e`).
- A2: uninstall kills the subprocess (verified in pool_dynamic_test).
- A6: adding a new entry to `shipped.json` makes it appear in `ListRecipes` on next boot (verified by adding a second test-only entry in the test).
- A12: no goroutine leaks across Open/Close cycles in the e2e test.
- A13: NFR-006 plaintext scan passes.

## Constraints

- The fixture pool stays for unit tests of upstream consumers (toolloop). Don't delete `core/mcp/fixture/`.
- `OpenOne` / `CloseOne` are struct-only on `*stdio.Pool`. Don't promote them to `core/mcp.Pool` interface — that would force fixture pool to implement them too, and the spec marks single-server dynamic add/remove as out of scope for the abstract pool surface.
- Don't fabricate a new event-emitter package for audit. Reuse the existing one (find via grep — search for `event.Emitter` or `audit.Emit`). If no production emitter is wired, pass nil + TODO (matches the pattern from MCP-tool WP03).
- Keep plaintext env values out of `<DataDir>` — verified by NFR-006 scan.

## Branch strategy

Branch `wp05-tools-rpc-and-chassis` off `main`.
