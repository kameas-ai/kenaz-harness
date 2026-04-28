---
work_package_id: "WP11"
title: "Cost table loader and usage→cost reducer"
dependencies:
  - "WP01"
  - "WP04"
planning_base_branch: "main"
merge_target_branch: "main"
branch_strategy: "Planning artifacts were generated on main; completed changes must merge back into main."
subtasks:
  - "T001"
  - "T002"
  - "T003"
  - "T004"
  - "T005"
phase: "Phase 4 - Cost reporting"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP11 – Cost table loader and usage→cost reducer

## Goal

Implement `core/llm/cost` — a starter price-table loader (ships in-tree
as `core/llm/cost/starter_table.yaml`, marked `best_effort: true`),
operator-override merge from `~/.config/kaneaz/cost-table.yaml`
(platform equivalent), and a `Reducer.Derive(usage, kind, model) Cost`
function that attaches cost numbers to `Response.Cost` and to the
`llm/response_final` event payload.

## Spec references

- FR-011 — Token usage and cost reporting (input, output, cached,
  reasoning tokens; derived cost in operator currency).
- NFR-012 — Audit completeness (cost in `response_final`).
- US3 Acceptance Scenario 1 — usage in event log for every
  successful request.
- Spec OQ-2 — ship a starter price table marked best-effort, with an
  operator override path.

## Plan references

- §2 Architectural Placement — `core/llm/cost/`.
- §4 Internal Layering — `CostReducer` derives cost after the
  adapter returns usage; reducer feeds both `Response.Cost` and the
  audit emitter.
- §5.4 Cost-table format — schema (currency, kind, model glob,
  per_million_tokens map).
- §8 R8 — staleness mitigation: `best_effort: true` flag,
  CI freshness warning at > 90 days old.
- §9 OQ-2 default — ship the table.

## Subtasks

- T001 — Define cost-table YAML schema and loader. Glob match by
  `kind` + `model`; resolve to per-million-tokens rates for input,
  output, cached_input_read, cached_input_write, reasoning.
- T002 — Author starter table at `core/llm/cost/starter_table.yaml`
  populated with day-one provider common-model entries (best-effort);
  flag `best_effort: true`.
- T003 — Implement operator-override merge from
  `~/.config/kaneaz/cost-table.yaml` (or platform equivalent —
  XDG-style on Linux, `Library/Application Support` on macOS,
  `%APPDATA%` on Windows). Override entries replace starter entries
  by exact `(kind, model_glob)` key.
- T004 — Implement `Reducer.Derive(usage, kind, model) Cost`. If no
  matching entry, set `Cost.Indeterminate = true` and continue —
  request must NOT fail (US3 Acceptance 1 must still pass without a
  cost number).
- T005 — Tests: starter-table happy path for each day-one provider,
  operator override merge, glob match precedence, missing-model
  produces `Indeterminate=true` (no error), cached-token cost
  arithmetic, currency preserved through merge.

## Acceptance criteria

- `go test ./core/llm/cost/...` passes; coverage ≥ 80 %.
- A `Response` for an Anthropic Claude Sonnet model has
  `Response.Cost.Total > 0` against the starter table on a
  non-zero usage.
- A `Response` for an unknown model has `Response.Cost.Indeterminate
  == true` and the request still completes successfully (audit log
  shows `response_final`).
- Operator override file replaces starter rate; assertion via
  test using a temp HOME dir.
- `llm/response_final` event payload includes `cost` field
  (NFR-012).

## Files to create / modify

- `core/llm/cost/loader.go`
- `core/llm/cost/reducer.go`
- `core/llm/cost/starter_table.yaml`
- `core/llm/cost/loader_test.go`
- `core/llm/cost/reducer_test.go`
- `core/llm/registry/registry.go` (wire reducer between adapter
  return and audit emit)

## Definition of done

- All subtasks complete; tests green; lint clean.
- Starter table reviewed for accuracy at PR time; `best_effort: true`
  is set.
- PR merged.
