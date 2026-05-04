# plan.md — Token + cost telemetry per session

**Mission**: `token-cost-telemetry-01KQ8TD7`
**Owner**: alecfeeman
**Status**: planned

## 1. Branch contract

- **Feature branch**: `feat/token-cost-telemetry-01KQ8TD7` cut from `main`.
- **Target**: `main`, single squash-merge PR.
- **Hard upstream dep**: `backend-context-window-length-01KQ8TD3` MUST land first. That mission owns the writer (`SessionUsageWriter.RecordTurnUsage` invoked from `chat_runner.driveRun()` on `chat.run.complete`) which persists `sessions.last_usage_json` and emits the `session.usage.updated` broker topic. This mission is the consumer; we add no second writer. If the upstream mission slips, WP01–WP02 land behind a writer shim that calls `Reducer.Derive` from the same chat-runner seam — no parallel pipeline.
- **Soft dep**: `provider-implementation-uniformity-01KQ8V4F` is the long-term home for pricing data — for v1 we ship a standalone curated table on the harness release cadence, with a `last_updated` header that the tooltip surfaces.
- **Tightly coupled writer/consumer split**: WP07 is the joint integration test owned by this mission, exercising both the upstream writer and our reader against a single fixture session.

## 2. Architecture

### 2.1 Aggregation strategy — read-side SQL view + monthly cache

We pick the **hybrid** approach over a separate `session_usage` table:

- **Per-session running total**: live SQL view `v_session_usage` selects `session_id`, JSON-extract'd token + cost fields from `sessions.last_usage_json`, plus `updated_at`. Because the upstream writer overwrites `last_usage_json` per turn (it stores the latest snapshot), we instead lean on `agentgraph_events` / the chat runner emitting a `session.usage.updated` event per turn. The view materializes from a new `session_usage_turns` append-only table (one row per `chat.run.complete`) — populated by `coreusage.Manager.Add` from the SAME seam the upstream uses to write `last_usage_json`. No extra adapter hook.
- **Why not pure read-side off `last_usage_json`**: that column carries the most-recent-turn snapshot, not the running total, so we'd have no per-turn history to sum. We keep the upstream `last_usage_json` semantics intact and add a sibling append-only table for per-turn detail.
- **Per-project monthly rollup cache**: `session_usage_monthly` keyed by `(project_id, year_month, provider_kind)` — refreshed lazily on RPC read with a 60-second TTL. Project landing page hits the cache; per-session footer reads the live view directly (small and fast).
- **Schema** (new migration `core/session/migrations_session_usage.go`, version 0320):
  ```sql
  CREATE TABLE session_usage_turns (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id TEXT NOT NULL REFERENCES sessions(id),
    turn_idx INTEGER NOT NULL,
    provider_kind TEXT NOT NULL,
    model_id TEXT NOT NULL,
    prompt_tokens INTEGER NOT NULL DEFAULT 0,
    completion_tokens INTEGER NOT NULL DEFAULT 0,
    total_tokens INTEGER NOT NULL DEFAULT 0,
    cost_usd REAL,                -- NULL when no pricing entry exists
    cost_source TEXT NOT NULL,    -- 'provider' | 'derived' | 'unknown'
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
  );
  CREATE INDEX ix_session_usage_turns_session ON session_usage_turns(session_id, created_at);
  CREATE INDEX ix_session_usage_turns_month ON session_usage_turns(created_at);

  CREATE VIEW v_session_usage AS
    SELECT session_id,
           SUM(prompt_tokens) AS prompt_tokens,
           SUM(completion_tokens) AS completion_tokens,
           SUM(total_tokens) AS total_tokens,
           SUM(COALESCE(cost_usd, 0)) AS cost_usd,
           MAX(CASE WHEN cost_source='derived' THEN 1 ELSE 0 END) AS any_derived,
           MAX(created_at) AS updated_at
    FROM session_usage_turns
    GROUP BY session_id;

  CREATE TABLE session_usage_monthly (
    project_id TEXT NOT NULL,
    year_month TEXT NOT NULL,        -- 'YYYY-MM' in user's local TZ
    provider_kind TEXT NOT NULL,
    cost_usd REAL NOT NULL,
    prompt_tokens INTEGER NOT NULL,
    completion_tokens INTEGER NOT NULL,
    refreshed_at TIMESTAMP NOT NULL,
    PRIMARY KEY (project_id, year_month, provider_kind)
  );

  CREATE TABLE cost_threshold_fired (
    year_month TEXT NOT NULL,
    pct INTEGER NOT NULL,            -- 50, 80, 100, 150, 200
    fired_at TIMESTAMP NOT NULL,
    PRIMARY KEY (year_month, pct)
  );
  ```

### 2.2 Pricing data module — `core/llm/pricing/`

- **New package** `core/llm/pricing/` extracted from today's `core/llm/cost/starter_table.yaml` in-package data so a future mission can replace it without churning `cost.Reducer`.
  - `pricing.go` exports `Table`, `Entry { ProviderKind, ModelPattern, InputPer1MUSD, OutputPer1MUSD, CachedInputPer1MUSD, CachedInputWritePer1MUSD, ReasoningPer1MUSD }`, `LastUpdated time.Time`.
  - `pricing.yaml` is the curated source: `last_updated: 2026-04-27` header + per-`(provider_kind, model_id_pattern)` entries. Glob matching is reused from the existing `core/llm/cost.matchGlob`.
  - `Lookup(kind, modelID) (*Entry, bool)` is the primary accessor; `LastUpdatedDate() time.Time` powers the tooltip.
- **`cost.Reducer.Derive` upgrade** (lands during `backend-context-window-length` per its plan §2.4 — we coordinate the rename of `cost.Source` from `"starter"` to `"provider" | "derived" | "unknown"`):
  1. Provider-reported `usage.cost_usd` (OpenRouter `usage.cost`, future Anthropic billing extension) → `cost_source = "provider"`, exact dollar in UI.
  2. Pricing-table-derived → `cost_source = "derived"`, `~$X` (tilde-prefixed) in UI.
  3. No matching entry → `cost_source = "unknown"`, `cost_usd = NULL`, UI shows tokens-only with no dollar figure.
- **OpenRouter SSE already includes `usage.cost`**: the adapter stream loop (`core/llm/openrouter/openrouter.go` around the `s.usage` accumulator) gains a `CostUSD *float64` projection on `llm.Usage`. The other three adapters (Anthropic, OpenAI direct, Bedrock) pass through token counts only — `Reducer.Derive` falls through to the table.

### 2.3 `coreusage.Manager` and the runner seam

- **New package** `core/usage/usage.go`:
  ```go
  type Usage struct {
    PromptTokens     int
    CompletionTokens int
    TotalTokens      int
    CostUSD          *float64
    CostSource       string // "provider" | "derived" | "unknown"
    ProviderKind     string
    ModelID          string
  }
  type Manager interface {
    Add(ctx context.Context, sessionID string, u Usage) error
    GetSession(ctx context.Context, sessionID string) (Aggregate, error)
    GetProjectRollup(ctx context.Context, projectID string, ym time.Time) (ProjectRollup, error)
  }
  ```
- **Wiring**: `chat_runner.driveRun()` in `core/rpc/views/agentgraph/chat/chat_runner.go` already hosts the upstream `RecordTurnUsage` seam. We add a sibling field `cfg.UsageManager coreusage.Manager` populated by `core/rpc/api.go` (next to `SessionUsageWriter`), and `RecordTurnUsage` becomes a thin wrapper that:
  1. Persists snapshot onto `sessions.last_usage_json` (upstream behavior, kept).
  2. Calls `cfg.UsageManager.Add(ctx, sessionID, usage)` to append a turn row.
  3. Emits `session.usage.updated` (upstream behavior, kept).
- **Single writer**: nothing else writes `session_usage_turns`. WP07 enforces this with a build-tag-gated invariant test.

### 2.4 RPCs

- **`Sessions.GetUsage(sessionID) → SessionUsage`** in `core/rpc/views/sessions/api.go`:
  ```go
  type SessionUsage struct {
    PromptTokens     int     `json:"promptTokens"`
    CompletionTokens int     `json:"completionTokens"`
    TotalTokens      int     `json:"totalTokens"`
    CostUSD          *float64 `json:"costUsd"`
    CostSource       string  `json:"costSource"`   // "provider" | "derived" | "mixed" | "unknown"
    UpdatedAt        time.Time `json:"updatedAt"`
  }
  ```
  `mixed` is reported when the session has both provider-reported and derived turns — UI then renders with tilde to be safe.
- **`Projects.GetUsageRollup(projectID, ym?) → ProjectUsageRollup`** in `core/rpc/views/projects/api.go`:
  ```go
  type ProjectUsageRollup struct {
    YearMonth        string                 `json:"yearMonth"`
    TotalCostUSD     float64                `json:"totalCostUsd"`
    PerProvider      []ProviderRollup       `json:"perProvider"`
    TopSessions      []SessionRollupEntry   `json:"topSessions"`   // top 10 by cost
    PricingDataDate  string                 `json:"pricingDataDate"`
  }
  ```
- Both views read through `coreusage.Manager`; the project rollup hits `session_usage_monthly` cache with 60s TTL and refreshes off `v_session_usage` aggregates.
- Bindings layer (`core/rpc/bindings.go`) and the typed `frontend/src/lib/harnessClient.ts` get the new methods + types.

### 2.5 Threshold notification scheduler

- **Trigger point**: at the tail of `coreusage.Manager.Add`, after the turn row is persisted, the manager computes the current calendar-month total (via `v_session_usage` joined with sessions→projects, scoped to `created_at >= start-of-month`). Calendar boundary is the user's local timezone, computed off `time.Now().Local()` to match how a human reads a billing month.
- **Threshold check**: if `Settings.MonthlyCostNotifyUSD > 0`, compute `pct = monthTotal / threshold * 100`. For each of `[50, 80, 100, 150, 200]` not yet recorded in `cost_threshold_fired` for the current `year_month`, insert a row and emit a broker event `cost.threshold.crossed { pct, monthTotal, threshold, ym }`. Frontend toast subscribes.
- **Persistence and replay**: the `cost_threshold_fired` table is append-only and keyed by `(year_month, pct)` so re-running the manager on a session restore can't fire twice.
- **Monthly reset**: no scheduled job. The "rollover" is implicit: on the first turn of a new calendar month, the SUM-by-month query naturally returns 0 from prior months, and the threshold table simply doesn't contain rows for the new `year_month` yet, so they fire fresh.
- **Settings dial**: `Settings.MonthlyCostNotifyUSD float64` added to `core/rpc/views/settings`. Default 0 = disabled. Range validation in the UI: 0 or 1..10000. Help text on the dial cites FR-007c — "Hard caps live in your provider dashboard."

### 2.6 Frontend — footer cell + clickable panel

- **Reuse SessionsView footer**: `frontend/src/views/sessions/SessionsView.vue` already gained the context-window meter shipped in `7e60a2a` (per upstream plan). We add a sibling `<CostCell>` component in the same status bar:
  - Layout: `$0.12 · 4.2k tokens` for `costSource === 'provider'`, `~$0.12 · 4.2k tokens` for `'derived' | 'mixed'`, `4.2k tokens` for `'unknown'`.
  - Subscribes to `session.usage.updated` topic via existing `useEventStream` (same lifecycle pattern as the meter).
  - Hover tooltip: source line ("from OpenRouter `/usage`" vs "estimated from token counts × per-million rate, last updated 2026-04-27"), pricing-data-date pulled from RPC.
  - Click opens `<CostPanel>` mini panel with month-to-date spend, per-provider breakdown, and a "Set notification threshold" link routing to Settings.
  - Rounding: `> $1.00` shows two decimals, `$0.01..$1.00` shows three, `< $0.01` shows `~<$0.01` rather than `~$0` (avoids the "we logged a turn but it shows $0" surprise).
- **Project landing page**: `frontend/src/views/projects/ProjectLandingPage.vue` gains a "Top 10 sessions by cost (this month)" card. Same RPC `Projects.GetUsageRollup` call, server-paged at 10. Card collapsed when `totalCostUsd === 0` to avoid bare-empty visual weight.
- **Toast wiring**: extend `frontend/src/composables/useEventStream` consumer in the chat shell to listen for `cost.threshold.crossed`. Reuses the `<MergeSuggestionToast>` styling but a new component `<CostThresholdToast>` because the text and CTA differ.

### 2.7 Feature flag

- **Backend**: `HARNESS_COST_TELEMETRY` env var read once at `core.New()`; default `"on"`. When `off`:
  - `coreusage.Manager` is the no-op implementation; no writes, no events.
  - Migration 0320 still runs (additive, harmless).
  - RPCs return zero rollups.
- **Frontend**: bindings expose a `harness.config.costTelemetryEnabled` flag (set from the same env at startup). When `false`, footer cell hidden, project landing card hidden, toasts suppressed.

## 3. Risk register

| Risk | Likelihood | Impact | Mitigation |
|------|-----------|--------|------------|
| Clock skew at month boundary (turn at 23:59:59 local fires under prior month, threshold check uses next month's window) | Medium | Low | All boundary math uses one captured `time.Now().Local()` per `Manager.Add` call; reused across the SUM query, the `created_at` write, and the `year_month` key. No drift inside a single Add. |
| Pricing table drifts from real provider billing | High | Medium | (a) Tilde prefix flags every derived figure as estimate. (b) Tooltip exposes `last_updated`. (c) For OpenRouter, prefer provider-reported `usage.cost` always — never override with table. (d) Reconcile-to-1% goal in spec §4 is best-effort; we don't claim hard reconciliation. |
| Tiny-amount rounding to `~$0.00` looks broken | Medium | Low | Render `~<$0.01` for non-zero amounts under one cent. Token count still shown alongside. |
| Cross-mission writer ordering (we ship before backend-context-window) | Medium | High | WP02 ships behind a writer shim that calls `Reducer.Derive` directly from the chat runner if the upstream `SessionUsageWriter` seam isn't present. WP07's integration test fails-loud when both writers exist (asserts only one write per `chat.run.complete`). |
| Project landing page slow with thousands of sessions | Low | Medium | `session_usage_monthly` cache with 60s TTL absorbs hot reads; rebuild query is a single `GROUP BY` on indexed `created_at`. |
| Threshold scheduler fires duplicate toasts on app restart | High | Low | `cost_threshold_fired` table makes the 50/80/100/150/200 fires idempotent per `year_month`. |
| Provider misreports `usage.cost` (e.g., zero for free-tier router models) | Medium | Low | Adapter records `cost_source = "provider"` only when `cost > 0`. Zero-cost falls through to derivation, which for `kind=ollama` with all-zero rates renders `~$0.00 · N tokens` cleanly. |
| Settings dial collides with existing MaxAgentTurns dial UI density | Low | Low | New field added to existing Settings → "Notifications" section; no dedicated screen. |
| Wails event stream backpressure if user has many open project tabs | Low | Low | `cost.threshold.crossed` fires at most 5 times per month per app session; not a streaming load. |
| Migration 0320 on a fresh install vs. existing DBs | Low | Low | Pure additive (CREATE TABLE / VIEW), no ALTER on existing rows; numbered above all upstream missions to avoid collision. |

## 4. Sequencing

```
WP01 (pricing module) ──┐
                        ├─► WP02 (coreusage.Manager + migration) ─► WP03 (RPCs) ─► WP04 (footer + panel) ─► WP07 (cross-mission integration)
                        │                                                       └─► WP05 (project landing card)
                        └─► WP06 (threshold scheduler + Settings dial) ─► WP04 (toast wiring)
```

WP01 is parallelizable with WP06's settings-shape work. WP02 lands only after the upstream `backend-context-window-length` runner seam exists (or behind the documented shim). WP07 is the gate.

## 5. Test surface

- **Backend unit**: `core/llm/pricing/pricing_test.go` — glob match, missing-entry returns `nil`, `LastUpdated` parses.
- **Backend unit**: `core/llm/cost/reducer_test.go` extension — three branches (provider / derived / unknown), `cost_source` correctness.
- **Backend unit**: `core/usage/usage_test.go` — `Add`, `GetSession`, `GetProjectRollup`, monthly cache TTL.
- **Backend unit**: `core/session/migrations_session_usage_test.go` — migration creates table, view, threshold-fired table; idempotent re-run.
- **Backend unit**: `core/rpc/views/sessions/api_test.go` extension — `GetUsage` happy path + empty session.
- **Backend unit**: `core/rpc/views/projects/api_test.go` extension — `GetUsageRollup` top-10, per-provider, cache hit + miss.
- **Backend unit**: `core/usage/threshold_test.go` — fires once per pct per month; survives restart; respects `MonthlyCostNotifyUSD = 0`.
- **Frontend unit**: `SessionsView.test.ts` extension — three render branches (`provider`/`derived`/`unknown`), tooltip text, click opens panel.
- **Frontend unit**: `ProjectLandingPage.test.ts` extension — top-10 list, empty-state collapse.
- **Frontend unit**: `CostThresholdToast.test.ts` — render, dismiss, no-replay.
- **Cross-mission integration**: `core/rpc/integration_test.go` — single writer invariant; OpenRouter adapter fixture round-trips `usage.cost` to RPC; pricing-derived path round-trips with tilde flag.

## 6. Rollout

1. **Day 0**: WP01 lands the standalone `core/llm/pricing/` module + curated YAML + tooltip date.
2. **Day 0**: WP06 lands `Settings.MonthlyCostNotifyUSD` as a no-op dial (UI present, scheduler not yet wired).
3. **Day 1 (after upstream `backend-context-window-length` writer seam)**: WP02 lands `coreusage.Manager` + migration 0320; runner seam wired; turn rows accumulating.
4. **Day 1**: WP03 lands the two RPCs.
5. **Day 2**: WP04 lands footer cell + click panel + toast wiring; WP05 lands project landing card; WP06 wires the scheduler-to-toast path end-to-end.
6. **Day 2**: WP07 verifies cross-mission single-writer invariant.
7. **Feature flag**: `HARNESS_COST_TELEMETRY=on` ships as default. `off` is exercised in CI to ensure no regressions in non-cost surfaces.

## 7. Out of scope

- Backfilling historical `last_usage_json` rows from before this mission. Top-10 only counts post-migration turns.
- Per-user / per-team aggregation. Single-user harness assumption holds.
- Cost export (CSV / API). Future mission.
- Currency other than USD. The pricing module schema reserves a `currency` field but v1 hardcodes USD.
- Hard cap enforcement. Q12.1 decision: visibility only.
- Sub-cent decimal precision beyond three places. Future mission once we have a reconciliation harness against real invoices.
