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
| FR-005 | Frontend status bar adds a "$0.12 · 4.2k tokens" cell next to the context meter. | proposed |
| FR-006 | Project landing page shows top-10 sessions by cost. | proposed |
| FR-007 | Optional `Settings.MonthlyCostCapUSD` triggers a prompt before sends that would exceed the remaining budget. | proposed |

## 4. Success criteria

- Sum of session usages reconciles with provider invoices to within 1%.
- Cost cap accurately blocks sends that would exceed the budget.
