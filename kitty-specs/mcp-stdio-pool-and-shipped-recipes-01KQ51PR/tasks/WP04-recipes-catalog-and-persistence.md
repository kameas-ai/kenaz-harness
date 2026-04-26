---
work_package_id: "WP04"
title: "Recipes catalog + enabled persistence + secrets resolve"
dependencies: []
planning_base_branch: "main"
merge_target_branch: "main"
branch_strategy: "Branch wp04-recipes-catalog off main; merge back when WP04 acceptance gate passes."
subtasks:
  - "T018"
  - "T019"
  - "T020"
  - "T021"
  - "T022"
  - "T023"
phase: "Phase 5+6 — Recipes catalog + persistence"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-26T16:35:00Z"
    agent: "system"
    action: "WP prompt authored manually after Plan agent could not write files."
---

# Work Package Prompt: WP04 — Recipes catalog + enabled persistence + secrets resolve

## Goal

Land the shipped-recipes catalog format, the embedded `shipped.json` with Brave Search as v1 entry, the `<DataDir>/mcp/recipes.enabled.json` persistence layer with atomic write, and the secrets-backed env resolver with the canonical keychain locator scheme. Independent of the stdio pool work — this is pure data + persistence.

## Spec / plan references

- Spec: §FR-018, FR-019, FR-020, NFR-006.
- Plan: Phase 5 (catalog) + Phase 6 (persistence + install/uninstall).
- Data-model: `Recipe`, `EnvKey`, `Capabilities`, `EnabledRecipes`.

## Prerequisites

None — independent of WP01-WP03. Can land in parallel with stdio-pool foundations.

## Subtasks

- **T018 — `core/mcp/recipes/recipes.go`** — `Recipe`, `EnvKey`, `Capabilities`, `SamplingPolicy`, `Catalog` per data-model.md. Sentinel errors `ErrRecipeNotFound`, `ErrInvalidRecipeID`. `Catalog` interface exposes `List() []Recipe` (catalog declaration order — stable), `Get(id) (Recipe, bool)`. Validation: `ID` matches `^[a-z][a-z0-9-]{0,63}$`; `Command[0]` non-empty.
  - `(*Recipe).ToServerSpec(env map[string]string) mcp.ServerSpec` — fills `Name=ID`, `Transport="stdio"`, `Command`, `Env`. Used by WP05 to build pool inputs.

- **T019 — `core/mcp/recipes/shipped.go`** — `//go:embed shipped.json` + `LoadShipped() (*Catalog, error)` parses the embedded JSON into `*Catalog`. Package-level singleton `var shipped = mustLoad()` so callers can `recipes.Shipped()` without repeated parsing. `mustLoad()` panics on parse failure (build-time data — bad JSON should fail the binary).

- **T020 — `core/mcp/recipes/shipped.json`** — v1 catalog with one entry. Use the data-model.md's exact shape:
  ```json
  {
    "version": 1,
    "recipes": [
      {
        "id": "brave-search",
        "display_name": "Brave Search",
        "description": "Web + local search via the Brave Search API. Free tier 2000 queries/month.",
        "category": "search",
        "command": ["npx", "-y", "@modelcontextprotocol/server-brave-search"],
        "env_keys": [
          {
            "name": "BRAVE_API_KEY",
            "display": "Brave Search API Key",
            "docs_url": "https://api.search.brave.com/app/keys",
            "required": true
          }
        ],
        "capabilities": { "tools": true, "resources": false, "prompts": false, "sampling": false },
        "docs_url": "https://github.com/modelcontextprotocol/servers/tree/main/src/brave-search",
        "init_timeout_ms": 5000,
        "ping_period_ms": 30000,
        "sampling_policy": { "allowed": false, "default": false }
      }
    ]
  }
  ```
  **Note for the implementer**: research flagged that the Brave package's exact tool names (`brave_web_search`, `brave_local_search`) and env-var name (`BRAVE_API_KEY`) need verbatim verification before merge. Run `npm view @modelcontextprotocol/server-brave-search` (or clone the repo) and confirm. Update the JSON if anything differs.

- **T021 — `core/mcp/recipes/enabled.go`** — `EnabledRecipes` per data-model.md. `EnabledRecipe` struct includes `ID, EnabledAt, SamplingEnabled, EnvAuditHash`.
  - `LoadEnabled(dataDir string) (*EnabledRecipes, error)` reads `<dataDir>/mcp/recipes.enabled.json`. Missing → empty `EnabledRecipes{}`. Corrupt (parse error) → log at warn + return empty per spec.md §9 edge case 7.
  - `(*EnabledRecipes).Save(dataDir string) error` — atomic write: write to `recipes.enabled.json.tmp`, fsync, `os.Rename`, fsync parent dir. Tested under concurrent Save.
  - `(*EnabledRecipes).Add(rec EnabledRecipe)` / `Remove(id string)` / `Get(id) (EnabledRecipe, bool)` / `List() []EnabledRecipe`.

- **T022 — `core/mcp/recipes/keychain.go`** — `KeychainLocator(recipeID, envName string) string` returns `"mcp/" + recipeID + "/" + envName`. `ResolveEnv(ctx context.Context, backend secrets.Backend, recipe Recipe) (map[string]string, error)`:
  - For each `EnvKey` in `recipe.EnvKeys`: build `secrets.CredentialReference{Kind: secrets.RefKeychain, Locator: KeychainLocator(recipe.ID, envKey.Name)}`.
  - Resolve via `backend`. If `envKey.Required` and resolution fails, return error.
  - Returns map ready for `exec.Cmd.Env`.
  - `EnvAuditHash(recipe Recipe) string` returns sha256 of the sorted locator strings (NOT values) — change-detection for the audit field.

- **T023 — Tests** —
  - `recipes_test.go`: parse shipped.json; Brave entry present with expected fields; ID validation regex; `ToServerSpec` round-trip including `Env` map population.
  - `enabled_test.go`: round-trip save/load with multiple entries; missing-file → empty; corrupt-file → log + empty; concurrent Save (10 goroutines) → consistent state, no truncation; atomic-write verified by injecting an `os.Rename` failure (mock or skip if not testable; document why).
  - `keychain_test.go`: locator format; ResolveEnv with a fake backend (multiple keys, missing required key → error, missing optional key → empty string in map); EnvAuditHash stable + changes when locators change.

## Acceptance

- `go test -race -count=1 -short ./core/mcp/recipes/...` passes.
- Catalog ships exactly one recipe (Brave Search) with verified field values from upstream.
- No plaintext keychain VALUES anywhere in `core/mcp/recipes/` — only locators. (The actual NFR-006 plaintext-on-disk scan lives in WP05 across `<DataDir>`.)
- `core/mcp/recipes/` is independent of `core/mcp/stdio/` and vice versa — no imports from one into the other except through `core/mcp.ServerSpec` (the existing public type).

## Constraints

- Independent of stdio-pool WPs — can land in parallel with WP01.
- Don't introduce a global registry singleton beyond the package-level `shipped` catalog.
- Atomic write must fsync the parent directory after rename (some filesystems lose the rename otherwise).
- Don't read or write the keychain in this WP — `ResolveEnv` takes a `secrets.Backend` interface; the writer side lives in WP05 (rpc view's InstallRecipe).
- Use the existing `core/secrets` package's types and patterns. Don't fabricate new credential kinds.

## Branch strategy

Branch `wp04-recipes-catalog` off `main`.
