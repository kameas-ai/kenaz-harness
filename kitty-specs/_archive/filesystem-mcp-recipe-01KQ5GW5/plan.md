# Implementation Plan: Filesystem MCP recipe

**Branch**: `filesystem-mcp-recipe-01KQ5GW5` (lane allocated at WP-implement time)
**Date**: 2026-04-26
**Spec**: `kitty-specs/filesystem-mcp-recipe-01KQ5GW5/spec.md`

## Summary

Ship `@modelcontextprotocol/server-filesystem` as the second one-click recipe in the Kaneaz Tools catalog. Adds a config-options surface to the Recipe schema (directory list + boolean toggles), a path-validation layer (canonicalize + deny-list), persisted config per enabled recipe, and a Tools-panel "Open workspace" button. Default sandbox is `<DataDir>/agent-workspace/`, created on first install. Read-only mode supported.

This mission rides on top of the mcp-stdio-pool mission — it adds **data + UI** on existing rails, with two small struct extensions to `Recipe` (`ArgsTemplate`, `ConfigOptions`) and one to `EnabledRecipe` (`Config`).

## Technical Context

- **Language/Version**: Go 1.22+; TypeScript 5.x.
- **Primary Dependencies**: stdlib `path/filepath` (`Abs`, `EvalSymlinks`); Wails runtime `BrowserOpenURL` for "Open workspace". No new third-party deps.
- **Storage**: persisted `EnabledRecipe.Config` via existing `enabled.go` round-trip; sandbox dir on filesystem; new `recipes.config.json` stash for last-used config per recipe id.
- **Testing**: Go `-race -count=1 -short`; vitest. Real subprocess spawning gated `-tags=npx_e2e`; default suite uses the in-tree fake server with custom flags simulating the filesystem server's surface.
- **Performance**: NFR-003 — install flow < 3 s on warm npm cache.
- **Constraints**: NFR-004 (no path traversal), NFR-005 (read-only mode genuinely disables writes).

## Charter Check

- DIRECTIVE_001 (no cyclic imports): `core/mcp/recipes/` adds `substitution.go`; `core/rpc/views/tools/` consumes; new `core/rpc/views/shell/` for `BrowserOpenURL` adapter. No cycles. Pass.
- C-001 (no third-party SDK in `core/`): stdlib only. Pass.
- Privacy CI #4 (no raw color literals): zero net-new color literals.
- Privacy CI on rpc payloads: audit events redact via the existing pipeline; paths are not secrets.

## Project Structure

```
core/mcp/recipes/
├── recipes.go                  # MODIFIED: ArgsTemplate, ConfigOption, ConfigKind* constants
├── shipped.json                # MODIFIED: filesystem entry
├── enabled.go                  # MODIFIED: Config map[string]any per entry
├── substitution.go             # NEW: ${DATA_DIR}, ${ALLOWED_DIRS} substitution
├── substitution_test.go        # NEW
├── recipes_test.go             # MODIFIED
└── enabled_test.go             # MODIFIED

core/rpc/views/tools/
├── api.go                      # MODIFIED: InstallRecipe(id, env, config)
├── impl.go                     # MODIFIED: validate + substitute + spawn
├── path_validation.go          # NEW: canonicalize + deny-list
├── path_validation_test.go     # NEW
└── impl_test.go                # MODIFIED: filesystem install + read-only

core/rpc/views/shell/           # NEW
├── api.go                      # ShellAPI: OpenInOSBrowser
└── impl.go                     # wraps wails/v2/pkg/runtime.BrowserOpenURL

core/rpc/api.go                 # MODIFIED: Shell() accessor + binding
core/rpc/bindings.go            # MODIFIED: Shell_OpenInOSBrowser

frontend/src/views/tools/
├── KaneazToolsPanel.vue        # MODIFIED: filesystem-row Open-workspace button + chips
├── RecipeKeyPromptModal.vue    # MODIFIED: render ConfigOptions
├── DirectoryPicker.vue         # NEW: chip-list editor with OS folder picker
└── __tests__/                  # MODIFIED + NEW

frontend/src/lib/types.ts       # MODIFIED: ConfigOption, RecipeConfig
frontend/src/lib/harnessClient.ts  # MODIFIED: installRecipe(id, env, config); shell.openInOSBrowser

docs/mcp-recipes.md             # MODIFIED: filesystem walkthrough
```

**Structure Decision**: existing harness layout (Go backend + Vue frontend). Adds one new rpc view (`shell`) for the OS-browser action and slim extensions to the existing `recipes` and `tools` packages.

## Phase 0 — Research summary

- **`@modelcontextprotocol/server-filesystem` CLI**: takes one or more directory paths as positional args (`npx -y @modelcontextprotocol/server-filesystem /path/one /path/two`). Read-only mode is supported via the `:ro` suffix on individual paths in newer builds; older builds have a `--read-only` global flag. Verify at WP-implement time. Cite: https://github.com/modelcontextprotocol/servers-archived/tree/main/src/filesystem.
- **Path canonicalization**: `filepath.Abs` + `filepath.EvalSymlinks` is sufficient on macOS/Linux; Windows needs `\\?\` prefix awareness. Stdlib only.
- **Wails OpenURL**: `github.com/wailsapp/wails/v2/pkg/runtime.BrowserOpenURL(ctx, url)` works for `file://` URLs on macOS / Linux / Windows.
- **Deny-list**: `/`, `/etc`, `/System`, `/Library`, `/private`, `/Applications`, the literal `$HOME` (allow children, not the dir itself). Add Linux equivalents (`/proc`, `/sys`, `/boot`).

## Phase 1 — Recipe schema extensions

**Targets**: `core/mcp/recipes/recipes.go`, `recipes_test.go`.

- `ConfigOption` struct + `ConfigKind*` constants (`directory_list`, `boolean`, `string`).
- `Recipe.ArgsTemplate []string` (additive; existing recipes have nil → no extra args).
- `Recipe.ConfigOptions []ConfigOption` (additive).
- `(*Recipe).ToServerSpec(env, config) ServerSpec` — replaces the WP04 single-arg version. Substitutes `${VAR}` in `Command + ArgsTemplate`. Backward-compat: nil config → no substitution, behaves like WP04.
- Tests cover schema parse, substitution, default fill-in.

**Dependencies**: none beyond the stdio-pool baseline.

## Phase 2 — Substitution + persisted config

**Targets**: `core/mcp/recipes/substitution.go`, `enabled.go`, tests.

- `Substitute(template []string, vars map[string]string) []string`. Tokens: `${DATA_DIR}`, `${ALLOWED_DIRS}` (space-separated). Unknown vars left as-is and a warn-logged event.
- `EnabledRecipe.Config map[string]any` field added; load/save round-trip-safe; missing field defaults to `{}`. JSON-stable ordering not required (it's a map; Go's `encoding/json` sort by key for maps gives deterministic output).
- Backward-compat for existing `enabled.json` files: a missing `config` field unmarshals to empty map.

**Dependencies**: Phase 1.

## Phase 3 — Path validation

**Targets**: `core/rpc/views/tools/path_validation.go` + test.

- `ValidateAllowedDir(path string) error` returns nil iff: absolute, exists, canonical (no `..` after `EvalSymlinks`), and not in deny-list.
- Deny-list constants per Phase 0 research.
- `EnsureWorkspace(dataDir string) (string, error)` — creates `<DataDir>/agent-workspace/` if missing; touches a `.kaneaz-workspace` marker file.

**Dependencies**: none.

## Phase 4 — `InstallRecipe` config flow

**Targets**: `core/rpc/views/tools/api.go`, `impl.go`, `impl_test.go`.

- Extend `InstallRecipe(id, env, config)` signature.
- For each `ConfigOption` in the recipe:
  - Required and missing → error.
  - `directory_list`: each path validated by `ValidateAllowedDir`.
  - `boolean`: type-check, default if missing.
- Persist config via `EnabledRecipes.Add` → `Save`.
- Substitute `${DATA_DIR}` → `<DataDir>`; `${ALLOWED_DIRS}` → space-joined `config["allowed_directories"]`.
- For filesystem recipe: if `config["read_only"] == true`, append the right CLI flag (TBD per Phase 0 research at implement time).
- Spawn via existing `pool.OpenOne`.
- Audit `mcp.recipe.installed` payload includes `config` (paths kept; secret-shaped fields redacted via the existing pipeline — although filesystem recipe has no env keys).

Also: `recipes.config.json` stash. On uninstall, write the just-removed config to `<DataDir>/mcp/recipes.config.json` keyed by recipe id. On install, if no `config` argument is provided AND a stashed config exists for the id, prompt the modal to "Use last-used config?" (frontend handling). Backend exposes `Tools_GetStashedConfig(id) → map[string]any` for the frontend to ask.

**Dependencies**: Phases 1, 2, 3.

## Phase 5 — Shell OpenInOSBrowser

**Targets**: `core/rpc/views/shell/{api,impl}.go`, `core/rpc/api.go`, `bindings.go`.

- `ShellAPI.OpenInOSBrowser(ctx, path string) error`: validate path exists, build `file://<abs>` URL, call `runtime.BrowserOpenURL(ctx, url)`. Wails runtime is already imported in main.go; the rpc surface needs the harness's stored Wails ctx.
- New binding `Shell_OpenInOSBrowser`.
- `core.API.Shell()` accessor + nil-safe stub for tests.

**Dependencies**: none beyond chassis surface.

## Phase 6 — Frontend

**Targets**: `KaneazToolsPanel.vue`, `RecipeKeyPromptModal.vue`, `DirectoryPicker.vue`, `lib/types.ts`, `lib/harnessClient.ts`.

- `lib/types.ts`: add `ConfigOption`, `RecipeConfig` types.
- `lib/harnessClient.ts`: extend `installRecipe(id, env, config)`; add `shell.openInOSBrowser(path)`.
- `RecipeKeyPromptModal.vue` extension to render `ConfigOptions`:
  - `directory_list`: shows `DirectoryPicker.vue` (chip list with "+" and remove); the `+` button calls `client.shell.openDirectoryDialog?` if available, else uses `<input type="file" webkitdirectory>` fallback. (Wails has `OpenDirectoryDialog` in v2 runtime — use it preferentially.)
  - `boolean`: existing checkbox styling.
  - `string`: text input.
  - Submit forwards `(id, env, config)` to `installRecipe`.
- `KaneazToolsPanel.vue`:
  - For the filesystem recipe row (recognized by `category === "filesystem"`), append a "Open workspace" button that calls `shell.openInOSBrowser(<DataDir>/agent-workspace)` — backend exposes the resolved path via the `RecipeStatus` extension or via a new `Tools_WorkspacePath(id)` binding. Simpler: reuse the `Tools_RecipeStatus(id)` payload and add an optional `WorkspacePath` field.
  - Allowed-directories chips visible when expanded; "Edit" reopens the modal.

**Dependencies**: Phase 4 + Phase 5.

## Phase 7 — Polish + docs

**Targets**: `docs/mcp-recipes.md`, e2e tests.

- Add a "Filesystem" section to `docs/mcp-recipes.md` covering: install walkthrough, default workspace location, adding extra roots, read-only mode, security notes (path-traversal protection, deny-list).
- E2E test (gated `-tags=integration`): install filesystem recipe with custom workspace, ask the model (via stub LLM) to call `write_file`, assert the file lands at the right disk path.
- Manual A1-A7 checklist in `docs/mcp-recipes.md` for `wails dev` validation.

**Dependencies**: Phases 1-6.

## Work-package breakdown (proposed)

- **WP01 — Recipe schema + substitution + path validation** (Phases 1, 2, 3). Pure backend additive change. Lands `ConfigOption`, `${VAR}` substitution, deny-list validator. No behavior change yet — existing recipes (Brave) untouched.
- **WP02 — InstallRecipe config flow + Shell view** (Phases 4, 5). Wires the config surface end-to-end backend; ships the `Shell_OpenInOSBrowser` binding. Adds the filesystem entry to `shipped.json`. After this WP, the recipe installs and runs from a CLI-only test, but the modal can't yet collect config.
- **WP03 — Frontend** (Phase 6). Modal renders `ConfigOptions`; "Open workspace" button works. After this WP, the user can install + use the filesystem recipe end-to-end via the UI.
- **WP04 — Polish + docs** (Phase 7). E2E test, walkthrough docs, A1-A7 manual checklist.

DAG: WP01 → WP02 → WP03 → WP04.

## Risk register

| Risk | Phase | Mitigation |
|---|---|---|
| Reference server CLI surface drift (`--read-only` may not exist in shipped npm) | 0, 4 | Verify at WP02 implement time via `npm view`; if no `--read-only`, fall back to `path:ro` per-path syntax or document the flag as pending upstream support. |
| Path canonicalization differs across OS | 3 | Use `filepath.Abs + EvalSymlinks` everywhere; Windows test gated `-tags=windows` if at all. |
| User points the recipe at `/` and bypasses deny-list via creative paths | 3 | Canonicalize THEN check the deny-list — `/etc/../` resolves to `/`. Test in `path_validation_test.go`. |
| Wails `OpenDirectoryDialog` unavailable in browser dev | 6 | Fall back to `<input type="file" webkitdirectory>`. Document the dev/prod difference. |
| Persisted config drifts schema across versions | 2 | `EnabledRecipe.Config map[string]any` is intentionally permissive; the validator rebuilds on load and rejects unknown options at install time. Round-trip-safe across versions because we never strip unknown fields. |
| Recipe stash on uninstall feels surprising | 4 | "Reset config" button in the modal is explicit; document in `docs/mcp-recipes.md`. |
| `sampling/createMessage` accidentally enabled on filesystem recipe | 1 | `sampling_policy.{allowed:false, default:false}` in `shipped.json`. Validator at install rejects sampling toggle on for recipes with `allowed:false`. |
| Workspace dir collisions with user data | 4 | The `.kaneaz-workspace` marker makes the dir self-identify; never delete on uninstall; document the path in `docs/mcp-recipes.md`. |

## Open questions

(Restated from spec.md §11 + new ones surfaced during planning.)

1. Reference server CLI flags (`--allowed-directory` vs positional args; `--read-only` vs `:ro` suffix). Verify at WP02 implement time.
2. Stash-config-on-uninstall: keep, with explicit "Reset config" affordance.
3. macOS sandboxing for signed/distributed builds — out of scope for this mission; flagged for the eventual app-distribution mission.
4. **NEW** — Should Brave Search and Filesystem share a "Cost-impact" badge in the modal? Filesystem is free; Brave has a free tier. Probably yes for any future paid recipes — track as UI follow-up.
