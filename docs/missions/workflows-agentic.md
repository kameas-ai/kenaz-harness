# Workflows Agentic — Rollout Guide

Mission: `workflows-agentic-01KW2D3X`

## Background

Kenaz ships as a personal AI assistant with direct access to email, Slack,
calendar, and code. The most-requested power-user feature is the ability to
encode repeated multi-step tasks as a runnable workflow — "pull my morning
messages, summarise them, and push an OS notification at 07:00" — without
requiring the user to build or deploy infrastructure. The `workflows-agentic`
mission delivers this end-to-end: a YAML workflow schema with DAG execution
semantics, a cron scheduler, a browsable catalog of starter templates, external
network runners, a control-flow library, a `/wf` slash command, and the
full audit + Cedar policy gate. See `docs/workflows.md` for the PRD.

## Status

| Work Package | Title | Status |
|---|---|---|
| WP01 | DAG semantics (multi-input, parallel batches, topological sort) | Merged |
| WP02 | Cron scheduler + migration 0321 + 4 RPCs | Merged |
| WP03 | Catalog + install flow + preview drawer | Merged |
| WP04 | 5 starter YAMLs (daily_ea_briefing, code_review, web_research, pr_status_poll, doc_generator) | Merged |
| WP05 | web_fetch + web_scrape (CSS + LLM) | Merged |
| WP06 | Control-flow nodes (notify, wait_until, aggregate) | Merged |
| WP07 | /wf slash command + WorkflowsGateway | Merged |
| WP08 | Capstone: e2e integration tests + audit verification + this mission doc | **This branch** |

## Architecture

```
Frontend (WorkflowsView, /wf slash command)
    │
    │  Wails RPC
    ▼
core/rpc/views/workflows/API     (WorkflowsAPI interface + impl)
    │                    │
    │  Cedar gate         │  Audit emitter
    │                    │
    ▼                    ▼
core/workflows/Engine         core/context/audit/
    │                             (workflow.executed, .saved,
    │  StepRunner registry         .deleted, .step_failed,
    │                              .network_fetch, notify.sent)
    ├── model_turn    (LLMStreamer)
    ├── mcp_call      (MCPCaller)
    ├── shell         (os/exec)
    ├── tool_call     (ToolCaller)
    ├── http_request  (net/http)
    ├── web_fetch     (web.Fetcher + robots.txt)
    ├── web_scrape    (CSS extractor | LLM extractor)
    ├── notify        (Notifier + MCPCaller)
    ├── wait_until    (wall-clock / condition poller)
    ├── aggregate     (merge | array | concat)
    ├── read_artifact / write_artifact
    ├── transform     (${...} template expansion)
    └── conditional   (predicate evaluator + branch skip)
    │
    ├── core/workflows/catalog/     (builtin FS + Store + RecipeRegistry)
    ├── core/workflows/scheduler/   (CronScheduler, robfig/cron v3)
    └── core/workflows/storage      (SQLite — workflow_runs_cache, workflow_schedules)
```

## Step kinds

| Kind | Description | Key fields |
|---|---|---|
| `model_turn` | LLM completion via LLMStreamer | `user_prompt`, `profile`, `model`, `allow_tools` |
| `tool_call` | Named tool via ToolCaller | `tool_name`, `tool_args` |
| `mcp_call` | MCP server tool call | `server`, `tool_name`, `tool_args` |
| `http_request` | Raw HTTP (1 MiB cap, 30 s default) | `method`, `url`, `headers`, `body` |
| `shell` | os/exec subprocess | `cmd`, `args`, `cwd`, `env`, `timeout_ms` |
| `read_artifact` | Load artifact bytes | `artifact_id_ref` |
| `write_artifact` | Persist artifact bytes | `title`, `content`, `mime_type` |
| `transform` | `${...}` template render | `template` |
| `conditional` | Predicate + branch skip | `if`, `then_step`, `else_step` |
| `web_fetch` | robots.txt-aware HTTP fetch | `url`, `user_agent`, `min_interval_ms` |
| `web_scrape` | CSS or LLM data extraction | `url`, `mode` (`css`/`llm`), `extractors`, `extract_prompt` |
| `notify` | Multi-surface notification | `notify_title`, `notify_body`, `surface` (`os`/`slack`/`email`/`push`) |
| `wait_until` | Block until time or condition | `until` (RFC 3339), `duration`, `condition` |
| `aggregate` | Merge outputs from multiple parents | `inputs_from`, `strategy` (`merge`/`array`/`concat`), `separator` |

## DAG semantics

By default, workflow steps execute sequentially in declaration order. When any
step carries a non-empty `inputs_from` list, the loader switches to DAG mode
(WP01):

1. **Topological sort** — Kahn's BFS algorithm (`core/workflows/loader.go:topoSort`)
   validates the graph and rejects cycles with `ErrWorkflowCycle`, including the
   offending cycle path in the error message (`"step_a → step_b → step_a"`).

2. **Parallel batches** — `Engine.runDAG` builds a ready-queue of all steps
   whose parents have completed, fires the whole batch concurrently via
   goroutines + WaitGroup, then advances. Steps in the same batch execute
   in parallel; steps with unsatisfied `inputs_from` edges wait for their
   parents.

3. **Output references** — each step's output is stored in `RunContext.StepOutputs`
   keyed by name. Downstream steps reference it via `${step.<name>.output}` in
   any string field.

4. **Failure semantics** — the first failing step in a batch marks the run
   `failed` and skips all remaining unstarted steps. In-flight goroutines in
   the same batch finish before the failure is propagated.

Reference: WP01 implementation in `core/workflows/runtime.go` (`runDAG`).

## Catalog

Six built-in workflow YAMLs ship under `core/workflows/builtin/`:

| ID | Description |
|---|---|
| `daily_ea_briefing` | Email + Slack + Calendar → morning OS briefing at 07:00 |
| `code_review` | Runs a model_turn over a git diff to generate review feedback |
| `web_research` | web_fetch + web_scrape + model_turn research pipeline |
| `pr_status_poll` | Polls GitHub PR status on a cron schedule |
| `doc_generator` | Reads an artifact and produces structured documentation |
| `plan_implement_review` | Three-step LLM chain: plan → implement → review |

**List**: `Catalog_List` returns all entries with `InstallStatus` reflecting
the current DB state (`not_installed` / `installed`).

**Preview**: `Catalog_Get` returns the raw YAML source plus the structured
entry so the preview drawer can render both without a second round-trip.

**Install**: `Catalog_Install` calls `Store.Save`, optionally arms a cron
schedule via `Scheduler.Register`, and returns `InstalledRef.MissingCredentials`
listing any `mcp_call.server` names not present in the recipe registry.

## Scheduler

The cron scheduler (`core/workflows/scheduler/CronScheduler`) wraps
`github.com/robfig/cron/v3`:

- **Format**: standard 5-field cron (`minute hour dom month dow`). The parser
  also accepts 6-field with seconds and cron descriptors (`@daily`, etc.).
- **Timezone**: per-schedule timezone is applied by prepending `CRON_TZ=<IANA>`
  to the spec (e.g. `"CRON_TZ=America/New_York 0 7 * * *"`). This avoids the
  process-wide `WithLocation` footgun.
- **Persistence**: schedules survive chassis restarts via the `workflow_schedules`
  SQLite table (migration 0321). `New()` reloads and re-registers all enabled
  rows from the DB.
- **Dispatcher interface**: the scheduler decouples from the engine via
  `Dispatcher.Dispatch(ctx, workflowID, scheduled)`, which the production chassis
  wires to the real `Engine.Run`. Tests inject a `fakeDispatcher`.
- **`Tick(time.Time)`**: a no-op on `CronScheduler`; overridden by test
  implementations that need clock injection.

## Audit kinds

All workflow-family events are defined in `core/context/audit/audit.go`.
Privacy invariant: no payload carries step inputs, step outputs, prompt text,
response text, or full URLs — only ids, kind labels, metrics, and hostnames.

| Kind | Payload struct | When emitted |
|---|---|---|
| `workflow.executed` | `WorkflowExecutedPayload` | On Run completion (success or failure) |
| `workflow.saved` | `WorkflowSavedPayload` | On Save success |
| `workflow.deleted` | `WorkflowDeletedPayload` | On Delete success |
| `workflow.step_failed` | `WorkflowStepFailedPayload` | Per failed step inside a Run |
| `workflow.network_fetch` | `WorkflowNetworkFetchPayload` | Per successful web_fetch / web_scrape |

The `notify` runner emits via the narrower `AuditEmitter.EmitNotifySent`
interface (which the RPC layer can adapt to `audit.Emitter`) rather than
calling `audit.Emit` directly, preserving the privacy invariant: only the
surface target name and a truncated title (≤60 chars) are recorded; the
notification body is never included.

## Acceptance: EA Briefing Demo

The daily EA briefing (`daily_ea_briefing.yaml`) is the mission's acceptance
demo. It works end-to-end when:

1. `Catalog_Install("daily_ea_briefing")` returns `Scheduled: true` and lists
   `gmail`, `slack`, `google_calendar` in `MissingCredentials` (because those
   MCP servers are not in the default recipe registry).

2. After configuring the three MCP servers, a manual `RunNow` fires the
   workflow. The engine fans the three `mcp_call` steps out in parallel (DAG),
   waits for all three to complete, runs `write_briefing` (model_turn), then
   dispatches the OS notification via `notify_step`.

3. At 07:00 America/New_York, the cron scheduler fires automatically, the
   engine runs identically, and the user's desktop shows the morning briefing
   notification.

4. The audit log records `workflow.executed` (status=completed, step_count=5)
   and the runner-level `notify.sent` event for the `os` surface.

The WP08 capstone test `Test_DailyEABriefing_EndToEnd` in
`core/workflows/integration_agentic_test.go` verifies this path with fake
dependencies, asserting all four points above programmatically.

## Open follow-ups

Tracked under the `workflow-extensions-01KW2D3Y` umbrella mission:

- **Visual editor** — drag-and-drop DAG canvas for authoring workflows
  without hand-editing YAML.
- **`sub_workflow` step kind** — invoke a named workflow as a single step
  of a parent workflow, enabling workflow composition.
- **Marketplace** — remote workflow registry beyond the built-in catalog;
  version pinning + signature verification.
- **Human-in-the-loop (HITL)** — `human_review` step kind that pauses
  execution and surfaces a UI prompt before proceeding.
- **Authoring agent** — a `/wf-author` command that turns a natural-language
  description into a YAML workflow via a model_turn chain.
