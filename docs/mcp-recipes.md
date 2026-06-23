# MCP recipes

This document covers the shipped MCP recipes catalog: what it is, how
to use it from the harness UI, how to add a new entry, and how to
debug the inevitable cold-start hiccup.

## What is a recipe?

A **recipe** is one entry in the harness's catalog of vetted MCP
servers (tool-providing subprocesses that speak the
[Model Context Protocol](https://modelcontextprotocol.io/)). Each
recipe declares:

- A canonical `id` (lowercase kebab, primary key for keychain locators
  and tool-routing).
- A user-facing display name + description.
- The exact `command` the harness execs to spawn the server (the
  default uses `npx -y @modelcontextprotocol/server-<name>`).
- The credential-bearing env vars the server needs (`env_keys`), each
  with a docs link to the provider's API-key issuance page.
- The MCP capabilities the recipe author claims (`tools`, `resources`,
  `prompts`, `sampling`).
- A sampling policy (`allowed`, `default`) — see "Capabilities" below.

Recipes live in `core/mcp/recipes/shipped.json` and are embedded into
the binary at build time via `core/mcp/recipes/shipped.go`. The
**enabled** subset persists to `<DataDir>/mcp/enabled.json`; secrets
land in the OS keychain (macOS Keychain, Linux Secret Service, Windows
Credential Vault) under locators of the form
`harness/<recipe-id>/<env-name>` (FR-020, NFR-006 — no plaintext on
disk).

## Brave Search walkthrough

The reference recipe shipped with v1 is **Brave Search**. Toggling it
on plumbs `https://api.search.brave.com/` into the running model's
tool-call surface so the assistant can search the open web.

1. **Open the Tools tab.** Click `02 — TOOLS` in the left rail. The
   "Kenaz tools" panel sits at the top of the canvas; the existing
   "Long-term memory" row is unchanged.
2. **Find the Brave Search row** in the "Connected MCP recipes" list
   below the memory row. The status pill on the right reads
   `stopped`; the toggle on the far right is off.
3. **Toggle it on.** The harness checks for keychain entries. On a
   fresh install no key is stored, so the **Connect tool** modal
   opens.
4. **Get a Brave API key.** Click the "Get a key →" link under the
   "Brave Search API Key" input. The link opens
   `https://api.search.brave.com/app/keys` in your default browser
   (Wails desktop hands the URL off to the OS browser; in `wails dev`
   the same anchor `target=_blank` works). Sign in with Google /
   GitHub / email; the free tier is 2000 queries/month.
5. **Copy the key, paste it into the modal, click Connect.** The
   modal calls `Tools_InstallRecipe("brave-search", {BRAVE_API_KEY: …})`.
   The Go side writes the key to the OS keychain, zeroes the
   in-memory plaintext, and the supervisor spawns the server via
   `npx -y @modelcontextprotocol/server-brave-search`.
6. **Watch the status pill.** First-run `npx` cold-fetch takes 5–15 s;
   after 4 s a "warming…" inline note appears next to the pill. When
   the handshake completes the pill flips to `running`.
7. **Use it from chat.** Start a new session against any provider that
   supports tool-use (Claude, GPT-4 family, Gemini Pro). Ask
   "what's the weather in Reykjavík right now?" — the assistant
   should call the Brave Search tool and answer with current data.

## Filesystem (read/write your local files via MCP)

### What it does
Gives the model `read_file`, `write_file`, `list_directory`,
`create_directory`, `move_file`, and `search_files` tools scoped to
directories you explicitly allow. Files live on your real disk —
**these are NOT the same as artifacts** (which are model outputs
captured inside the harness; see `docs/artifacts.md`).

### Default workspace
On first install the harness creates `<DataDir>/agent-workspace/` and
includes it as the default allowed directory. Open it from the Tools
panel via "Open workspace" or in your OS file browser at
`~/.harness/agent-workspace`.

### Adding more directories
In the Tools panel, click "Edit configuration" on the Filesystem row.
Add directories with the "+ Add directory" button. Each directory
must:
- Exist on disk before install.
- Be inside your home directory (children of `$HOME` are allowed; the
  home directory root itself is not).
- Not be a system path: `/`, `/etc`, `/System`, `/Library`, `/private`,
  `/usr/bin`, `/sbin`, etc. are denied. The full deny list lives in
  `core/rpc/views/tools/path_validation.go`.

If the harness rejects a path, the modal banner explains why. Common
errors:
- `path is not absolute` — start with `/` or `~/`.
- `path does not exist` — create the directory first.
- `path is in deny-list` — pick a child instead.

### Install walkthrough (manual A1)
1. Open the harness; navigate to Tools.
2. Toggle Filesystem on. The modal opens with the default workspace
   pre-filled.
3. Optionally add more directories.
4. Click Install. The status pill cycles `starting → running` (about
   1.5 s on a warm npm cache; up to 15 s on first-ever install while
   `npx` fetches the package).
5. Open a new chat session. Ask the model "list the files in the
   workspace and create a hello.txt file with a greeting". The model
   should call `list_directory` and `write_file`; check
   `<DataDir>/agent-workspace/hello.txt` for the result.

### Read-only mode

**Status: pending upstream support.** The canonical
`@modelcontextprotocol/server-filesystem` package does not (as of WP04
verification, 2026-04-26) expose a `--read-only` CLI flag, a `:ro`
suffix syntax for paths, or an environment variable that disables
write tools. The README documents read-only access only via Docker
`--mount type=bind,...,ro` (which the harness's `npx` install path
does not use) or via the MCP **Roots** protocol at runtime (which the
harness does not yet drive on the host side).

For now the recipe ships **read/write only**. Two paths to read-only
exist if you need it:

- **Hand-edit the persisted config** at `<DataDir>/mcp/enabled.json`
  to add a CLI flag once upstream ships one. The recipe argv is
  rebuilt from that file on every harness restart, so your override
  survives across launches but does not survive an Uninstall + re-
  install through the modal.
- **Constrain the directory at the OS layer** — point the recipe at a
  path mounted read-only by your OS / filesystem (e.g. an APFS
  read-only snapshot, a `mount_nullfs -o ro`-style overlay, or a
  Docker volume mounted `:ro`). The MCP server's `write_file` will
  surface the OS-level `EROFS` as an error to the model.

When upstream lands a first-class read-only flag, this recipe will
gain a `read_only` ConfigOption and the modal will surface a checkbox.
Tracking issue: see "WP04 npm verification outcome" in
`kitty-specs/filesystem-mcp-recipe-01KQ5GW5/`.

### Uninstall
Toggle off in the Tools panel. The npx subprocess is killed; your
workspace files remain on disk. Run `Forget key` to also remove any
stored config.

### Troubleshooting
- **"npx: command not found"**: install Node.js from https://nodejs.org/.
- **"Server failed to start"**: check `~/.kenaz/harness.log` for
  `mcp.filesystem.message` lines from the server itself.
- **"Tool blocked"**: the tool may have failed our permission gate.
  Check Settings → Tool permissions (if surfaced) or
  `<DataDir>/mcp_servers.json`.

### Manual verification checklist (post-merge)

- [ ] **A1**: Install with default config → server runs.
- [ ] **A2**: Ask model to write a file in workspace → file appears at
      `<DataDir>/agent-workspace/<name>`.
- [ ] **A3**: Add a second allowed directory → model can read both.
- [ ] **A4**: (read-only — see "Status: pending upstream support"
      above; manual verification deferred until upstream ships a flag)
- [ ] **A5**: Uninstall → npx subprocess gone (`ps -ef | grep server-filesystem`).
      Workspace files persist.
- [ ] **A6**: Try install with `allowed_directories=["/etc"]` →
      modal shows path-validation error.
- [ ] **A7**: Restart harness → recipe re-spawns with persisted config
      (verified by check that workspace dir wasn't manually re-listed).

## Adding a new recipe

The catalog is data-driven: dropping a JSON entry into
`core/mcp/recipes/shipped.json` registers a new recipe with no Go
code change required.

```json
{
  "id": "filesystem",
  "display_name": "Filesystem",
  "description": "Read/write access to a local directory tree.",
  "category": "filesystem",
  "command": ["npx", "-y", "@modelcontextprotocol/server-filesystem", "/Users/me/projects"],
  "env_keys": [],
  "capabilities": { "tools": true, "resources": true, "prompts": false, "sampling": false },
  "docs_url": "https://github.com/modelcontextprotocol/servers/tree/main/src/filesystem",
  "init_timeout_ms": 5000,
  "ping_period_ms": 30000,
  "sampling_policy": { "allowed": false, "default": false }
}
```

Schema: see `core/mcp/recipes/recipes.go`. The build-time
`LoadShipped` validates every entry against `Recipe.Validate` — an
invalid `id`, empty `command`, or empty env-key `name` fails the
binary at init time, so a typo in `shipped.json` won't silently ship.

`category` drives the icon in the Tools panel. Known values:

| Category     | Icon (Lucide) | Notes                              |
| ------------ | ------------- | ---------------------------------- |
| `search`     | Search        | Web search, vector search          |
| `filesystem` | Folder        | Local-disk read/write              |
| `memory`     | Brain         | Long-term knowledge stores         |
| `fetch`      | Globe         | HTTP fetchers, scrapers            |
| `other`      | Wrench        | Default fallback                   |

## Capabilities

Each recipe declares which MCP capabilities its server advertises.
The harness still issues the live `tools/list`, `resources/list`,
`prompts/list` calls against the **negotiated** capability set during
the initialize handshake; the JSON declaration drives modal copy and
pre-flight expectations only.

- `tools`: the server exposes callable tools (most recipes set this).
- `resources`: the server exposes addressable resources the model can
  read on demand.
- `prompts`: the server exposes prompt templates.
- `sampling`: the server can call **back into the harness's active
  LLM** during tool execution. **Cost-amplification warning**: when
  `sampling_policy.allowed` is true and the per-server toggle is on
  (FR-015), an MCP tool-call can fan out into N more LLM completions
  on the user's active provider. Don't enable a sampling-allowed
  recipe against a metered provider you can't afford.

## Manual verification checklist (A14)

CI cannot exercise the full toggle-on-spawn-handshake-call path
because spawning real npx subprocesses against real provider APIs
is non-hermetic. The merger walks this checklist post-merge in
`wails dev` to declare the WP done:

- [ ] `wails dev` boots the harness; Tools tab loads without console
      errors.
- [ ] Brave Search row renders with category icon (Search), state
      pill `stopped`, toggle off.
- [ ] Toggling on with no stored key opens the **Connect tool** modal.
- [ ] "Get a key →" link opens the Brave API keys page in the OS
      browser (NOT inside the Wails window — `target=_blank` +
      Wails BrowserOpenURL fall-through).
- [ ] Cancel button closes the modal without spawning anything; toggle
      reverts to off.
- [ ] Pasting an obviously-invalid key + Connect surfaces a banner
      with the rpc error message; modal stays open.
- [ ] Pasting a valid key + Connect closes the modal; status pill goes
      `starting` → (after 4 s) `starting` + "warming…" → `running`.
- [ ] "Show details" expands the per-row detail panel: protocol
      version, server name + version, tool/resource/prompt counts,
      restart count.
- [ ] In a session against Claude (or any tool-capable provider), the
      prompt "search the web for the current Bitcoin price" triggers
      a Brave Search tool-call and returns a current-price answer.
- [ ] "Forget BRAVE_API_KEY" in the recipes row removes the keychain
      entry; toggling off + on re-prompts for the key.
- [ ] Toggling off stops the subprocess (verify via `ps` — no
      `npx server-brave-search` child remains).

## Troubleshooting

- **`npx` cold-start latency.** First-run downloads pull
  `@modelcontextprotocol/server-<name>` from npm + its dependency
  graph; cold-fetch on a slow connection has been observed to take up
  to 30 s. The "warming…" hint surfaces at 4 s; if the row stays
  `starting` past 60 s the supervisor escalates to `failed` and the
  pill turns red. Re-toggling typically succeeds the second time
  (cached `npx` install).
- **Key rotation.** Hover the env-key row in the panel and click
  "Forget <KeyName>". The keychain entry is purged; the next toggle-
  on re-opens the modal so you can paste the rotated key.
- **Logs.** Every recipe-spawned server writes structured JSON lines
  to `~/.kenaz/harness.log` under the topic
  `mcp.<recipe-id>.message` (incoming + outgoing JSON-RPC frames,
  redacted). Use `jq 'select(.topic | startswith("mcp.brave-search"))'`
  to scope to one recipe. Subprocess stderr is captured into a 4 KiB
  ring-buffer surfaced via the per-row detail panel.
- **Stuck `restarting`.** The supervisor caps at 5 restarts in 60 s;
  past that the row goes `failed` with the last error in the detail
  panel. A failure with "exit status 1" most often means the API key
  is wrong or rate-limited.
- **Wails desktop vs. browser dev parity.** The "Get a key →" link
  uses a vanilla `<a target="_blank" rel="noopener">`. In `wails dev`
  this opens a new browser tab; in the Wails desktop binary it
  defers to the `runtime.BrowserOpenURL` fallback so the link opens
  in the user's default OS browser. Both paths verified manually.
