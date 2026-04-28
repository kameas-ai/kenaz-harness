---
work_package_id: "WP13"
title: "harness policy explain surface and policy diffing"
dependencies:
  - "WP01"
  - "WP04"
  - "WP12"
planning_base_branch: "main"
merge_target_branch: "main"
branch_strategy: "Planning artifacts were generated on main; completed changes must merge back into main."
subtasks:
  - "T001"
  - "T002"
  - "T003"
  - "T004"
phase: "Phase 13 - harness policy explain + policy diffing"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP13 – harness policy explain surface and policy diffing

## Goal

Build the `core/policy/explain/` package and the `harness policy
explain <action>` command surface so operators can move from "blocked,
mysterious" to "blocked, here's why and what to do." Add the policy-
diff surface (FR-018) so an operator can review how a new policy
version differs from the current before accepting an update.

## Spec references

- FR-015 (policy explanation surface).
- FR-018 (policy diffing).
- User Story 7 (denial reports with remediation hints).
- SC-007 (operators reach a "blocked but explained" experience for
  any disallowed action — never silent failure).
- Research D8 (`harness policy explain` is the SCP-shop ticket-killer).

## Plan references

- Plan §2 — `core/policy/explain/`.
- Plan §3 `PolicyEngine.Explain(ctx, action) (Explanation, error)`.
- Plan §7 v1.0 — minimum-viable explain surface + diffing follow-up
  in v1.x; this WP delivers v1 explain and a basic diff.

## Subtasks

- T001: Implement `engine.Explain(ctx, action)` to return an
  `Explanation` carrying:
  - The `Decision` itself (allow / deny + reason).
  - The matched clause's `(policy_id, clause_id, source_layer)`
    provenance.
  - A list of clauses that **would have** matched at parent layers
    but were narrowed away (so operators understand how org / team /
    personal layered).
  - The control kind's `RemediationHint()` (added in WP12).
  - The `inputs_summary` (redacted-aware).
- T002: Build a CLI surface under `cmd/harness/policy_explain.go` (or
  wherever the harness CLI lives) calling `engine.Explain` and
  rendering the `Explanation` as human-readable text plus a `--json`
  mode for tooling. Match the shape User Story 7 promises: policy id,
  clause id, evaluated input, remediation hint.
- T003: Build `core/policy/explain/diff.go` exposing `Diff(prev, next
  EffectivePolicy) PolicyDiff`. The diff identifies clauses
  added/removed/modified per `(layer, kind)` group with stable
  ordering. Add `cmd/harness/policy_diff.go` to surface it. The diff
  is purely a view — it does not block updates.
- T004: Tests:
  - Spec User Story 7 acceptance scenario passes via the explain
    command output (contains policy id, clause id, evaluated input,
    remediation hint).
  - Diff between two policy versions correctly identifies a removed
    provider, an added MCP server, a tightened cost ceiling.
  - JSON-mode output is stable byte-for-byte across runs (golden
    file).
  - Fuzz test: `engine.Explain(action)` is total — never panics —
    even on actions whose Kind has no registered evaluator
    (returns a typed `Explanation{Outcome: Deny, ReasonCode:
    ReasonPolicyUnavailable, ...}`).

## Acceptance criteria

- `harness policy explain "<action-kind>" --inputs ...` returns the
  expected fields for both allow and deny outcomes.
- The explain output identifies parent-layer clauses that were
  narrowed away (so operators can request a team admin to add a
  provider, per User Story 7's remediation idea).
- `harness policy diff <prev-version> <next-version>` shows
  added/removed/modified clauses grouped by layer + kind.
- JSON-mode output is byte-stable.

## Files to create/modify

- Create `core/policy/explain/{explain.go, diff.go,
  remediation.go}` + tests.
- Create `cmd/harness/policy_explain.go`,
  `cmd/harness/policy_diff.go` (or use the existing CLI entry
  point — add subcommands rather than a parallel binary).
- Modify `core/policy/engine/engine.go` to implement `Explain`
  fully (was a stub from WP01).

## Definition of done

- Acceptance criteria pass.
- Charter quality gates clean.
- The CLI is wired into the harness's existing command tree, not a
  separate binary (DIRECTIVE_001).
- Conventional-commit message; commit attributed per DIRECTIVE_029.
