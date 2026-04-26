# Spec: Filesystem MCP recipe — let models read/write the agent workspace

**Mission ID**: `filesystem-mcp-recipe-01KQ5GW5`
**Status**: draft
**Owner**: alecfeeman
**Planning base**: `main`
**Merge target**: `main`

## 1. Why this mission

The mcp-stdio-pool mission lays the rails for shipping MCP-server recipes one-click; the Brave Search recipe is the v1 entry. Once the user wants the model to **generate files** — write a markdown doc, save a code snippet, scaffold a project — the model needs filesystem tools. Anthropic's `@modelcontextprotocol/server-filesystem` is the canonical answer: it exposes `read_file` / `write_file` / `list_directory` / `create_directory` / `move_file` / `search_files` over a sandboxed root directory.

This mission ships that recipe and surfaces the workspace path under user control.

## 2. Goals

- Add `filesystem` recipe to the shipped MCP catalog (`core/mcp/recipes/shipped.json`). One-click install from the Tools panel.
- Default sandbox: `<DataDir>/agent-workspace/` — a directory the harness creates on first install. Visible to the user via the OS file browser; nothing leaks outside it.
- Settable additional roots: in the Tools-panel install modal, let the user toggle on extra sandbox paths (e.g. `~/Documents/agent-projects`). Each is added as a discrete `--allowed-directory` flag to the server invocation.
- "Open workspace" button in the Tools panel that opens the sandbox in the OS file browser (`Wails OpenURL` with a `file://` URL).
- Sensible defaults for read-only mode for users who want read access without write — toggle in the install modal.

## 3. Non-goals

- Per-session sandbox roots. v1 ships harness-global recipes only (per the stdio-pool mission); per-session roots wait on the broader per-session-recipe scope mission.
- A custom file browser inside the harness UI. The OS file browser is the right surface.
- Versioning / git integration of the sandbox. Out of scope; the user can `git init` it manually.
- Symlink resolution outside the sandbox — `@modelcontextprotocol/server-filesystem` already enforces root containment; we trust it.
- Streaming reads of huge files. The reference server is fine for files up to a few MiB; very large reads are a model-prompt-engineering issue.

## 4. User stories

- **US1** As a user, I open Tools → toggle on Filesystem. The install modal asks: (a) "Read-only?" (default off), (b) "Allow these directories" with the default `<DataDir>/agent-workspace/` already filled. I click Install. The server spawns, the model now sees `read_file` etc.
- **US2** As a user with the recipe enabled, I ask the model to "write a 5-step plan to ./PLAN.md". The model uses `write_file` with path `./PLAN.md`, the file lands under `<DataDir>/agent-workspace/PLAN.md`. I click "Open workspace" and verify the file.
- **US3** As a user, I want to give the model my code repo. In the Tools panel, I click "Add allowed directory" and pick `~/Code/my-repo`. The recipe restarts with the new root. The model can now read my repo and propose edits via `write_file`.
- **US4** As a security-conscious user, I tick "Read-only" before installing. The model can list and read but `write_file` returns a permission error from the server.
- **US5** As a user, I uninstall the recipe. The server stops; the on-disk `agent-workspace/` directory and any files I've added stay (we don't auto-delete user data).

## 5. Functional requirements

### 5.1 Recipe catalog entry

- **FR-001** Add a recipe to `core/mcp/recipes/shipped.json`:
  ```json
  {
    "id": "filesystem",
    "display_name": "Filesystem",
    "description": "Read and write files in a sandboxed workspace. The model gets read_file, write_file, list_directory, create_directory, move_file, and search_files tools scoped to the directories you allow.",
    "category": "filesystem",
    "command": ["npx", "-y", "@modelcontextprotocol/server-filesystem"],
    "args_template": ["${ALLOWED_DIRS}"],
    "env_keys": [],
    "config_options": [
      {
        "name": "allowed_directories",
        "display": "Allowed directories",
        "kind": "directory_list",
        "default": ["${DATA_DIR}/agent-workspace"],
        "required": true,
        "description": "Directories the model is allowed to read and write. The default workspace is created automatically. Add additional paths to give the model access to e.g. a code repo."
      },
      {
        "name": "read_only",
        "display": "Read-only mode",
        "kind": "boolean",
        "default": false,
        "description": "When on, the model can list/read but not write. Disables write_file, create_directory, move_file."
      }
    ],
    "capabilities": { "tools": true, "resources": false, "prompts": false, "sampling": false },
    "docs_url": "https://github.com/modelcontextprotocol/servers-archived/tree/main/src/filesystem",
    "init_timeout_ms": 5000,
    "ping_period_ms": 30000,
    "sampling_policy": { "allowed": false, "default": false }
  }
  ```

- **FR-002** Extend `Recipe` (`core/mcp/recipes/recipes.go`) with **two** new fields beyond what WP04 of stdio-pool shipped:
  ```go
  ArgsTemplate []string         `json:"args_template,omitempty"` // appended to Command at install with ${VAR} substitution
  ConfigOptions []ConfigOption  `json:"config_options,omitempty"`
  ```
  ```go
  type ConfigOption struct {
      Name        string   `json:"name"`
      Display     string   `json:"display"`
      Kind        string   `json:"kind"`            // "directory_list" | "boolean" | "string"
      Default     any      `json:"default,omitempty"`
      Required    bool     `json:"required"`
      Description string   `json:"description"`
  }
  ```
  Substitution: `${DATA_DIR}` → `<DataDir>`; `${ALLOWED_DIRS}` → space-separated list of allowed directories from the install config; missing options default to their declared `Default`.

### 5.2 Install / uninstall flow

- **FR-003** `Tools_InstallRecipe(id, env, config) → RecipeStatus` — extend the WP05-of-stdio-pool binding to accept a `config map[string]any` matching the recipe's `ConfigOptions`.
- **FR-004** `core/rpc/views/tools/impl.go InstallRecipe` for the filesystem recipe:
  1. Validate every required `ConfigOption` is present and well-typed.
  2. For `kind=directory_list`: ensure each path is absolute, exists (or create it for the default workspace), and is not under a forbidden root (deny `/`, `/etc`, `/System`, the user's home directory itself unless explicitly listed; allow children of home).
  3. Persist config alongside the enabled recipe entry: `EnabledRecipe.Config map[string]any`. (Update `EnabledRecipes` shape from stdio-pool WP04.)
  4. Resolve final `argv`: substitute `${DATA_DIR}` and `${ALLOWED_DIRS}` into `Command + ArgsTemplate`. If `read_only` is true, append `--read-only` flag (verify the upstream server supports this; if not, pass `--allowed-directory` ony for read-only mounts using the `:ro` suffix or whatever the server accepts — verify before merging).
  5. Call existing pool `OpenOne(ctx, spec)`.
- **FR-005** Default workspace creation: `<DataDir>/agent-workspace/` is `MkdirAll`d on first install if missing. A `.kaneaz-workspace` marker file is written so the user can `find` for their workspace easily.

### 5.3 Tools-panel UI

- **FR-006** `KaneazToolsPanel.vue` rendering for filesystem recipe:
  - Below the standard toggle row: "Open workspace" button that calls `client.shell.openInOSBrowser(<DataDir>/agent-workspace)`. Uses Wails's `BrowserOpenURL` runtime call.
  - "Allowed directories" chip-list editable from the install modal. Adding/removing requires re-installing (server restart).
- **FR-007** `RecipeKeyPromptModal.vue` (existing) extended to render `ConfigOptions`:
  - `kind=directory_list`: chip list with "+" button → OS folder picker (`<input type="file" webkitdirectory>` or Wails `OpenDirectoryDialog`).
  - `kind=boolean`: checkbox.
  - `kind=string`: text input.
- **FR-008** `Shell_OpenInOSBrowser(path string)` Wails binding. Uses `runtime.BrowserOpenURL("file://" + path)`. Validate path exists.

### 5.4 Wire shape

- **FR-009** `EnabledRecipes` (`core/mcp/recipes/enabled.go`) gains `Config map[string]any` per entry. Round-trip-safe via JSON. Migration of existing enabled.json files: a missing `config` field defaults to `{}`.
- **FR-010** Audit events on install: `mcp.recipe.installed` payload includes the recipe id and a redacted snapshot of the config (paths preserved; would-be-secret fields, e.g. any future env keys, redacted).

## 6. Non-functional requirements

- **NFR-001** `go test -race -count=1 -short ./core/mcp/recipes/... ./core/rpc/views/tools/...` ≥ baseline + new tests.
- **NFR-002** Frontend tests + build clean.
- **NFR-003** Install flow on a warm npm cache: < 3 s end-to-end.
- **NFR-004** No path traversal: directories accepted by `InstallRecipe` are validated to be absolute, canonicalized (resolve symlinks, check no `..`), and pass the deny-list check.
- **NFR-005** Read-only mode genuinely disables writes — verified by a test that toggles read_only on, asks the server to `write_file`, asserts an error response.

## 7. Acceptance criteria

- **A1** US1 — install flow produces a running filesystem server; the toolloop's discovery shows the filesystem tools (`filesystem__read_file` etc.).
- **A2** US2 — model writes `./PLAN.md` and the file lands under `<DataDir>/agent-workspace/PLAN.md`.
- **A3** US3 — adding `~/Code/my-repo` as an allowed directory makes the model able to read it. Verified by a test that runs the recipe with two roots and asserts both appear in `list_directory("/")` (or the equivalent server-specific behavior).
- **A4** US4 — read-only mode rejects `write_file`.
- **A5** US5 — uninstall stops the server; workspace files persist on disk.
- **A6** Path-traversal attempt: install with `allowed_directories=["/"]` is rejected.
- **A7** Persisted config round-trip: enable → restart harness → recipe re-spawns with the same config.

## 8. Architecture

```
core/mcp/recipes/
├── recipes.go                 # MODIFIED: ArgsTemplate, ConfigOption, ConfigKind constants
├── shipped.json               # MODIFIED: filesystem recipe entry
├── shipped.go                 # unchanged
├── enabled.go                 # MODIFIED: Config map[string]any per entry
├── substitution.go            # NEW: ${VAR} substitution + path validation
├── substitution_test.go       # NEW
├── recipes_test.go            # MODIFIED: filesystem entry parse + ToServerSpec with config
└── enabled_test.go            # MODIFIED: round-trip with config

core/rpc/views/tools/
├── api.go                     # MODIFIED: InstallRecipe accepts config
├── impl.go                    # MODIFIED: validate + substitute + spawn
├── impl_test.go               # MODIFIED: filesystem install flow + read-only
└── path_validation.go         # NEW: deny-list canonicalizer
core/rpc/views/shell/          # NEW directory (or extend an existing view)
├── api.go                     # NEW: OpenInOSBrowser
└── impl.go                    # NEW: wraps Wails runtime.BrowserOpenURL

frontend/src/views/tools/
├── KaneazToolsPanel.vue       # MODIFIED: filesystem-row "Open workspace" button + chip list
├── RecipeKeyPromptModal.vue   # MODIFIED: render ConfigOptions (directory_list, boolean, string)
└── DirectoryPicker.vue        # NEW: chip-list editor with OS folder picker

docs/mcp-recipes.md            # MODIFIED: filesystem walkthrough section
```

## 9. Edge cases

1. User picks a directory that doesn't exist → install rejects with "directory does not exist; create it first or pick another path".
2. User picks a system directory (`/etc`, `/System`) → install rejects with the deny-list message.
3. `npx` cold-start hits the existing `firstByteTimeout` (30 s) from stdio-pool WP01.
4. User uninstalls then re-installs without changing config → config persists across uninstall (the enabled-list entry is removed, but a separate `recipes.config.json` stash retains the last-used config for that recipe id; a "reset config" button in the modal explicitly clears it).
5. Multiple harness instances writing to the same workspace simultaneously → out of scope; the harness lockfile already prevents two harnesses on the same DataDir.
6. The reference server's `--read-only` flag may not exist verbatim; verify before merging (research follow-up).

## 10. Out of scope (explicit)

- Per-session sandbox roots.
- Custom in-app file browser.
- Git integration of the sandbox.
- Symlink resolution outside the sandbox.
- Streaming reads of huge files.
- File watcher → notify the model of external changes (would be `notifications/resources/list_changed` from the server, which the harness already handles upstream — the recipe just needs `capabilities.resources=true` to surface; flagged as a follow-on).

## 11. Open questions

1. **Reference server CLI surface**: verify exact flags (`--allowed-directory`, possibly `--read-only`) by `npm view @modelcontextprotocol/server-filesystem` or cloning the repo at WP-implement time.
2. **Stash-config-on-uninstall**: keep or drop. Default: keep (US5 user expectation).
3. **macOS sandboxing**: when running outside Wails dev (signed app), `npx` may need explicit entitlements to spawn a child writing into the user's home directory. Track as a build/distribution concern outside this mission.

## 12. Out-of-band dependencies

- `@modelcontextprotocol/server-filesystem` (npm). Anthropic-archived but still functional. Same archive caveat as Brave Search — flagged for follow-up if a community fork supersedes.
- Node.js + `npx` on PATH (same as Brave Search).
