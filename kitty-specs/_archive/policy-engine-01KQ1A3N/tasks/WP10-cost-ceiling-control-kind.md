---
work_package_id: "WP10"
title: "cost_ceiling control kind with per-session and per-day enforcement"
dependencies:
  - "WP02"
  - "WP03"
  - "WP04"
  - "WP06"
planning_base_branch: "main"
merge_target_branch: "main"
branch_strategy: "Planning artifacts were generated on main; completed changes must merge back into main."
subtasks:
  - "T001"
  - "T002"
  - "T003"
  - "T004"
  - "T005"
phase: "Phase 10 - cost_ceiling control kind"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP10 – cost_ceiling control kind with per-session and per-day enforcement

## Goal

Land `cost_ceiling` as a control kind enforcing per-session and
per-day spend limits at the LLM-call boundary, denominated in the
policy-declared currency. Resolves spec OQ2 — per-policy currency
declaration; mixing currencies in a layered effective policy is
rejected at validation as `currency_mismatch`. The kind consumes usage
data exposed by `llm-connector-01KQ1770` FR-011 (token / usage).

## Cross-mission dependencies

- **llm-connector-01KQ1770**: provides per-call cost data via its
  FR-011 surface (token usage + pricing tables). Per-day aggregation
  is computed by the connector or by a small aggregator under
  `core/policy/clauses/cost_ceiling/aggregator.go` if the connector
  does not yet expose totals. Document the seam.
- **event-log-01KQ1A3M**: per-day totals reconstruct from event-log
  on startup (durable across restarts).

## Spec references

- FR-004 (control catalog v1 — cost ceilings per session, per day).
- FR-007 (fail-closed default: when daily total is unavailable, deny).
- FR-008 (denial taxonomy — `ReasonExceedsCeiling`,
  `ReasonPolicyUnavailable`).
- FR-012 (cost-ceiling enforcement at the LLM-call boundary, usage
  from `llm-connector` FR-011).
- User Story 6 (graceful degradation when input source unavailable).
- Spec OQ2 → plan §9 proposal: per-policy currency, mixing rejected.

## Plan references

- Plan §2 — `clauses/cost_ceiling/`.
- Plan §4 strict-narrowing — `NumericAtMost` (smaller is stricter)
  with stricter-wins tie-break by `(layer, version)` tuple.
- Plan §6 — consumer row for `llm-connector-01KQ1770`.
- Plan §9 OQ2 proposal.

## Subtasks

- T001: Schema: `params: { currency: <ISO-4217-code>, per_session:
  <decimal>, per_day: <decimal>, fail_open_when_total_unavailable:
  bool (default false) }`. Use a fixed-precision decimal type (avoid
  float). Currency is a top-level required field per data-model
  decision.
- T002: Lowering: match `Action.Kind() == "llm.call"` against
  `inputs.session_cost_so_far + inputs.this_call_estimate` ≤
  `per_session`, and `inputs.day_cost_so_far +
  inputs.this_call_estimate` ≤ `per_day`. Deny with
  `ReasonExceedsCeiling` and a structured input summary that records
  the three values (redacted-aware: see WP12 redaction plumbing).
- T003: Narrowing: `NumericAtMost` per `(currency, scope)` group.
  **Currency-mismatch rule** (resolves OQ2): if the parent declares
  `currency: USD` and child declares `currency: EUR` for the same
  scope, the validator emits a `currency_mismatch` error finding and
  rejects activation. Test exhaustively in the WP04 narrowing matrix.
- T004: Fail-closed posture (FR-007, NFR-005): if
  `inputs.day_cost_so_far` is missing/unknown and the clause does
  NOT have `fail_open_when_total_unavailable: true`, deny with
  `ReasonPolicyUnavailable` and emit
  `policy_unavailable_fail_closed`. If fail-open is set, allow and
  emit `policy_unavailable_fail_open` (loud, alertable per User
  Story 6).
- T005: Per-day aggregator: optional
  `core/policy/clauses/cost_ceiling/aggregator.go` that subscribes to
  the event log and rebuilds the running daily total. Document
  precedence: if the LLM connector provides totals natively
  (FR-011), the aggregator is bypassed; otherwise the aggregator
  becomes the source of truth.

## Acceptance criteria

- Per-session ceiling: a session with $4.50 spent and a $1.00 estimated
  call against a $5/session policy is denied with
  `ReasonExceedsCeiling`.
- Per-day ceiling: similar at the daily granularity.
- Currency-mismatch finding rejects layered policies with mismatched
  currencies (ADR landed for OQ2 resolution).
- Fail-closed posture is the default; unavailable daily total
  produces a denial + `policy_unavailable_fail_closed` event.
- Fail-open opt-in works as documented and emits the louder
  `policy_unavailable_fail_open` event.
- Narrowing test covers stricter-wins tie-break for equal values
  across layers (data-model + plan §4 spec edge case).

## Files to create/modify

- Create `core/policy/clauses/cost_ceiling/{kind.go, schema.go,
  lower.go, merge.go, adapter.go, aggregator.go}` + `*_test.go`.
- Create `docs/adr/<n>-cost-ceiling-currency-model.md` resolving OQ2.
- Modify `core/policy/registry_test.go`.

## Definition of done

- Acceptance criteria pass.
- Charter quality gates clean.
- ADR for OQ2 resolution landed.
- Cross-mission dependency on `llm-connector-01KQ1770` (FR-011
  usage surface) and `event-log-01KQ1A3M` documented in PR body.
- Conventional-commit message; commit attributed per DIRECTIVE_029.
