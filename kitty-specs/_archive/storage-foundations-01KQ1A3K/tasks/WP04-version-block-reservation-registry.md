---
work_package_id: "WP04"
title: "Migration version-block reservation registry"
dependencies:
  - "WP03"
planning_base_branch: "feat/storage-foundations-01KQ1A3K"
merge_target_branch: "main"
branch_strategy: "Planning artifacts were generated on feat/storage-foundations-01KQ1A3K; completed changes must merge back into main via squash merge per charter Branch Strategy."
subtasks:
  - "T001: encode the version-block table from plan §6.3 in code"
  - "T002: validate Register() against owning-mission's reserved range"
  - "T003: emit deterministic ordering (alphabetical mission, then version) at composition root"
  - "T004: documentation note in tasks/README and core/storage/migrations/doc.go"
  - "T005: tests for collision rejection and out-of-range refusal"
phase: "Phase 4 - Version-block reservation"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP04 – Migration version-block reservation registry

## Goal

Encode the cross-mission version-block reservation table (plan §6.3) inside the migration framework so consuming missions cannot collide on version numbers. The framework rejects `Register` calls whose version falls outside the owning mission's reserved range and emits a deterministic registration order at the composition root.

## Spec references

- FR-003 (migration framework)
- C-001 (architectural integrity — clear contracts between missions)
- C-006 (vector backend / consumer extensibility — adding a new consumer must not break others)
- US 3 (versioned migrations)

## Plan references

- §6.3 Consuming missions register migrations — version-block reservation table
- §3 Public API (Migration.OwningMission, Migration.Version)
- §4.4 Migration Engine

## Subtasks

1. Add `core/storage/migrations/blocks.go` declaring the canonical map: `storage`->[1,99], `event-log`->[100,199], `secrets-keychain`->[200,299], `session`->[300,399], `scheduler`->[400,499], `mcp`->[500,599], `a2a`+`signed-cards-trust`->[600,699], `bundle`+`shared-context-distribution`->[700,799], `memory-rag`->[800,899], `app-layer`->[900,999], `reserved-future`->[1000,...]. Ranges are inclusive integer pairs.
2. Modify `MigrationRegistry.Register` to validate `Migration.Version` falls inside `blocks[m.OwningMission]`. Out-of-range -> `ErrVersionOutOfBlock` (new sentinel) carrying the mission and expected range.
3. Modify `MigrationRegistry.Register` to reject duplicate `(Version)` and duplicate `(ID)` across all owning missions; collisions -> `ErrVersionCollision` / `ErrMigrationIDCollision`.
4. At composition-root sorting time (used inside `Apply`), enforce deterministic ordering: ascending `Version` (already required); within a single boot, the order callers register migrations is preserved when versions tie (which they must not, but assert anyway).
5. Document the table and how to claim a new range in `core/storage/migrations/doc.go` and add a paragraph to `kitty-specs/storage-foundations-01KQ1A3K/tasks/README.md` so future mission planners discover it.
6. Tests: registering a migration with a version outside the owning mission's block fails; registering a migration with a duplicate version (within or across missions) fails; registering `event-log/0001-init` at version 100 succeeds; registering `event-log` at version 50 fails with `ErrVersionOutOfBlock` and a message naming `[100,199]`.

## Acceptance criteria

- Attempting to register `{OwningMission: "event-log", Version: 50}` returns `ErrVersionOutOfBlock`.
- Attempting to register two migrations at the same `Version` returns `ErrVersionCollision`.
- The block table is the single source of truth: it is referenced from `core/storage/migrations/doc.go` and `tasks/README.md` so future mission authors see it.
- All previously-passing migration tests continue to pass.

## Files to create/modify

- Create: `core/storage/migrations/blocks.go`
- Create: `core/storage/migrations/doc.go`
- Modify: `core/storage/migrations/registry.go` (Register validation)
- Modify: `core/storage/storage.go` (new sentinel errors)
- Modify: `kitty-specs/storage-foundations-01KQ1A3K/tasks/README.md` (link to the table)
- Create: `core/storage/migrations/blocks_test.go`

## Definition of done

- Block table encoded; `Register` enforces it.
- Documentation visible to consuming missions in two places (code + task README).
- Tests prove out-of-block, collision, and duplicate-ID refusals.
