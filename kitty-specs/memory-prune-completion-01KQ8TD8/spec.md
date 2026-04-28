# Spec: Memory prune sweep completion

**Status**: draft · **Owner**: alecfeeman

## 1. Why

`core/memory/prune/` exists with pruner + scheduler + metrics, but the wiring into the runtime + the user-facing surface is incomplete. Without it the greedy raw-memory store grows unbounded. The narrative-layer mission depends on prune to keep the raw layer manageable.

## 2. Goals

- Schedule the prune sweep on a configurable interval (default daily at 03:00 local).
- Surface pruner output (what was evicted, why) in the Memory view.
- Per-rule configuration: age-cap, dedup, low-recall.
- User can inspect a "prune preview" before any eviction takes effect.
- **Never delete artifacts** (per `built-in-save-artifact` constraint).

## 3. Functional requirements

| ID | Requirement | Status |
|---|---|---|
| FR-001 | Scheduler triggers prune at the configured cadence; missed runs catch up on next launch. | proposed |
| FR-002 | New Settings: `MemoryPruneSchedule`, `MemoryPruneAgeCapDays`, `MemoryPruneLowRecallThreshold`. | proposed |
| FR-003 | "Prune preview" RPC returns candidates without committing; UI surfaces them in a confirm-able list. | proposed |
| FR-004 | The default schedule is preview-only (user must approve evictions) for the first 30 days; after 30 days of non-rejection, auto-commits. | proposed |
| FR-005 | Pruner skips any chunk with `metadata.kind == "narrative"`, `scope_kind == "long_term"`, or originating from `kaneaz__save_artifact`. | proposed |
| FR-006 | Memory view gains a "prune log" tab listing the last N prune runs with eviction counts + reasons. | proposed |

## 4. Success criteria

- Memory store size stabilises after first prune cycle on a long-running session.
- Zero false-positive evictions of narrative or artifact rows in test fixtures.
