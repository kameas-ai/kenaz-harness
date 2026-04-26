# Implementation Plan: MCP stdio pool + shipped recipes

**Branch**: `mcp-stdio-pool-and-shipped-recipes-01KQ51PR` (lane allocated at WP-implement time)
**Date**: 2026-04-26
**Spec**: `kitty-specs/mcp-stdio-pool-and-shipped-recipes-01KQ51PR/spec.md`

## Summary

Real, full-featured stdio MCP pool replacing the in-memory fixture in production. Spawns child processes per the MCP protocol spec, supports the full standard surface (tools/resources/prompts/sampling/roots/logging/progress/cancellation), auto-restarts with backoff, and ships a Brave Search recipe with a Tools-hub UI for toggling. Fixture stays for unit tests of upstream consumers (toolloop, permissions, rpc); production wiring uses the new `core/mcp/stdio.Pool`.

## Technical Context

- **Language/Version**: Go 1.22+ (matches existing `go.mod`); TypeScript 5.x for frontend.
- **Primary Dependencies**: stdlib `os/exec`, `encoding/json`, `bufio` (no third-party JSON-RPC library — kept thin); `embed` for the catalog. Existing `core/secrets`, `core/llm`, `core/logging`, `core/rpc/StreamBroker`. Frontend: Vue 3, existing `useHarnessClient` + Wails bindings.
- **Storage**: `<DataDir>/mcp/recipes.enabled.json` (single JSON file, atomic write). OS keychain via `core/secrets.MemoryBackend → zalando/go-keyring` (already wired). Catalog via `embed.FS`.
- **Testing**: `go test -race -count=1 -short ./core/mcp/... ./core/rpc/...` for backend; Vitest for frontend; integration test that actually spawns `node` running an in-tree fake MCP server.
- **Target Platform**: macOS, Linux, Windows desktop (Wails).
- **Project Type**: backend Go + Vue frontend (existing harness shape).
- **Performance Goals**: per NFR-003, spawn → initialize → first `tools/list` < 3 s on warm npm cache; per FR-005, concurrent `Call` invocations against the same server are independent.
- **Constraints**: NFR-001 (-race clean), NFR-002 (no goroutine leaks), NFR-006 (no plaintext keychain values on disk). FR-002 framer must skip non-JSON lines.

## Charter Check

- DIRECTIVE_001 (no cyclic imports): `core/mcp/stdio` depends on `core/mcp` (types), `core/secrets`, `core/llm`, `core/logging`. None depend back. Pass.
- C-001 (no third-party SDK in `core/`): stdlib only; `zalando/go-keyring` is reached transitively through `core/secrets`. Pass.
- Privacy CI invariant (rpc never re-emits raw payloads): the new RPC view returns recipe metadata + status snapshots, never tool-call bodies. Pass.
- No new SDK imports outside the established secrets seam. Pass.

## Project Structure

```
core/mcp/
├── pool.go                         # UNCHANGED  (Pool, ServerSpec, Tool)
├── fixture/                        # UNCHANGED  (kept for unit tests)
│   ├── pool.go
│   └── pool_test.go
├── stdio/                          # NEW
│   ├── pool.go                     # StdioPool — implements core/mcp.Pool
│   ├── server.go                   # ServerInstance lifecycle + restart logic
│   ├── framer.go                   # newline JSON-RPC reader/writer
│   ├── protocol.go                 # message shapes for every method we speak
│   ├── router.go                   # ResponseRouter (id → chan)
│   ├── sampling.go                 # sampling/createMessage adapter onto core/llm
│   ├── roots.go                    # roots/list adapter
│   ├── log.go                      # notifications/message → slog
│   ├── progress.go                 # notifications/progress → StreamBroker
│   ├── ringbuf.go                  # 64 KiB stderr ring buffer
│   ├── pool_test.go
│   ├── server_test.go
│   ├── framer_test.go
│   ├── router_test.go
│   ├── sampling_test.go
│   ├── log_test.go
│   ├── integration_test.go         # real subprocess: in-tree fake MCP server in testdata/
│   └── testdata/
│       ├── fake-mcp-server/main.go    # builds with go test
│       └── golden/*.json
└── recipes/                        # NEW
    ├── recipes.go                  # Recipe / EnvKey / Capabilities + Registry
    ├── shipped.go                  # //go:embed shipped.json
    ├── shipped.json                # v1: Brave Search
    ├── enabled.go                  # EnabledRecipes load/save + atomic write
    ├── recipes_test.go
    └── enabled_test.go

core/rpc/
├── api.go                          # MODIFIED: newLLMStack uses stdio.NewPool
└── views/
    ├── mcp/                        # MODIFIED: existing reference-only listing kept
    └── tools/                      # NEW package
        ├── api.go                  # ListRecipes / InstallRecipe / Uninstall / ForgetKey / Status
        ├── impl.go
        ├── impl_test.go
        └── bindings.go             # Wails-binding fragment (or extension to existing bindings.go)

core/core.go                        # MODIFIED: Start enables persisted recipes; Shutdown closes pool

frontend/src/views/tools/
├── ToolsView.vue                   # MODIFIED: prepend Kaneaz Tools section
├── KaneazToolsPanel.vue            # ALREADY EXISTS — extend with recipe rows
├── RecipeKeyPromptModal.vue        # NEW
└── __tests__/
    ├── KaneazToolsPanel.test.ts
    └── RecipeKeyPromptModal.test.ts

frontend/src/lib/
├── types.ts                        # MODIFIED: Recipe / RecipeStatus types
└── useHarnessAPI.ts                # MODIFIED: tools API surface added

docs/
└── mcp-recipes.md                  # NEW: user-facing add-a-recipe walkthrough
```

**Structure Decision**: Existing harness layout (Go backend + Vue frontend). New backend code clusters under `core/mcp/stdio/` and `core/mcp/recipes/`; new RPC view goes under `core/rpc/views/tools/` (the directory does not yet exist — created alongside the existing `core/rpc/views/mcp/` reference-only view, which serves a different surface). The frontend `KaneazToolsPanel.vue` shell already exists hosting the Memory tool entry; the recipe rows extend that panel.

## Phase 0 — Research summary

(Authoritative source: `research.md`)

- Stdio framing is newline-delimited JSON-RPC 2.0; bounded line size at 4 MiB; non-JSON lines skipped with warn.
- Initialize handshake pins `protocolVersion: "2024-11-05"`; first-init failure does NOT trigger restart (post-init crash does).
- Cancellation correlates by request id; late responses dropped without blocking the reader.
- Sampling pass-through translates onto `corellm.GenerationRequest`; gated per-recipe with default off.
- Roots responds with `<DataDir>` + optional active project root.
- Logging notifications map to slog under `mcp.<recipe>.message`; progress notifications fan out on `mcp:progress` topic.
- Health ping every 30 s, 2 consecutive failures → restart.
- `npx -y` cold-spawn requires a separate first-byte timeout (30 s) distinct from `init_timeout_ms` (5 s).
- Brave Search exposes `brave_web_search` and `brave_local_search`, requires `BRAVE_API_KEY`.
- Catalog ships as `embed.FS` JSON, matches existing tree conventions.

## Phase 1 — Foundations (`core/mcp/stdio/{framer,protocol,router,server}.go`)

**Targets**: framer.go, protocol.go, router.go, ringbuf.go, server.go (lifecycle subset).

**Key types/functions**:
- `Framer` with `NewFramer(r, w)`, `Read() (RequestEnvelope, error)`, `Write(env) error`. `bufio.Scanner` with 4 MiB buffer, custom split that strips trailing `\r`. Non-JSON lines → return sentinel `errSkipped`.
- Method-specific param/result shapes in `protocol.go` for: `initialize`, `tools/list`, `tools/call`, `resources/list`, `resources/read`, `resources/subscribe`, `prompts/list`, `prompts/get`, `ping`, `roots/list` (server→client), `sampling/createMessage` (server→client), `notifications/initialized`, `notifications/cancelled`, `notifications/progress`, `notifications/message`, `notifications/tools/list_changed`, `notifications/resources/list_changed`, `notifications/resources/updated`, `notifications/prompts/list_changed`.
- `ResponseRouter` with `Register(id) <-chan ResponseEnvelope`, `Deliver(env)`, `Cancel(id)`. Late deliveries dropped via non-blocking select.
- `RingBuffer` — fixed 64 KiB, lock-around-write, `Snapshot(maxBytes)` returns the tail.
- `ServerInstance.Spawn(ctx, recipe, env)` — `exec.CommandContext`, wires Pipes, starts reader/writer/stderr-pump goroutines under `sync.WaitGroup`, sends `initialize`, awaits response with `firstByteTimeout` and `recipe.InitTimeoutMs` deadlines, sends `notifications/initialized`.
- `ServerInstance.Close(ctx)` — SIGTERM, wait 2 s, SIGKILL; close stdin to nudge graceful exit; `Wait()`; signal `doneCh`; wait the WaitGroup.

**Testing**:
- `framer_test.go`: round-trip request, multi-line input including malformed lines, oversized lines (4 MiB +1) → transport error and reader exits.
- `router_test.go`: parallel registers + delivers; cancellation followed by late delivery doesn't block; race-detector clean.
- `server_test.go`: launches a stub binary (`testdata/fake-mcp-server/main.go` via `go test`), runs initialize → close.

**Dependencies**: none (foundation).

## Phase 2 — Pool surface (`core/mcp/stdio/pool.go`)

**Targets**: `core/mcp/stdio/pool.go`.

**Key types/functions**:
- `type Pool struct{ mu sync.RWMutex; servers map[string]*ServerInstance; samplingHandler SamplingHandler; rootsHandler RootsHandler; broker EventPublisher }`
- `NewPool(opts PoolOptions) *Pool` where `PoolOptions{ Sampler, Roots, Broker, Logger }`.
- `Open(ctx, specs)` — concurrent spawn via `errgroup.Group`; one bad spec doesn't poison the pool.
- `Close(ctx)` — fan out close; collect first error; return after all goroutines exit.
- `Tools(ctx)` — aggregates each running server's cached tool list.
- `Call(ctx, server, tool, args)` — looks up instance, calls `inst.CallTool(ctx, tool, args)` which assigns id, registers router channel, writes envelope, blocks on channel, handles ctx cancellation.
- Compile-time witness `var _ mcp.Pool = (*Pool)(nil)`.

**Testing**: `pool_test.go` — boots fake server twice (`server-A`, `server-B`); 50 concurrent tool calls across both; asserts results align; asserts `Close` returns goroutine count to baseline (NFR-002).

**Dependencies**: Phase 1.

## Phase 3 — Resilience (auto-restart, health pings, EOF)

**Targets**: `server.go` (extend with restarter), `pool.go` (status surface).

**Key types/functions**:
- `runSupervisor(ctx)` — long-lived. Listens on `crashCh` (closed by reader/writer on EOF or fatal write). On crash: prune `restartHistory` to <5 min; if `len < 3`, sleep `backoff[len]` (1s/2s/4s), respawn; else mark `state="failed"` and emit a structured event.
- `healthPinger(ctx)` — ticker at `recipe.PingPeriodMs`; sends `ping` with 5 s deadline; tracks consecutive failures; on second consecutive failure signals supervisor.
- `RecipeStatus()` — produces snapshot from in-memory state.
- `(*Pool).RecipeStatus(id) (RecipeStatus, bool)` — public read accessor for the rpc view.
- EOF detection: framer.Read returns `io.EOF` → reader emits crash signal and exits.

**Testing**:
- `server_test.go` extension: fake server with `--crash-on-call`; assert restart fires with measured 1 s backoff; third crash within 5 min → `failed`.
- Health ping test: fake server with `--ignore-pings`; assert two failures trip restart (~70 s wallclock; gate behind `-tags=slow`).
- EOF test: `cmd.Process.Kill()` from outside; assert restart cycle.

**Dependencies**: Phase 2.

## Phase 4 — Server-initiated requests (roots, sampling, log, progress)

**Targets**: `roots.go`, `sampling.go`, `log.go`, `progress.go`.

**Key types/functions**:
- `RootsHandler interface{ Roots(ctx) []Root }` — Pool dispatches `roots/list` through this. Default impl `DefaultRoots(dataDir, projectRootProvider func() string)`.
- `SamplingHandler interface{ CreateMessage(ctx, req SamplingRequest) (SamplingResponse, error) }` — translates onto `corellm.Registry.Stream`. Per-server gate: when `inst.samplingOn == false`, pool responds `RPCError{Code:-32601}` without invoking the handler.
- `LogSink` — handles `notifications/message`; calls `logging.L().With("mcp.recipe", id).Log(level, payload.message, attrs)`.
- `ProgressForwarder` — handles `notifications/progress`; emits `mcp:progress` event onto the broker.
- Reader loop in `server.go` dispatches: `id != nil && method != ""` → server-initiated request → handler map; `id != nil && method == ""` → response → router; `id == nil` → notification → notification topic.

**Testing**:
- `sampling_test.go`: stub `corellm.Registry`; fake server emits `sampling/createMessage`; assert request shape mapped; assert response shape returned. Gate test: `samplingOn=false` → server gets `-32601`.
- `roots_test.go`: fake server emits `roots/list`; assert response includes `file://<dataDir>` and project root.
- `log_test.go`: capture slog; fake server emits `notifications/message{level:"warning", data:{message:"x"}}`; assert slog record.
- `progress_test.go`: fake broker; fake server emits progress; assert event published.

**Dependencies**: Phase 1. Independent of Phase 3.

## Phase 5 — Recipes catalog (`core/mcp/recipes/`)

**Targets**: `recipes.go`, `shipped.go`, `shipped.json`, `recipes_test.go`.

**Key types/functions**:
- `Recipe`, `EnvKey`, `Capabilities`, `SamplingPolicy` per data-model.md.
- `Catalog struct{ entries map[string]Recipe }` — `LoadShipped() (*Catalog, error)`, `List()` (catalog declaration order), `Get(id)`.
- `(*Recipe).ToServerSpec(env) mcp.ServerSpec`.
- `shipped.go`: `//go:embed shipped.json` + `init()` parses into package-level `var shipped *Catalog`.
- `shipped.json` ships Brave Search per spec.md FR-020.

**Testing**: parse shipped.json; assert Brave entry; assert ID validation regex; `ToServerSpec` round-trip.

**Dependencies**: none directly; Pool consumes.

## Phase 6 — Persistence + install/uninstall (`core/mcp/recipes/enabled.go`)

**Targets**: `enabled.go`, `enabled_test.go`.

**Key types/functions**:
- `EnabledRecipe`, `EnabledRecipes` per data-model.md.
- `LoadEnabled(dataDir)` — missing → empty; corrupt → log + empty.
- `Save(dataDir)` — atomic write: tmpfile + fsync + rename + fsync parent.
- `Add(rec)` / `Remove(id)`.
- `KeychainLocator(recipeID, envName) string { return "mcp/" + recipeID + "/" + envName }`.
- `ResolveEnv(ctx, backend, recipe) (map[string]string, error)`.

**Testing**: round-trip save/load; corrupt-file recovery; concurrent Save (atomic semantics); locator format. **NFR-006 plaintext-on-disk scan** in `core/rpc/views/tools/impl_test.go` — install with `BRAVE_API_KEY=test_secret_marker_xyz`, walk `<DataDir>` recursively, assert no file contains the literal.

**Dependencies**: Phase 5.

## Phase 7 — RPC view (`core/rpc/views/tools/`)

**Targets**: `api.go`, `impl.go`, `impl_test.go`, bindings.

**Key types/functions**:
- `RecipeListing struct { Recipe; Enabled bool; Status RecipeStatus }`
- `ToolsAPI interface { ListRecipes; InstallRecipe; UninstallRecipe; ForgetRecipeKey; RecipeStatus }`.
- `API struct { catalog; enabled; pool *stdio.Pool; secrets secrets.Backend; dataDir; mu sync.Mutex }`.
- `InstallRecipe`: validate required env keys; write each via the existing `keychainWriter`; add to `EnabledRecipes` and `Save`; build spec via `recipe.ToServerSpec(resolvedEnv)`; call `pool.OpenOne(ctx, spec)`; emit audit `mcp.recipe.installed`. Plaintext env zeroed before return.
- `UninstallRecipe`: pool removes server (SIGTERM grace), enabled list updated, save.
- `ForgetRecipeKey`: keychain delete.
- `RecipeStatus`: thin pass-through.

**Testing**: stub `secrets.Backend` + a fake `stdio.Pool` interface. Install with missing required env → error; Install + uninstall round-trip; ForgetRecipeKey deletes locator; RecipeStatus pass-through honors not-installed; NFR-006 scan after Install.

**Dependencies**: Phases 2, 5, 6. Adds `OpenOne(ctx, spec)` and `CloseOne(ctx, id)` to the Pool surface (extensions beyond `mcp.Pool` — declared on `*stdio.Pool` directly).

## Phase 8 — Wire into chassis (`core/rpc/api.go`, `core/core.go`)

**Targets**: `core/rpc/api.go` (`newLLMStack` + `New`), `core/core.go` (`Start`, `Shutdown`).

**Key changes**:
- `core/rpc/api.go`: replace `fixture.New()` in `newLLMStack` with `stdio.NewPool(stdio.PoolOptions{ Sampler: samplingHandlerFromRegistry(reg), Roots: stdio.DefaultRoots(c.DataDir(), nil), Broker: broker, Logger: logging.L() })`. The `mcpPoolAdapter` is unchanged. Construct the new `tools.API` with the same pool + `secretsBackend` + `recipes.Catalog` + loaded `EnabledRecipes`. Wire `API.toolsAPI` and `func (a *API) Tools() tools.ToolsAPI`. Update `core.Subsystems.MCP` so `c.MCP = pool` (lets `core.Shutdown` close it via existing wiring at `core.go:174-176`).
- `core/core.go`: extend `Start(ctx)` — construct specs from `recipes.LoadEnabled` + `recipes.LoadShipped` + `secrets.Resolve` and call `c.MCP.Open(ctx, specs)`. `Shutdown` already calls `c.MCP.Close(ctx)`.

**Test plan / pool selection**:
- Continue with `fixture.New()`: `core/toolloop/loop_test.go`, `core/rpc/views/llm/impl_toolloop_test.go`, `core/rpc/api_test.go` chassis tests.
- Use `stdio.Pool` with the in-tree fake server: `core/mcp/stdio/pool_test.go`, `integration_test.go`; `core/rpc/views/tools/impl_test.go` uses a fake `mcp.Pool` shim except the NFR-006 plaintext-scan test.
- New end-to-end test in `core/rpc/api_test.go` with `-tags integration`: boots `core.Core` against temp `DataDir`, installs the fake recipe, runs a tool call through the toolloop, asserts response.

**Dependencies**: Phases 2, 5, 6, 7.

## Phase 9 — Frontend

**Targets**: `KaneazToolsPanel.vue` (extend), `RecipeKeyPromptModal.vue`, `ToolsView.vue` already imports the panel; `types.ts`, `useHarnessAPI.ts`, two test files.

**Key components**:
- `KaneazToolsPanel.vue` is already in place hosting the Memory tool. Extend it: list recipes from `client.tools.listRecipes()` on mount. Each row: category icon (`category → Lucide icon`: `search → Search`, `filesystem → Folder`, `memory → Brain`, `fetch → Globe`, default → `Wrench`), display name, description, status pill, toggle. Toggle on with required env keys → opens `RecipeKeyPromptModal`. Toggle on with all keys present → `installRecipe({})`. Toggle off → `uninstallRecipe(id)`. "Forget key" affordance → `forgetRecipeKey`. Polling: when any visible row is in non-terminal state (`starting`, `restarting`, `running`), poll `recipeStatus` for those rows at 1 Hz; idle when all rows terminal.
- `RecipeKeyPromptModal.vue`: form for each `EnvKey`; required keys marked; "Get a key →" link uses `EnvKey.DocsURL`. Submit → `installRecipe(id, env)`; surfaces error from impl into a banner.
- `ToolsView.vue` already prepends `<KaneazToolsPanel />` above the existing servers table.

**Testing**:
- `KaneazToolsPanel.test.ts`: stub the rpc client; assert list rendering, toggle calls install with empty env when keys present, toggle calls install with modal env on submit; polling mounts/unmounts according to state.
- `RecipeKeyPromptModal.test.ts`: required-key validation; docs link wiring; submit emits the right event.

**Dependencies**: Phase 7 (rpc surface).

## Phase 10 — Polish + integration

**Targets**: `core/mcp/stdio/integration_test.go` (A1 walkthrough), `leak_test.go` (NFR-002 goroutine assertion), `core/rpc/views/tools/no_plaintext_test.go` (NFR-006 scan), `docs/mcp-recipes.md`, `KaneazToolsPanel.vue` cold-spawn "warming…" indicator (state `starting` and elapsed > 4 s).

**Key tests**:
- A1 end-to-end: install fake recipe → toolloop dispatches a call → assistant final response references the result.
- A4 concurrency: 50 concurrent calls against the fake server.
- A7 external kill: `cmd.Process.Kill()` from outside the pool → restart fires.
- A12 no goroutine leaks: snapshot before Open and after Close + 100 ms; tolerance ±2.
- A13 no plaintext on disk: walk `<DataDir>`; assert no file contains the test secret.
- A14: manual checklist in `docs/mcp-recipes.md` (CI cannot run wails dev).

**Dependencies**: all earlier phases.

## Risk register

| Risk | Phase | Mitigation |
|---|---|---|
| `npx` cold-start latency masking errors as timeouts | 1 / 10 | Separate `firstByteTimeout` (30 s) from `init_timeout_ms` (5 s); UI shows "warming…" indicator after 4 s; first-init failure does NOT trigger restart loop. |
| Response-router channel leaks if server replies after our context cancels | 1 / 2 | Router's `Deliver` uses non-blocking `select { case ch <- env: default: log.Debug }`; channel removed from map on ctx cancel before send is attempted. Test at `router_test.go`. |
| `sampling/createMessage` rate-limit / cost amplification | 4 | Per-recipe `samplingEnabled` toggle, default off; when off, server gets `-32601`. Document the cost implication in `docs/mcp-recipes.md`. Future hardening (rate limit per-server, max tokens cap) noted as out-of-scope-for-v1 follow-up. |
| Stderr ring buffer growing unbounded | 1 | Fixed 64 KiB ring buffer; `Snapshot(maxBytes)` returns the tail; `RecipeStatus.StderrTail` capped at 4 KiB. Test asserts buffer occupancy after >64 KiB write stays at 64 KiB. |
| Test flakiness around real subprocess spawning in CI | 1 / 2 / 10 | In-tree fake MCP server in `testdata/fake-mcp-server/main.go` built via `go test` ensures deterministic behavior; `npx`-dependent tests gated behind `-tags=npx_e2e` and excluded from default CI. |
| First-time `recipes.enabled.json` write race with concurrent Open | 6 / 7 | Mutex inside `core/rpc/views/tools.API`; atomic write via tmpfile + rename. `enabled_test.go` covers concurrent Save. |
| Cyclic import risk: `stdio` ↔ `core/llm` for sampling | 4 / 8 | `SamplingHandler` is an interface defined in `core/mcp/stdio`; the adapter onto `corellm.Registry` lives in `core/rpc/api.go` (one import direction only: rpc → stdio + rpc → llm). |
| `protocolVersion` drift when MCP spec advances | 0 / 1 | `SupportedProtocolVersion` is a single constant; bump in a one-line change per future mission. Today pinned at `2024-11-05`. |

## Open questions

(Restated from spec.md §11 + new ones from research)

1. **From spec §11 #3 — Recipe scope**: still harness-global only for v1. (No new info.)
2. **From spec §11 #4 — Cold-spawn UX**: research confirms 6–12 s typical first-run; plan resolves with `firstByteTimeout=30s` + UI indicator. **Question for WP**: should we cache the npm install location (`NPM_CONFIG_PREFIX`) so subsequent recipes are fast?
3. **NEW — Late research validation**: WebFetch was denied in this planning session. The WP that lands the framer (Phase 1) MUST verify the live spec wording for stdio framing, cancellation semantics, and the current `protocolVersion` string before merging. Recommend a research-pass git tag at start of WP01.
4. **NEW — Brave server tool name verification**: WP that lands `shipped.json` MUST verify exact tool names (`brave_web_search`, `brave_local_search`) and env-var name (`BRAVE_API_KEY`) by `npm view @modelcontextprotocol/server-brave-search` or cloning the repo.
5. **NEW — Project root for `roots/list`**: `core/core.go` does not yet have a "current project" concept. Plan punts by passing a nil-able `func() string` to `DefaultRoots`. **Question**: is there any existing per-session state in `core/session` that we should treat as "the active project" for v1? (Note: context-library WP02 currently in flight; this question may resolve when that lands.)
6. **NEW — `OpenOne` / `CloseOne` extension**: the plan adds methods to `*stdio.Pool` not on `core/mcp.Pool`. **Question for WP-breakdown**: should these go on the `mcp.Pool` interface so future transports honor them, or stay struct-only? Recommend: struct-only for now; revisit when a second transport lands (per spec.md §3 non-goal).
