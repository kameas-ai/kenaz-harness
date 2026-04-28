---
work_package_id: "WP03"
title: "YAML clause to Rego lowering pipeline"
dependencies:
  - "WP01"
  - "WP02"
planning_base_branch: "main"
merge_target_branch: "main"
branch_strategy: "Planning artifacts were generated on main; completed changes must merge back into main."
subtasks:
  - "T001"
  - "T002"
  - "T003"
  - "T004"
phase: "Phase 3 - YAML to Rego lowering pipeline"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP03 – YAML clause to Rego lowering pipeline

## Goal

Build the `core/policy/lower/` compiler that walks a validated
`PolicyArtifact`, dispatches each clause to its registered
`ControlKind.LowerToRego`, and emits a single bundle of Rego modules the
engine loads via `core/policy/engine/opa/`. Includes the raw-Rego escape
hatch (research D3) so advanced authors can drop down without us
fragmenting the engine.

## Spec references

- FR-005 (control-kind extensibility — lowering is per-kind).
- FR-006 (single Evaluate API — lowering must produce Rego shaped to
  match the `Action.Kind()` dispatch).
- NFR-001 (sub-1 ms p99 — lowering is build-time work, not hot path;
  the engine must hold prepared queries, not recompile per evaluation).
- NFR-002 (determinism — stable, ordered Rego output for identical YAML).
- C-001 (architectural integrity — only `clauses/<kind>/` packages know
  their Rego shape).

## Plan references

- Plan §2 — `core/policy/lower/`, `lower/rego_emitters/`.
- Plan §3 `ControlKind.LowerToRego(clause Clause) (string, error)`.
- Plan §4 step 4–6 (lower → load into a single OPA instance →
  prepared evaluators).
- Plan §1 + research D3 — YAML clauses compile to Rego; raw-Rego
  escape hatch (open question 9.1 in plan §9).
- Plan §8 R1 (Rego learning curve mitigation — operators stay in YAML).

## Subtasks

- T001: Implement `core/policy/lower/lower.go` exposing
  `Compile(artifacts []PolicyArtifact) (CompiledBundle, error)`. The
  compiler iterates clauses, calls `Lookup(kind).LowerToRego(clause)`,
  collects the emitted modules with stable, sorted module names, and
  returns a `CompiledBundle` carrying module-name→source map plus a
  manifest entry listing every clause's `(policy_id, clause_id, kind,
  module_name)` for traceability used by WP12 explain and WP13 logs.
- T002: Implement `core/policy/lower/rego_emitters/` shared helpers —
  e.g., a "set membership" emitter, a "tier comparison" emitter, a
  "numeric ceiling" emitter — that per-kind packages call. These
  helpers are pure functions (input → Rego string) with unit tests.
- T003: Implement the raw-Rego escape hatch: a clause whose `kind` is
  `raw_rego` carries a `params.module` string the lowering pipeline
  passes through after parse-only validation (must compile under
  `ast.ParseModule`, must declare a known package namespace prefix).
  Mark this kind explicitly as advanced; document that it bypasses
  per-kind validation and is not eligible for the FR-001 narrowing
  validator (it is treated as an opaque "deny everything not in"
  expression at narrowing time — see WP04).
- T004: Wire `engine.Reload` to call `lower.Compile`, hand the bundle to
  the OPA backend, and prepare per-`Action.Kind` queries. Verify the
  hot path no longer touches `lower` after Reload; add a benchmark
  asserting that `Evaluate` does not allocate Rego compilation work
  (NFR-001 hot-path discipline).

## Acceptance criteria

- A test policy with two clauses (one `provider_allowlist` stub kind +
  one `cost_ceiling` stub kind) lowers to two Rego modules, each
  compilable by OPA. The fake kinds may live in `lower_test.go` until
  WPs 06+ replace them.
- Module names are deterministic across runs (sorted by
  `(policy_id, clause_id)`); a golden-file test asserts byte-equal
  output for identical input.
- Raw-Rego escape hatch accepts a hand-written module that compiles and
  rejects a syntactically invalid module with a typed error citing the
  offending clause id.
- After `engine.Reload`, the prepared-evaluator cache contains one
  entry per distinct `Action.Kind` referenced by the bundle, and
  `Evaluate` does not invoke `lower.Compile` again (assertion: a test
  spy on the lowering function records zero calls during a hot loop of
  evaluations).

## Files to create/modify

- Create `core/policy/lower/lower.go` (top-level Compile).
- Create `core/policy/lower/lower_test.go` (golden output, raw-Rego
  validation, hot-path no-recompile assertion).
- Create `core/policy/lower/rego_emitters/sets.go`,
  `rego_emitters/tiers.go`, `rego_emitters/numbers.go` (shared
  emitters with unit tests).
- Create `core/policy/lower/rego_emitters/*_test.go` for each emitter.
- Create `core/policy/lower/raw.go` (raw-Rego escape-hatch handler) +
  `raw_test.go`.
- Modify `core/policy/engine/engine.go` to invoke `lower.Compile` from
  `Reload` and to populate the prepared-query cache.

## Definition of done

- Acceptance criteria pass.
- `go test ./core/policy/lower/... -race` and the engine reload tests
  green.
- Charter quality gates clean.
- A short ADR (or note in plan §9) records the raw-Rego escape-hatch
  decision per DIRECTIVE_003 — including the explicit caveat that raw
  Rego clauses are opaque to the narrowing validator (WP04).
- Conventional-commit message; commit attributed per DIRECTIVE_029.
