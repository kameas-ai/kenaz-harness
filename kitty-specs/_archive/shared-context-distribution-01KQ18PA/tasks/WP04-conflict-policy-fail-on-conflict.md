---
work_package_id: "WP04"
title: "Conflict detection, fail-on-conflict mode, and size/fail-closed policies"
dependencies:
  - "WP03"
planning_base_branch: "main"
merge_target_branch: "main"
branch_strategy: "Planning artifacts were generated on main; completed changes must merge back into main."
subtasks:
  - "T001"
  - "T002"
  - "T003"
  - "T004"
phase: "Phase 4 - Conflict policy"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP04 – Conflict, size, and fail-closed policies

## Goal

Build `core/context/policy/` and the strict `fail-on-conflict` mode on top of the merger. This WP delivers the policy struct that `ResolveRequest` carries, the typed `ConflictReport` returned in strict mode, the size-budget trim policy (NFR-002 default 256 KB), and the `fail-closed` posture for required layer verification failures.

## Spec references

- FR-008 (Configurable conflict policy: override-by-precedence vs fail-on-conflict)
- NFR-002 (Per-layer size budget; default 256 KB; trim policy)
- Edge case: "personal contradicts org with fail-on-conflict policy → resolution fails with structured conflict report"
- Edge case: "personal layer exceeds size budget → harness warns, content beyond budget not injected"
- C-005 (Append-only: conflict events emit through audit not through mutation)

## Plan references

- §4.7 (Policy table: conflict, size budget, verification failure knobs)
- §3 (`ResolutionPolicy` field on `ResolveRequest`)
- Risk R5 (fail-closed posture for required layers)
- Open Question §9.2 (strict mode opt-in)

## Subtasks

- T001 Define `core/context/policy/` types: `ResolutionPolicy{ConflictMode, SizeBudget, VerificationPosture}` with sensible defaults.
- T002 Wire `fail-on-conflict` mode: when two layers define an entry with the same name, the merger returns a typed `ErrConflict` carrying the populated `[]ConflictReport` and refuses to produce a snapshot.
- T003 Implement size-budget enforcement: per-layer ceiling, default 256 KB; trim policy `keep-by-name-order-then-warn` for soft mode; hard-fail option for strict operators. Trimmed entries are recorded in the snapshot warning channel.
- T004 Implement `fail-closed` posture: when a *required* layer's verification fails (signal from WP05), resolution returns a typed error rather than silently dropping the layer. Optional layers may be skipped with a warning.

## Acceptance criteria

- Strict-mode test: org and personal define same entry, mode=fail-on-conflict → `ErrConflict` with both sides identified.
- Default-mode test: same fixture, mode=override-by-precedence → resolution succeeds, override recorded.
- Size-budget test: oversize layer is trimmed in soft mode (with warning), errors in hard mode.
- Fail-closed test: simulated verification failure on required org pack halts resolution; same failure on optional pack proceeds with warning.
- All policy knobs are settable per `ResolveRequest` (not global state).

## Files to create/modify

- `core/context/policy/policy.go`
- `core/context/policy/types.go`
- `core/context/policy/policy_test.go`
- `core/context/layer/merge.go` (extend with policy hook)

## Definition of done

- Tests exercise both default and strict modes.
- No global state for policy — every knob travels on `ResolveRequest`.
- Size-budget warnings carry enough metadata to populate audit events (WP10).
- WP merged to main via squash-merge PR.
