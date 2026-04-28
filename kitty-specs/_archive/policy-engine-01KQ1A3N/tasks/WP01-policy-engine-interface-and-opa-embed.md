---
work_package_id: "WP01"
title: "PolicyEngine interface and embedded OPA evaluator"
dependencies: []
planning_base_branch: "main"
merge_target_branch: "main"
branch_strategy: "Planning artifacts were generated on main; completed changes must merge back into main."
subtasks:
  - "T001"
  - "T002"
  - "T003"
  - "T004"
  - "T005"
phase: "Phase 1 - PolicyEngine interface + OPA in-process embed"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP01 – PolicyEngine interface and embedded OPA evaluator

## Goal

Stand up the public `core/policy` package with the `PolicyEngine` interface
(Evaluate / Explain / EffectivePolicy / Reload), the canonical
`Action` / `Decision` / `Outcome` / `ReasonCode` types, and a minimal
in-process OPA evaluator under `core/policy/engine/opa/` that compiles and
evaluates a hard-coded "always allow" Rego module end-to-end. This WP is
the foundation every later WP plugs into; it does not yet ingest YAML
clauses or layer artifacts.

## Spec references

- FR-006 (single `policy.Evaluate(ctx, action)` API).
- FR-007 (fail-closed default posture wired into the engine).
- FR-008 (closed denial-reason taxonomy).
- NFR-001 (sub-1 ms p99 evaluation; prepared evaluators on the hot path).
- NFR-002 (decision determinism — single in-process OPA instance, no
  remote calls in eval).
- C-001 (architectural integrity — `core/policy/engine/opa/` is the only
  package allowed to import OPA).
- C-004 (local-first — embedded, in-process evaluator only).

## Plan references

- Plan §2 directory layout and boundary rules (`engine/`, `engine/opa/`).
- Plan §3 Public API — `PolicyEngine`, `Action`, `Decision`, `Outcome`,
  `ReasonCode`, `EffectivePolicy` placeholder.
- Plan §4 step 6–8 (prepared evaluators per Action.Kind, hot-path flow).
- Plan §7 v1.0 phasing — embedded OPA + Rego evaluator.
- Plan §8 R7 (OPA decision-log JSON shape never wired directly to disk;
  must traverse the event-log redaction pipeline downstream).

## Subtasks

- T001: Create `core/policy/policy.go` with the `PolicyEngine` interface
  and the public types (`Action`, `Decision`, `Outcome`, `ReasonCode`
  constants from FR-008, `EffectivePolicy` placeholder struct,
  `Explanation` placeholder). No implementation logic in this file.
- T002: Create `core/policy/engine/` with an `engine` package that owns
  the runtime engine struct, prepared-evaluator cache keyed by
  `Action.Kind()`, and the snapshot pointer described in plan §4 step 7.
  This package MUST NOT import OPA directly.
- T003: Create `core/policy/engine/opa/` as the only package importing
  `github.com/open-policy-agent/opa/v1/rego`. Expose a backend
  constructor that takes a set of compiled Rego modules and returns a
  prepared-query interface the `engine` package consumes through a small
  internal interface (so a future Cedar backend per plan D2 / DIRECTIVE_001
  is a drop-in).
- T004: Wire up a smoke-test policy: a hand-written Rego module that
  always allows; verify `engine.Evaluate(ctx, action)` returns
  `Decision{Outcome: Allow}` and that an unmatched action falls through
  to the fail-closed default with `Outcome: Deny` and `ReasonCode:
  ReasonPolicyUnavailable` (or a transitional placeholder reason — the
  full taxonomy lands as later kinds populate it).
- T005: Add a benchmark exercising `Evaluate` on the smoke-test policy
  to baseline NFR-001 (`go test -bench=BenchmarkEvaluate
  ./core/policy/engine/...`). Record the p99 in the WP DoD note; the
  full hot-path budget is defended in WP14 cross-cutting tests.

## Acceptance criteria

- `core/policy/policy.go` defines `PolicyEngine`, `Action`, `Decision`,
  `Outcome`, all nine `ReasonCode` constants from FR-008, and the
  placeholder `EffectivePolicy` / `Explanation` types. No package outside
  `core/policy/engine/opa/` imports OPA (`go list -deps` gate or
  equivalent assertion in test).
- The `engine` package exposes a constructor that accepts an OPA backend
  (or a fake backend in tests) and implements all four `PolicyEngine`
  methods. `Reload` and `Explain` may stub for now but MUST be present
  with the signatures plan §3 specifies.
- An in-process Rego smoke test passes: load a constant `allow` module,
  call `Evaluate`, assert `Outcome: Allow`. A second test asserts the
  fail-closed default for an unmatched action (NFR-005 contract begins
  here; full coverage lands in WP14).
- A microbenchmark of `Evaluate` runs and records a baseline p99; the
  baseline is documented in the PR body. NFR-001 (sub-1 ms p99) is the
  defended target — if the baseline already exceeds it, raise an
  explicit risk in the PR for plan §8 R-list update.

## Files to create/modify

- Create `core/policy/policy.go` (public types + `PolicyEngine`
  interface).
- Create `core/policy/engine/engine.go` (engine struct, prepared-evaluator
  cache, snapshot pointer, fail-closed default wired into `Evaluate`).
- Create `core/policy/engine/engine_test.go` (smoke + fail-closed test).
- Create `core/policy/engine/opa/opa.go` (only OPA import site;
  prepared-query plumbing).
- Create `core/policy/engine/opa/opa_test.go` (prepared-evaluator
  construction + evaluation against a constant Rego module).
- Create `core/policy/engine/bench_test.go` (NFR-001 baseline benchmark).
- Modify `go.mod` / `go.sum` to add `github.com/open-policy-agent/opa`.

## Definition of done

- All listed acceptance criteria pass.
- `go test ./core/policy/... -race` is green.
- `gofmt`, `goimports`, `go vet`, `golangci-lint run ./core/policy/...`
  all clean (charter Quality Gates).
- The OPA-import boundary is asserted in test (a `TestOPAImportBoundary`
  that walks `core/policy` deps and fails if any package outside
  `core/policy/engine/opa` imports `github.com/open-policy-agent/opa/...`).
- Benchmark baseline recorded in the PR body. No production code outside
  `core/policy/` introduced.
- Conventional commit message; commit attributed per DIRECTIVE_029.
