# Spec: Real stdio MCP pool + shipped server recipes (full-featured)

**Mission ID**: `mcp-stdio-pool-and-shipped-recipes-01KQ51PR`
**Mission slug**: `mcp-stdio-pool-and-shipped-recipes`
**Status**: draft
**Owner**: alecfeeman
**Planning base**: `main`
**Merge target**: `main`

> **Scope directive**: this mission ships **real, full-featured** MCP server
> and tool infrastructure. No stubs, no fixture-pool hand-waves, no
> "lands in a downstream mission" placeholders for the hot path. The fixture
> pool stays only to support unit tests of upstream packages (toolloop,
> permissions). Production code paths use the real stdio pool end to end.

## 1. Why this mission

The harness ships a `Pool` interface (`core/mcp/pool.go`) and an in-memory
fixture (`core/mcp/fixture/`) that returns synthetic results. That's enough
for the toolloop integration tests, but it cannot run a real MCP server, so
models cannot actually call out to anything useful. The MCP-tool-execution
mission (WP01–WP05) covers permissions, hooks, audit, concurrency, and the
confirm-each modal; **none of those WPs implement a real subprocess pool**.
Without this layer, shipping a "Kaneaz Tools" hub with toggleable real tools
is impossible.

This mission closes that gap with a production-grade stdio MCP pool and ships
the first end-to-end real tool: a web search backed by Anthropic's official
`@modelcontextprotocol/server-brave-search` reference server. It also lays
down the catalog format and Tools-hub UI so adding the next recipe (Tavily,
Exa, Filesystem, Fetch, Memory, …) is a data change, not a code change.

## 2. Goals

- **Real stdio MCP pool** that spawns child processes per the MCP protocol
  spec and proxies the full request set the toolloop and the rest of the
  harness need: `initialize`, `tools/list`, `tools/call`, plus standard
  notifications (`notifications/initialized`, `notifications/cancelled`,
  `notifications/progress`, `notifications/tools/list_changed`).
- **Resources & prompts surfaces** — first-class support for the optional MCP
  capabilities (`resources/list`, `resources/read`, `resources/subscribe`,
  `prompts/list`, `prompts/get`). Models that ask for a resource get the
  resource; tooling that exposes prompts gets surfaced. No "we'll add it
  later" gating — these are part of the protocol and the harness should not
  blacklist them.
- **Sampling pass-through** — when a server requests `sampling/createMessage`
  (server-initiated LLM completions), the harness forwards to the active LLM
  registry and returns the completion. Gated by an explicit user toggle per
  server because it grants the server access to the user's models.
- **Roots negotiation** — the harness exposes its data dir + the active
  project root as roots when a server requests them.
- **Logging surface** — server `notifications/message` log entries land in
  `~/.kenaz/harness.log` with `mcp.<server>.<level>` slog tags.
- **Shipped recipes catalog** — Brave Search at minimum. Catalog is structured
  so adding Tavily / Exa / Fetch / Filesystem / Memory drops in as a data
  entry plus optional UI metadata.
- **Tools hub UI** — Kaneaz Tools panel renders the catalog, lets the user
  toggle each recipe on/off, prompts for required env (e.g.,
  `BRAVE_API_KEY`) at install time. Keys go through the existing OS-keychain
  backend and are surfaced to the spawned server's environment **only** at
  start time — never written to disk in plaintext.
- **End-to-end wired into the toolloop**: when a recipe is enabled, its tools
  appear in the toolloop's `Tools()` aggregation alongside any others; the
  model can `tool_use` against them; results flow back through the existing
  pump.
- **Auto-restart with backoff** for crashed servers (exponential, capped at
  3 retries inside a 5-minute window). User-visible status pill reflects
  running / restarting / failed states.
- **Health pings** — periodic `ping` requests (default 30 s) keep liveness
  visible; servers that fail two consecutive pings are restarted.
- **Cancellation discipline** — every in-flight call cancels cleanly when its
  caller's `ctx` is canceled; the server is told via `notifications/cancelled`
  and the host returns `ctx.Err()`.

## 3. Non-goals

- HTTP/SSE transport. v1 is stdio-only. The pool's internals abstract the
  transport so HTTP can drop in later, but the implementation ships only
  stdio.
- Server discovery from a wider community marketplace. v1 ships a hand-curated
  catalog of recipes. Arbitrary servers stay configurable via the existing
  `ServerSpec.Command` path; the marketplace UI is a follow-on.
- Per-tool permission gating. That belongs to the MCP-tool-execution
  mission's WP02 and is upstream of this mission.
- Frontend redesign of `ToolsView.vue` beyond the new Kaneaz Tools section.
- Signing / supply-chain verification of recipes.
- Auto-update of pinned npm versions.

## 4. User stories

- **US1** As a user opening Tools, I see a "Kaneaz Tools" panel listing the
  shipped recipes with on/off toggles and a live status pill per recipe
  (running / starting / restarting / failed / stopped).
- **US2** As a user toggling Brave Search on, I am prompted for my Brave API
  key. The key persists in the OS keychain. The server is spawned in the
  background; the toggle reflects the live status, including starting →
  running and any failure with a clear error message.
- **US3** As a user with Brave Search enabled, when I ask the model to search
  the web, the model issues a `tool_use` against `kaneaz.brave_web_search`,
  the server returns hits, the model continues, and the resolved-context
  panel shows the tool exchange.
- **US4** As a user toggling Brave Search off, the npx child is killed
  cleanly (SIGTERM with grace period, then SIGKILL) and the tool disappears
  from the toolloop's aggregation within ~250 ms.
- **US5** As a developer extending the harness, I can add a new shipped
  recipe by dropping a JSON entry into the catalog with command, env keys,
  display metadata, and capabilities — no Go code change in the pool itself.
- **US6** As a user whose Brave server crashes, the harness auto-restarts it
  up to 3 times with exponential backoff inside a 5-minute window; the status
  pill shows the restart cycle. After 3 failures, status is "failed" and a
  manual toggle off/on resets the counter.
- **US7** As a user running an MCP server that exposes resources (e.g., a
  filesystem server), the resources surface in a future Resources panel.
  v1 ships the data path; the panel is a follow-on UI mission. The harness
  does not block resources at the protocol level.
- **US8** As an operator inspecting `~/.kenaz/harness.log`, I see structured
  events for every spawn, initialize, tools/list, tools/call, restart, and
  shutdown, with `server`, `tool`, `request_id`, `duration_ms`, and `level`
  fields.

## 5. Functional requirements

### 5.1 Pool & lifecycle

- **FR-001** Real stdio pool implementation in `core/mcp/stdio/`. Each
  `ServerSpec` with `Transport: "stdio"` (and an empty/missing transport
  defaults to stdio) spawns a child via `exec.Cmd`, wires stdin/stdout to
  the JSON-RPC framer, and exposes its tools through `Pool.Tools()`.
- **FR-002** JSON-RPC framing per the MCP spec: newline-delimited JSON
  messages on stdin/stdout (Content-Length framing for HTTP later, not now).
  Implement request/response/notification routing with per-server response
  channels keyed by request id.
- **FR-003** Lifecycle on `Open(ctx, specs)`: spawn each server concurrently,
  send `initialize` with the harness's `clientInfo`, capabilities, and
  protocol version. `initialize` deadline default 5 s, configurable per
  recipe. On `initialize` success, send `notifications/initialized`. On
  failure, the server is marked failed but other servers continue — one bad
  recipe does not poison the pool.
- **FR-004** `Close(ctx)`: send SIGTERM to every running server, wait up to
  2 s for graceful exit, then SIGKILL. Wait for the wait-group of routers
  before returning so there are no leaked goroutines.
- **FR-005** Per-call concurrency: `Call(ctx, server, tool, args)` is safe
  for parallel invocation. Each call gets a unique request id and routes the
  matching response back to the caller via a buffered channel registered
  before the request goes out.
- **FR-006** Cancellation: when the caller's `ctx` is canceled mid-call, send
  `notifications/cancelled` to the server and return `ctx.Err()` to the
  caller. The server's eventual response (if any) is dropped without
  blocking.
- **FR-007** Auto-restart with exponential backoff: 1 s, 2 s, 4 s; max 3
  attempts inside any 5-minute window. After 3, the server is marked
  `failed` until the user toggles off/on (which resets the counter).
- **FR-008** Health pings: every 30 s by default, send `ping`. Two consecutive
  failures (5 s timeout each) trip a restart. Frequency is per-recipe-tunable.
- **FR-009** Stdout/stderr handling: stdout is the JSON-RPC channel.
  Non-JSON lines on stdout are logged at warn and skipped (some servers emit
  banner output). Stderr is captured into a ring buffer (last 64 KiB) and
  surfaced via `RecipeStatus` so the user can see startup errors.
- **FR-010** EOF detection: closing stdin or stdout EOF is treated as a
  crash, triggers restart per FR-007.

### 5.2 Protocol coverage

- **FR-011** `initialize` + `notifications/initialized` (mandatory).
- **FR-012** `tools/list` and `tools/call` (mandatory). Stream `tools/list`
  responses on `notifications/tools/list_changed` so toggling at the server
  side propagates to the harness without restart.
- **FR-013** `resources/list`, `resources/read`, `resources/subscribe`,
  `notifications/resources/list_changed`, `notifications/resources/updated`.
- **FR-014** `prompts/list`, `prompts/get`,
  `notifications/prompts/list_changed`.
- **FR-015** Server-initiated requests: `roots/list` (harness responds with
  data dir + active project root), `sampling/createMessage` (gated by
  per-server toggle, default off; when on, harness forwards to LLM registry
  using the active provider and returns the completion).
- **FR-016** Logging notifications: `notifications/message` from server →
  `slog.Info/Warn/Error` to `~/.kenaz/harness.log` with key `mcp.server` =
  recipe id and `mcp.level` from the message.
- **FR-017** Progress notifications: `notifications/progress` propagates as
  events on the existing chassis stream so a long-running tool call can show
  progress in the chat UI.

### 5.3 Catalog & recipes

- **FR-018** Recipe shape:
  ```go
  type Recipe struct {
      ID          string         // "brave-search"
      DisplayName string         // "Brave Search"
      Description string         // user-facing copy
      Category    string         // "search" | "filesystem" | "memory" | …
      Command     []string       // ["npx","-y","@modelcontextprotocol/server-brave-search"]
      EnvKeys     []EnvKey       // [{Name:"BRAVE_API_KEY", Display:"Brave Search API Key", DocsURL:"https://api.search.brave.com/app/keys"}]
      Capabilities Capabilities  // {Tools:true, Resources:false, Prompts:false, Sampling:false}
      DocsURL     string
      InitTimeoutMs int          // optional override; default 5000
      PingPeriodMs  int          // optional override; default 30000
  }
  ```
- **FR-019** Catalog at `core/mcp/recipes/`:
  - `recipes.go` — `Recipe`, `EnvKey`, `Capabilities` structs, registry
    surface.
  - `shipped.json` — embedded via `embed.FS`. v1 ships a single entry:
    Brave Search.
  - Shipped JSON is the authoritative source; Go layer just parses it.
- **FR-020** First shipped entry — Brave Search:
  ```json
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
    "docs_url": "https://github.com/modelcontextprotocol/servers/tree/main/src/brave-search"
  }
  ```

### 5.4 Install / uninstall flow

- **FR-021** `Tools_InstallRecipe(id, env map[string]string) (RecipeStatus, error)`:
  1. Validate required env keys are present and non-empty.
  2. Persist each env value via `secrets.Backend` keyed as
     `mcp/<recipe-id>/<env-name>`.
  3. Add the spec to the persisted "enabled recipes" list (new file
     `<DataDir>/mcp/recipes.enabled.json`, fsync on write).
  4. Re-invoke the active pool's `Open` with the updated spec list — the
     server spawns and `initialize` runs.
  5. Emit a `mcp.recipe.installed` audit event.
- **FR-022** `Tools_UninstallRecipe(id)`: remove the spec from
  `recipes.enabled.json`, send SIGTERM (FR-004 grace), update the pool, emit
  `mcp.recipe.uninstalled`. Keychain entry persists by default.
- **FR-023** `Tools_ForgetRecipeKey(id, envName)`: delete the keychain entry
  for that env. Emits `mcp.recipe.key_forgotten`. Server stays running with
  the env it captured at start (next restart will fail until re-keyed).
- **FR-024** `Tools_ListRecipes() []RecipeListing`: returns the full catalog
  + per-recipe `enabled bool`, `state string`, `lastError string`,
  `restartAttempts int`, `keysPresent bool`.
- **FR-025** `Tools_RecipeStatus(id) RecipeStatus`: live status snapshot
  including stderr ring-buffer tail (last 4 KiB) for debugging.

### 5.5 Frontend

- **FR-026** New `frontend/src/views/tools/KaneazToolsPanel.vue`:
  catalog list, per-row toggle, status pill, "Forget key" affordance, key
  prompt modal.
- **FR-027** ToolsView.vue gets a "Kaneaz Tools" section above any existing
  per-session tool toggles. The section renders KaneazToolsPanel.
- **FR-028** Status polling: 1 Hz `Tools_RecipeStatus` for any recipe in a
  non-terminal state (starting, running, restarting); idle when stopped or
  failed, on-toggle refresh otherwise.
- **FR-029** Recipe icons: each recipe declares a `category`. The frontend
  maps category → Lucide icon (e.g., `Search` for search).

### 5.6 Wiring

- **FR-030** `core/rpc/api.go newLLMStack` swaps `fixture.New()` for a
  `stdio.NewPool()` instance built with the persisted enabled recipes from
  `<DataDir>/mcp/recipes.enabled.json`. The fixture pool stays in the
  test-only path (used by toolloop unit tests).
- **FR-031** Boot sequence: `core.New` reads the enabled-recipes file (if
  any), `core.Start` spawns them concurrently via `Pool.Open`. Failed spawns
  log + degrade gracefully — the chat surface still works without their
  tools.
- **FR-032** `core.Shutdown` calls `Pool.Close` so child processes do not
  outlive the harness. Tested via a process-tree assertion.

## 6. Non-functional requirements

- **NFR-001** `go test -race -count=1 -short ./core/mcp/... ./core/rpc/views/tools/...`
  passes. New tests cover spawn, initialize, tools/list, tools/call (single
  + concurrent), cancellation, restart-on-crash, ping-failure-restart, EOF
  handling, malformed JSON skip, and recipes registry round-trip.
- **NFR-002** No goroutine leaks across `Open` / `Close` cycles. Test asserts
  goroutine count returns to baseline within 1 s of `Close`.
- **NFR-003** Spawn → initialize → first `tools/list` round-trip < 3 s on
  warm npm cache.
- **NFR-004** Cold-spawn UX: when first-run takes > 4 s (npm fetching the
  package), surface "warming…" indicator. Determinate progress not required.
- **NFR-005** Frontend tests + build green.
- **NFR-006** No plaintext keychain values on disk anywhere. Verify via a
  test that scans `<DataDir>` for any string matching the configured value.

## 7. Architecture

```
core/mcp/
├── pool.go               # existing: Pool interface + ServerSpec + Tool
├── fixture/              # existing: in-memory pool — kept for unit tests
├── stdio/                # NEW
│   ├── pool.go           # StdioPool implements core/mcp.Pool
│   ├── server.go         # one spawned server: cmd, framer, response router, restarter
│   ├── framer.go         # newline-delimited JSON-RPC reader/writer
│   ├── protocol.go       # MCP message shapes (initialize, tools/*, resources/*, prompts/*, notifications/*, sampling/createMessage, roots/list)
│   ├── sampler.go        # sampling/createMessage server→harness adapter onto core/llm
│   ├── roots.go          # roots/list adapter (data dir, active project root)
│   ├── log.go            # notifications/message → slog adapter
│   └── *_test.go
└── recipes/              # NEW
    ├── recipes.go        # Recipe / EnvKey / Capabilities, registry
    ├── shipped.go        # //go:embed shipped.json
    ├── shipped.json      # v1 catalog
    └── recipes_test.go

core/rpc/views/tools/     # MODIFIED
├── api.go                # add ListRecipes / InstallRecipe / UninstallRecipe / ForgetRecipeKey / RecipeStatus
├── impl.go               # wire registry + StdioPool + secrets.Backend
└── impl_test.go

core/rpc/api.go           # MODIFIED: newLLMStack uses StdioPool, not fixture
core/core.go              # MODIFIED: Start spawns enabled recipes, Shutdown closes pool

frontend/src/views/tools/
├── ToolsView.vue                  # MODIFIED: add Kaneaz Tools section
├── KaneazToolsPanel.vue           # NEW
├── RecipeKeyPromptModal.vue       # NEW
└── __tests__/
    ├── KaneazToolsPanel.test.ts
    └── RecipeKeyPromptModal.test.ts
```

## 8. Acceptance criteria

- **A1** Toggling Brave Search on, supplying a valid API key, then asking the
  model to search the web results in a real Brave Search response in the
  resolved-context panel; the assistant's reply cites a hit.
- **A2** Toggling off kills the npx subprocess (test verifies absence via
  `os.FindProcess` + `Signal(0)` returning `os.ErrProcessDone` within 2.5 s).
- **A3** Submitting an invalid Brave API key → server fails `initialize` →
  toggle returns to off; the modal shows the error from stderr ring buffer.
- **A4** Two concurrent `Call`s against the same running server return
  correct responses (response router by id).
- **A5** Cancelling the host context mid-call sends `notifications/cancelled`
  and the host returns context-cancelled.
- **A6** Adding a new entry to `shipped.json` makes it appear in the UI on
  next launch with no other code change.
- **A7** Killing the npx process externally (test does `cmd.Process.Kill()`)
  triggers auto-restart; status pill cycles starting → running.
- **A8** Two consecutive failed pings trigger a restart.
- **A9** Server-initiated `sampling/createMessage` (gated on, behind a flag)
  returns an LLM completion via the harness's active provider.
- **A10** Server-initiated `roots/list` returns `{file://<DataDir>}` plus the
  active project root if any.
- **A11** All `go test -race -count=1 -short ./core/...` and frontend tests
  pass.
- **A12** No goroutine leaks across `Open`/`Close` cycles.
- **A13** No plaintext keychain values in `<DataDir>`.
- **A14** `wails dev` reproduces the worked example end-to-end.

## 9. Edge cases

1. `npx` not on PATH — recipe fails at spawn with a user-visible "install
   Node.js" error pointing at https://nodejs.org/.
2. Server takes longer than `init_timeout_ms` — surfaced as timeout in the
   modal; user can retry; pool auto-restart logic is **not** triggered for
   first-time `initialize` failures (only for crashes after a successful
   initialize).
3. Server emits non-JSON to stdout — framer skips, logs at warn, continues.
4. Server emits an unknown notification — drop with a debug log.
5. User has no Brave key — recipe shows a "Get a key →" link, toggle is
   disabled until a key is supplied.
6. Two recipes export tools with the same name — namespace by server name in
   `Tool.Server` (already part of `mcp.Tool`).
7. `recipes.enabled.json` corrupted — log + ignore + start with empty pool;
   user can re-enable from the UI.
8. Server hangs in `Close` past the 2 s grace — SIGKILL still fires and the
   wait-group does not block forever; emit a warn-level event.

## 10. Out of scope (explicit)

- HTTP/SSE transport.
- Per-session / per-project recipe scope.
- Marketplace / community-contributed recipes.
- Signing / supply-chain verification.
- Auto-update of pinned npm versions.
- Resources/Prompts UI surfaces (data path lands here; UI in a follow-on).
- Per-tool permission gating (lives in MCP-tool-execution WP02).

## 11. Open questions

1. **Catalog format** — Go slice vs `embed.FS` JSON. Decision: ship JSON via
   `embed.FS` from the start so the catalog is data, not code.
2. **Sampling default** — off per recipe v1; user toggles on per-server. Add
   a global "allow any server to use the active provider" setting later.
3. **Recipe scope** — harness-global only for v1. Per-project Brave keys
   (billing-isolation) revisited when the context-library mission's project
   model lands.
4. **Cold-spawn UX** — accept the 10+ s first-run npm fetch with a "warming…"
   indicator; revisit when more recipes ship and the wait compounds.

## 12. Out-of-band dependencies

- `@modelcontextprotocol/server-brave-search` (npm) — Anthropic-maintained
  reference server.
- A Brave Search API key (free tier, no card).
- Node.js + `npx` on PATH on the user's machine.
