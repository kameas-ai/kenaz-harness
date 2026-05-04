# tasks.md — Token + cost telemetry per session

Mission: `token-cost-telemetry-01KQ8TD7`

## WP01 — `core/llm/pricing/` module + curated table

- **Dependencies**: none
- **Effort**: M
- **Files touched**:
  - `core/llm/pricing/pricing.go` (new)
  - `core/llm/pricing/pricing.yaml` (new — input/output/cached per-1M USD rates for: anthropic claude-sonnet-*, claude-haiku-*, claude-opus-*; openai gpt-4o*, o1*, o3*, gpt-4-turbo*; openrouter `*` fallback; bedrock anthropic.claude-*; with `last_updated: 2026-04-27` header)
  - `core/llm/pricing/pricing_test.go` (new)
  - `core/llm/cost/reducer.go` (extend `Source` to `"provider" | "derived" | "unknown"`, add `Derive` overload that consults pricing module)
  - `core/llm/cost/reducer_test.go` (extend)
- **Acceptance criteria**:
  - `pricing.LastUpdated` returns parsed date.
  - `pricing.Lookup("anthropic", "claude-sonnet-4-5")` returns the curated entry; unknown returns `(nil, false)`.
  - `cost.Reducer.Derive` returns provider/derived/unknown variants per the three spec branches.
  - All four tier-1 providers covered in the YAML (anthropic, openai, openrouter, bedrock).
  - Round-trip YAML parse + tooltip-shape struct.

## WP02 — `coreusage.Manager` + monthly aggregation

- **Dependencies**: WP01; upstream `backend-context-window-length-01KQ8TD3` writer seam (or shim)
- **Effort**: L
- **Files touched**:
  - `core/usage/usage.go` (new package, `Manager` interface + sqlite impl)
  - `core/usage/usage_test.go` (new)
  - `core/usage/noop.go` (new — used when `HARNESS_COST_TELEMETRY=off`)
  - `core/session/migrations_session_usage.go` (new — migration 0320: `session_usage_turns`, `v_session_usage`, `session_usage_monthly`, `cost_threshold_fired`)
  - `core/session/migrations_session_usage_test.go` (new)
  - `core/session/migrations.go` (register 0320)
  - `core/rpc/views/agentgraph/chat/chat_runner.go` (extend `RecordTurnUsage` to call `cfg.UsageManager.Add` after `SessionUsageWriter.Persist`)
  - `core/rpc/api.go` (wire `coreusage.Manager` into chat runner config)
- **Acceptance criteria**:
  - `Manager.Add` writes one `session_usage_turns` row with provider/derived/unknown source recorded.
  - `Manager.GetSession` returns the SUM aggregate from `v_session_usage`.
  - `Manager.GetProjectRollup` returns cached monthly aggregate; cache TTL 60s; refreshes on miss.
  - Single-writer invariant: only `RecordTurnUsage` invokes `Add` — verified by grep gate in CI.
  - Feature flag off → no rows written, no events.

## WP03 — `Sessions.GetUsage` + `Projects.GetUsageRollup` RPCs

- **Dependencies**: WP02
- **Effort**: M
- **Files touched**:
  - `core/rpc/views/sessions/api.go` (add `GetUsage`)
  - `core/rpc/views/sessions/api_test.go` (extend)
  - `core/rpc/views/projects/api.go` (add `GetUsageRollup`)
  - `core/rpc/views/projects/api_test.go` (extend)
  - `core/rpc/bindings.go` (export both methods)
  - `frontend/src/lib/harnessClient.ts` (typed wrappers)
  - `frontend/src/lib/types.ts` (`SessionUsage`, `ProjectUsageRollup`, `ProviderRollup`, `SessionRollupEntry`)
- **Acceptance criteria**:
  - `Sessions.GetUsage(sessionID)` returns aggregate matching `v_session_usage` with correct `cost_source` (`provider | derived | mixed | unknown`).
  - `Projects.GetUsageRollup(projectID)` returns top-10 by cost descending, per-provider breakdown summing to total, `pricingDataDate` populated.
  - Frontend types compile; client wrappers tested in `harnessClient.test.ts`.

## WP04 — Footer cost cell + click-to-panel UX

- **Dependencies**: WP03
- **Effort**: M
- **Files touched**:
  - `frontend/src/components/chat/CostCell.vue` (new)
  - `frontend/src/components/chat/CostPanel.vue` (new — month-to-date, per-provider, link to Settings)
  - `frontend/src/components/chat/__tests__/CostCell.test.ts` (new)
  - `frontend/src/components/chat/__tests__/CostPanel.test.ts` (new)
  - `frontend/src/views/sessions/SessionsView.vue` (insert `<CostCell>` next to the context-window meter)
  - `frontend/src/views/sessions/__tests__/SessionsView.test.ts` (extend)
  - `frontend/src/composables/useSession.ts` (subscribe to `session.usage.updated`)
- **Acceptance criteria**:
  - Renders `$0.12 · 4.2k tokens` (provider) / `~$0.12 · 4.2k tokens` (derived/mixed) / `4.2k tokens` (unknown).
  - Tooltip surfaces source string + pricing data date.
  - Click opens `<CostPanel>`; "Set notification threshold" link routes to Settings.
  - Sub-cent rendering: `~<$0.01` for non-zero amounts under one cent.
  - Hidden when `costTelemetryEnabled === false`.

## WP05 — Project landing page top-10-by-cost surface

- **Dependencies**: WP03
- **Effort**: S
- **Files touched**:
  - `frontend/src/views/projects/ProjectLandingPage.vue` (add "Top 10 sessions by cost" card)
  - `frontend/src/views/projects/__tests__/ProjectLandingPage.test.ts` (extend)
- **Acceptance criteria**:
  - Card lists up to 10 sessions sorted by cost desc.
  - Per-provider total chips above the list.
  - Card collapsed when `totalCostUsd === 0`.
  - Pricing-data-date footnote on the card.
  - Hidden when `costTelemetryEnabled === false`.

## WP06 — Threshold notification scheduler + `Settings.MonthlyCostNotifyUSD` dial

- **Dependencies**: WP02 (for the `Manager.Add` tail hook); can land scaffolding in parallel with WP01
- **Effort**: M
- **Files touched**:
  - `core/rpc/views/settings/settings.go` (add `MonthlyCostNotifyUSD float64`, default 0, validation 0 or 1..10000)
  - `core/rpc/views/settings/settings_test.go` (extend)
  - `core/usage/threshold.go` (new — calendar-month math, fires-once-per-pct logic backed by `cost_threshold_fired`)
  - `core/usage/threshold_test.go` (new)
  - `core/usage/usage.go` (call `threshold.Check` from `Add` tail)
  - `core/rpc/eventkinds.go` (register `cost.threshold.crossed` topic)
  - `frontend/src/components/chat/CostThresholdToast.vue` (new)
  - `frontend/src/components/chat/__tests__/CostThresholdToast.test.ts` (new)
  - `frontend/src/views/settings/...` (Settings UI: notification threshold input with FR-007c help text)
- **Acceptance criteria**:
  - With `MonthlyCostNotifyUSD = 10`, accumulating turns to $5.01 fires `pct=50`; to $8.01 fires `pct=80`; etc.
  - Each pct fires at most once per `year_month` (rows in `cost_threshold_fired`).
  - Setting `MonthlyCostNotifyUSD = 0` suppresses all firings.
  - Calendar boundary: a turn that brings cost above 50% on May 1 fires the May threshold even if April was already at 80%.
  - Toast renders, dismisses, and does not re-fire on app restart.
  - Settings dial copy matches FR-007c language pointing users to provider dashboards.

## WP07 — Cross-mission integration test (single writer + double-meter consumer)

- **Dependencies**: WP02–WP06; upstream `backend-context-window-length-01KQ8TD3` merged
- **Effort**: M
- **Files touched**:
  - `core/rpc/views/agentgraph/chat/integration_cost_telemetry_test.go` (new)
  - `core/rpc/integration_test.go` (extend if exists)
  - `scripts/check-single-usage-writer.sh` (new — grep gate executed in CI)
- **Acceptance criteria**:
  - Single fixture chat session: one `chat.run.complete` produces (a) one `sessions.last_usage_json` write, (b) one `session_usage_turns` row, (c) one `session.usage.updated` event. No duplicates.
  - OpenRouter fixture stream with `usage.cost = 0.012` lands as `cost_source = "provider"`, exact dollar in `Sessions.GetUsage` response.
  - Anthropic fixture stream (token counts only) lands as `cost_source = "derived"` with table-rate-derived figure, surfaced through `Sessions.GetUsage`.
  - Bedrock fixture without pricing entry lands as `cost_source = "unknown"`, `cost_usd = NULL`, frontend renders tokens-only.
  - Grep gate fails build if any file outside `core/usage` and `core/session/migrations_session_usage*.go` writes to `session_usage_turns`.
  - Threshold-fired event reaches frontend toast component in the integration harness.
