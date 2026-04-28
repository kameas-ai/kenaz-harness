---
work_package_id: "WP04"
title: "Strict-narrowing validator and org/team/personal layer merge"
dependencies:
  - "WP02"
planning_base_branch: "main"
merge_target_branch: "main"
branch_strategy: "Planning artifacts were generated on main; completed changes must merge back into main."
subtasks:
  - "T001"
  - "T002"
  - "T003"
  - "T004"
  - "T005"
  - "T006"
phase: "Phase 4 - Strict-narrowing validator + per-kind NarrowingMerge"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP04 – Strict-narrowing validator and org/team/personal layer merge

## Goal

Build the airtight `core/policy/layer/` package that merges org → team →
personal `PolicyArtifact`s into a single `EffectivePolicy`, runs the
strict-narrowing validator, and emits typed `policy_validator_finding`
records for every broadening attempt, schema mismatch, wildcard warning,
and unreachable clause. This WP is the SOC 2-defending core of the
engine: NFR-004 mandates 100 % rejection of broadening across every kind
and every adversarial pair the test suite throws at it.

**This validator must be airtight.** It is the load-bearing soundness
property of the entire engine. Property-based and adversarial-fuzz
testing is mandatory, not optional, on this WP.

## Spec references

- FR-001 (layered policy composition — strict narrowing).
- FR-010 (validator at policy build + load time).
- NFR-004 (layer-narrowing soundness — 100 % rejection across the test
  matrix).
- C-006 (strict narrowing — team / personal layers cannot broaden parent
  layers).
- C-005 (SOC 2 readiness — validator findings produce audit evidence).
- SC-003 (100 % of attempted layer broadenings are rejected by the
  validator).

## Plan references

- Plan §2 — `core/policy/layer/`, `layer/validator/`.
- Plan §3 `ControlKind.NarrowingMerge(parent, child Clause) (Clause,
  error)`.
- Plan §4 strict-narrowing validator contract — set / tier / numeric /
  boolean kinds, plus silence semantics (parent silent → child applies).
- Plan §5 `EffectivePolicy` (provenance per clause: `source_layer`,
  `source_policy_id`).
- Plan §8 R2 (the risk this WP retires).

## Subtasks

- T001: Define `core/policy/layer/effective.go` — the `EffectivePolicy`
  struct (clauses with provenance, validator findings,
  `effective_id`, `computed_at`, `content_hash`) per data-model.md.
- T002: Define `core/policy/layer/finding.go` — typed `Finding` with a
  closed code set (`team_would_broaden_org`,
  `personal_would_broaden_team`, `currency_mismatch`,
  `wildcard_warning`, `unreachable_clause`, `schema_mismatch`,
  `unknown_kind`) plus message, offending clause id, source policy id,
  severity (`error`, `warn`).
- T003: Implement `core/policy/layer/merge.go` exposing `Merge(layers
  []PolicyArtifact) (EffectivePolicy, []Finding)`. For each
  `(kind, scope)` group across layers, call `ControlKind.NarrowingMerge`
  for parent + child pairs in `org → team → personal` order. Silence
  semantics: a parent silent on a kind is treated as "no constraint";
  child declarations apply unmodified.
- T004: Implement the four canonical `NarrowingMerge` semantic classes
  as shared helpers under `core/policy/layer/semantics/`:
  - `SetIntersect` — child set MUST be subset of parent set (used by
    provider, model, mcp_server, mcp_capability, a2a_peer).
  - `TierAtLeast` — child tier MUST be ≥ stricter than parent
    (network_tier `loopback < lan < wan`; redaction_strictness
    `lenient < standard < strict`).
  - `NumericAtMost` — child numeric MUST be ≤ parent (cost_ceiling).
  - `BoolStricterWins` — child may turn ON when parent is OFF; child
    MUST NOT turn OFF when parent is ON (signature_required,
    sandbox_required).
  Per-kind packages (WPs 06–11) call into these helpers from
  `NarrowingMerge`.
- T005: Build the test surface that defends NFR-004 = 100 %:
  - Table-driven unit tests covering every `(parent, child)` pair from
    a hand-curated matrix per semantic class (broadening, narrowing,
    equal, parent-silent, child-silent, wildcard).
  - Property-based tests using a generator over each semantic class:
    for any random `(parent, child)`, assert that
    `merge` either rejects with the typed broadening finding OR returns
    a child that is provably ≤ parent under the kind's order. The
    property is the soundness theorem. Use `pgregory.net/rapid` or
    Go fuzz tests.
  - Adversarial fuzz: a `FuzzNarrowing` corpus seeded with edge cases
    (empty sets, wildcards, mixed currencies, off-by-one tier strings,
    numeric overflow, NaN-ish edge cases, duplicate clauses). Failure
    of the soundness theorem is a hard test failure.
  - Edge-case "stricter wins on tie" test for cost_ceiling per spec
    edge case.
- T006: Wire `engine.Reload` (from WP02–WP03) to call `layer.Merge`
  before `lower.Compile`. If any error-severity finding is present,
  Reload returns a typed error WITHOUT activating the new policy; the
  prior `EffectivePolicy` snapshot continues to apply (plan §8 R6).
  Warn-severity findings (e.g., wildcard) do not block activation but
  are surfaced.

## Acceptance criteria

- For every semantic class (set, tier, numeric, boolean), the
  property-based test passes 1000+ iterations with zero soundness
  violations. CI runs at least 200 iterations per push.
- The fuzz corpus includes minimum 20 hand-curated adversarial cases
  (wildcards, empty parents, currency mismatch, tier strings,
  near-zero numerics, etc.); `go test -fuzz` runs to seed coverage in
  CI for at least 30 seconds.
- Spec acceptance scenarios pass as table-driven cases:
  - User Story 2 scenario 1: parent {A,B,C} + child {A,B} → effective
    {A,B}.
  - User Story 2 scenario 2: parent {A,B} + child {A,B,D} → rejected
    with `team_would_broaden_org` citing clause id.
  - User Story 2 scenario 3: parent silent + child declares → child
    applies.
  - User Story 3 scenario 1: team {A,B} + personal excludes B → only A
    available.
  - User Story 3 scenario 2: team denies + personal permits → rejected
    with `personal_would_broaden_team`.
  - Edge case "conflicting cost ceilings": stricter (smaller) wins,
    tie-break by `(layer, version)` tuple recorded in the merge result.
- The "broadening" check is meaningful only when the parent has
  expressed a constraint (silence semantics asserted explicitly).
- `EffectivePolicy` carries `source_layer` and `source_policy_id` for
  every retained clause; provenance survives a round-trip through
  marshal/unmarshal.
- `engine.Reload` does not activate a policy that has any
  error-severity finding; the prior snapshot is preserved (test asserts
  the active snapshot pointer is unchanged).

## Files to create/modify

- Create `core/policy/layer/effective.go`,
  `core/policy/layer/finding.go`, `core/policy/layer/merge.go`.
- Create `core/policy/layer/semantics/sets.go`,
  `semantics/tiers.go`, `semantics/numbers.go`, `semantics/bools.go`,
  with unit tests per file.
- Create `core/policy/layer/merge_test.go` (table-driven scenario tests).
- Create `core/policy/layer/property_test.go` (rapid-based property
  tests).
- Create `core/policy/layer/fuzz_test.go` (Go fuzz harness +
  `testdata/fuzz/FuzzNarrowing/...` seed corpus).
- Modify `core/policy/engine/engine.go` to call `layer.Merge` during
  `Reload` and to honor the no-activation-on-error contract.

## Definition of done

- Acceptance criteria pass; property + fuzz tests are in CI.
- A test report attached to the PR enumerates every semantic class
  exercised and the iteration count per class — this is the artifact
  that defends NFR-004 in audit.
- Charter quality gates clean.
- A short ADR records the silence-semantics rule and the tie-break
  policy for numeric ceilings (DIRECTIVE_003).
- Conventional-commit message; commit attributed per DIRECTIVE_029.
- No raw-Rego clause is silently merged: per-kind merge for `raw_rego`
  is "child rejected at narrowing time unless parent is silent on
  raw_rego" — documented and tested.
