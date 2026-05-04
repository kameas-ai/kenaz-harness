# Spec: Token + cost telemetry per session

**Status**: draft · **Owner**: alecfeeman

## 1. Why

OpenRouter and Bedrock return per-turn `usage` (prompt_tokens, completion_tokens, total_tokens, cost). The harness logs it (`openrouter.sse.frame` lines carry the JSON) but never aggregates or surfaces it. The user has no way to see "this session has cost me $1.23 across 47 turns."

## 2. Goals

- Aggregate per-turn usage into a per-session running total.
- Surface in the session header / footer status bar (next to the existing context-window meter).
- Per-project rollup in the project landing page.
- Optional Settings dial: monthly cost cap with prompt-on-exceed.

## 3. Functional requirements

| ID | Requirement | Status |
|---|---|---|
| FR-001 | New `session_usage` table: session_id, prompt_tokens, completion_tokens, cost_usd, updated_at. | proposed |
| FR-002 | Adapter layers expose per-turn usage to a chassis-side hook called from the chat runner on each `Final()`. | proposed |
| FR-003 | Hook updates the running total via `coreusage.Manager.Add(sessionID, usage)`. | proposed |
| FR-004 | RPC method `Sessions.GetUsage(sessionID)` returns aggregated counters. | proposed |
| FR-005 | Frontend status bar adds a "$0.12 · 4.2k tokens" cell next to the context meter. Cost source labelling (decided Q12.2 = C — tilde for estimates): provider-reported `cost_usd` renders as `$0.12` (exact); curated-table-derived costs render as `~$0.12` (tilde-prefixed). Hover tooltip explains the source: "from OpenRouter `/usage`" vs "estimated from token counts × per-million rate (last updated `<harness-release-date>`)". | proposed |
| FR-005b | New `core/llm/pricing/` data module with `pricing.yaml` table per `(provider_kind, model_id_pattern)` carrying `input_per_1m_usd`, `output_per_1m_usd`, `cached_input_per_1m_usd` (when applicable). `core/llm/cost.Reducer.Derive(req, resp, profile)` walks: explicit `usage.cost_usd` from provider → tilde-flagged derivation from pricing table → `nil` if no entry exists. The third case shows `4.2k tokens` with no dollar figure (parity with Q12.1's "we don't lie about spend" principle). | proposed |
| FR-005c | Pricing table updates with each harness release. `pricing.yaml` files include a `last_updated: YYYY-MM-DD` header surfaced in the hover tooltip so users see the data's age and adjust trust accordingly. A future mission can fold pricing into the `provider-implementation-uniformity` capability surface, but for v1 it's a standalone curated table on the same release cadence. | proposed |
| FR-006 | Project landing page shows top-10 sessions by cost. | proposed |
| FR-007 | Cost visibility, **not** enforcement (decided Q12.1: warnings-only). The harness surfaces information and warnings about spend; it does NOT block sends. Rationale: true billing caps belong at the inference-provider level (OpenAI/Anthropic/OpenRouter dashboards) where they have authoritative billing data. The harness's per-turn `usage.cost_usd` is provider-reported but we're not the source of truth and can always be a little wrong. Config: `Settings.MonthlyCostNotifyUSD` (default `0` = disabled). When set non-zero, surface non-blocking toasts at 50% / 80% / 100% / 150% / 200% of the threshold per calendar month. Each threshold fires at most once per month; suppressed if user dismisses. Send is never blocked, never confirmed. | proposed |
| FR-007b | Per-session and per-project cost cells appear in the existing chat-surface footer (next to the context-window meter shipped in `7e60a2a`) and on the project landing page. Cells are read-only displays; clicking opens a small panel showing month-to-date spend, per-provider breakdown, and a "set notification threshold" link to Settings. | proposed |
| FR-007c | Help text explicitly points users to provider-side caps: "Hard spend caps are configured in your provider's dashboard (OpenAI usage limits, Anthropic billing, OpenRouter credits). The harness shows you what you've spent here so you know to look there when you're approaching your real cap." | proposed |

## 4. Success criteria

- Sum of session usages reconciles with provider invoices to within 1%.
- Cost cap accurately blocks sends that would exceed the budget.
