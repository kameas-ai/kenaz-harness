---
work_package_id: "WP06"
title: "provider_allowlist and model_allowlist control kinds"
dependencies:
  - "WP02"
  - "WP03"
  - "WP04"
planning_base_branch: "main"
merge_target_branch: "main"
branch_strategy: "Planning artifacts were generated on main; completed changes must merge back into main."
subtasks:
  - "T001"
  - "T002"
  - "T003"
  - "T004"
phase: "Phase 6 - provider_allowlist + model_allowlist"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP06 – provider_allowlist and model_allowlist control kinds

## Goal

Land the first two production control kinds — `provider_allowlist` and
`model_allowlist` — in their own packages under `core/policy/clauses/`,
each implementing the WP02 `ControlKind` contract. Each kind brings its
param schema, Rego lowering, narrowing-merge semantics (set
intersection from WP04), denial reason mapping, and a consumer-facing
adapter that the `llm-connector-01KQ1770` mission calls.

## Cross-mission dependency

- **llm-connector-01KQ1770** (consumer): provider activation and model
  selection paths call `policy.Engine.Evaluate` with `Action.Kind() ==
  "llm.provider.activate"` and `"llm.model.select"` respectively. If
  the connector is not yet exposing these hook points, this WP adds
  the policy-side `Action` shape and a stub adapter under
  `core/policy/clauses/.../adapter.go` so the connector mission can
  wire it later without surgery on this WP.

## Spec references

- FR-004 (control catalog v1 — provider + model allowlists).
- FR-005 (extensibility — kinds in their own packages).
- FR-006 (Evaluate API).
- FR-008 (denial taxonomy — `ReasonNotInAllowlist`).
- NFR-006 (control-catalog parity — each kind enforced by at least one
  consumer).
- User Story 1 (org publishes provider allowlist).

## Plan references

- Plan §2 — `clauses/provider_allowlist/`,
  `clauses/model_allowlist/`.
- Plan §3 `ControlKind` interface implementations.
- Plan §4 strict-narrowing — set-intersection semantics from WP04.
- Plan §6 — consumer row for `llm-connector-01KQ1770`.

## Subtasks

- T001: Create `core/policy/clauses/provider_allowlist/`. Define
  `params: { allow: [provider_id...] }`. Lower to a Rego module that
  matches `Action.Kind() == "llm.provider.activate"` against the
  allowlist. Wire `NarrowingMerge` to `semantics.SetIntersect`. Default
  `FailurePostureDefault: FailClosed`.
- T002: Create `core/policy/clauses/model_allowlist/`. Define
  `params: { allow: { provider_id: [model_id...] } }` (per-provider
  map). Lowering matches `Action.Kind() == "llm.model.select"` against
  the per-provider list. Narrowing is a per-provider set-intersect
  (helper: `semantics.MapOfSetIntersect` added under
  `core/policy/layer/semantics/`).
- T003: Per-kind tests:
  - Schema rejects malformed `params`.
  - Lowering produces deterministic Rego (golden file).
  - `NarrowingMerge` rejects broadening (table-driven, including
    silence semantics).
  - End-to-end: load a YAML policy, evaluate an `llm.provider.activate`
    action with `inputs.provider_id == "openai"` against an allowlist
    that omits OpenAI → `Decision{Outcome: Deny, ReasonCode:
    ReasonNotInAllowlist, ClauseID: ...}`.
- T004: Register both kinds via `init()` in their packages. Update
  `core/policy/registry_test.go` to assert these kinds appear in
  `RegisteredKinds()`. Add a consumer-facing adapter under each
  package (`adapter.go`) that exposes a typed helper for the LLM
  connector to construct `Action` values without reaching into
  internals (FR-006 ergonomics).

## Acceptance criteria

- Both kinds register at process start; `RegisteredKinds()` includes
  them.
- Spec User Story 1 acceptance scenario 1 passes end-to-end: org policy
  permits {Anthropic, OpenRouter}, employee bundle declares OpenAI →
  Evaluate returns `Deny` with `ReasonNotInAllowlist`, citing the
  policy id and clause id (FR-008).
- Narrowing scenarios from WP04 include provider + model kinds in the
  semantic-class matrix.
- Lowering golden files are byte-stable across runs.
- Consumer adapter exposes a clear API the LLM connector can call;
  unit tests use the adapter rather than constructing `Action` types
  by hand.

## Files to create/modify

- Create `core/policy/clauses/provider_allowlist/{kind.go, schema.go,
  lower.go, merge.go, adapter.go}` plus `*_test.go` files.
- Create `core/policy/clauses/model_allowlist/{kind.go, schema.go,
  lower.go, merge.go, adapter.go}` plus `*_test.go` files.
- Create `core/policy/layer/semantics/map_of_sets.go` +
  `map_of_sets_test.go`.
- Create golden Rego output fixtures under each package's
  `testdata/golden/`.
- Modify `core/policy/registry_test.go` to assert presence in the
  catalog.

## Definition of done

- Acceptance criteria pass.
- Charter quality gates clean.
- Cross-mission dependency on `llm-connector-01KQ1770` is documented
  in the PR body with the action-kind names this WP exports.
- Conventional-commit message; commit attributed per DIRECTIVE_029.
