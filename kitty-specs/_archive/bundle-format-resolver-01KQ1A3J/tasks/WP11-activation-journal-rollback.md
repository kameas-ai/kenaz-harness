---
work_package_id: "WP11"
title: "Activation pipeline and ActivationJournal rollback"
dependencies:
  - "WP04"
  - "WP09"
  - "WP10"
planning_base_branch: "main"
merge_target_branch: "main"
branch_strategy: "Planning artifacts were generated on main; completed changes must merge back into main."
subtasks:
  - "T001"
  - "T002"
  - "T003"
  - "T004"
phase: "Phase 11 - Activation"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP11 – Activation pipeline and ActivationJournal rollback

## Goal

Implement the activation phase: dispatch each verified artifact in `ActivationOrder` to its registered `ArtifactKindHandler`, record every commitment in an `ActivationJournal`, and roll back via `Deactivate` in reverse on cancellation or panic. Activation is the only step that mutates harness state outside `core/bundle/`.

## Spec references

- FR-002 Artifact-kind dispatch
- FR-015 Cancellation safety (no partial activation; rollback to last-known-good)
- FR-017 Bundle removal (uses `Deactivate` paths)
- US3 (P1) — partial state never visible after a failure
- C-001 Architectural integrity

## Plan references

- Plan §3.2 Resolver.Activate / Remove signatures
- Plan §4.4 Activation phase (dispatch via kinds.Registry)
- Plan §4.7 Cancellation safety (ActivationJournal records every commitment with a recovery hook)
- Plan §8 R8 (panic in handler `Activate` skips Deactivate — defer + recover discipline)

## Subtasks

- T001 Define `ActivationJournal`: append-only in-memory record of `{ArtifactRef, Activation, completedAt}`; thread-safe; supports `Reverse() iter` for rollback.
- T002 Implement `Resolver.Activate(ctx, g)`: iterate `g.ActivationOrder`, look up the kind handler in `kinds.Registry`, call `handler.Parse → Validate → Activate`, append to journal on success; on `ctx.Done()` or any handler error, stop and roll back.
- T003 Rollback: walk the journal in reverse, call `handler.Deactivate(ctx, activation)` for each completed activation; surface aggregated rollback errors as wrapped causes; emit `artifact_deactivated` events per step.
- T004 Panic safety: every handler call is wrapped in `defer recover()`; a panic is converted to an error and triggers rollback. R8 mitigation.
- T005 Implement `Resolver.Remove(ctx, ref)`: find all activations belonging to that bundle in the current `ActivationJournal` (or persisted last-known-good snapshot) and call `Deactivate` on each; remove the bundle's lockfile entry; cache contents survive (FR-011).
- T006 Last-known-good snapshot: persist the most recent successful `ResolvedGraph` content hash so a `Cancel` mid-run can return to the prior steady state without re-resolving.

## Acceptance criteria

- An activation that fails mid-way leaves the harness in the prior steady state — no partially-activated artifacts survive (FR-015).
- A handler panic is recovered, classified as `ErrActivationFailed`, and triggers the same rollback path as a returned error.
- `Cancel` (via `ctx.Done`) interrupts activation and rolls back; the lockfile is untouched.
- `Remove(ref)` deactivates every artifact of a bundle and removes its lockfile entry; CAS contents are preserved.
- The `noop` test handler (from WP04) integrates end-to-end through Activate → Deactivate.

## Files to create/modify

- `core/bundle/resolver/activate.go` (new — activation orchestrator)
- `core/bundle/resolver/journal.go` (new — ActivationJournal)
- `core/bundle/resolver/cancel.go` (new — context wiring + rollback)
- `core/bundle/resolver/remove.go` (new — Resolver.Remove impl)
- `core/bundle/errors.go` (extend — `ErrActivationFailed`)

## Definition of done

- All acceptance criteria pass.
- Integration test: a fixture handler that panics on activation triggers full rollback; assertions confirm no partial state.
- Cancellation test: `ctx.Cancel()` mid-activation rolls back cleanly; lockfile bytes unchanged on disk.
- `Remove` test: bundle removal preserves CAS contents and cleanly tears down activations.
- No new imports outside `kinds`, `lockfile`, `cache`, `events`, `errors`.
