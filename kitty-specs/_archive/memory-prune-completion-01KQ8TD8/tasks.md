# tasks.md — Memory prune completion (`memory-prune-completion-01KQ8TD8`)

> 9 work packages. Sequencing diagram at the bottom.

## WP01 — Soft-delete schema + audit kinds

**Effort**: S
**Deps**: none
**Files touched**:

- `core/memory/chunk.go` — add `DeletedAt`, `DeleteReason` fields; `IsDeleted` helper; `MemoryDeleteReason` constants.
- `core/memory/store.go` — `backfillChunkDefaults` defaults new fields to zero; new methods `SoftDelete(ctx, id, reason) error` + `Restore(ctx, id) error` on `chromemStore`; new `IncludeDeleted` field on `ScopeFilter`; `List` / `Query` filter out deleted by default.
- `core/memory/store_test.go` — round-trip soft-delete / restore; legacy gob loads with zero values; `List` excludes deleted.
- `core/context/audit/audit.go` — four new `Kind` constants + payload structs (`MemoryAutoPrunedPayload`, `MemoryDeletedPayload`, `MemoryRestoredPayload`, `MemoryPromotedOnSessionDeletePayload`).

**Acceptance**:
- Legacy gob (no `DeletedAt`/`DeleteReason`) loads without error; rows surface as live.
- `SoftDelete` then `List` returns the row excluded; passing `IncludeDeleted=true` includes it.
- `Restore` clears the fields atomically (one gob save).
- `go vet ./core/memory/... ./core/context/audit/...` passes.
- New audit kinds are exported and have payload structs matching the spec.

## WP02 — Auto-prune cycle (`RunCycle`)

**Effort**: M
**Deps**: WP01
**Files touched**:

- `core/memory/prune/cycle.go` (NEW) — `RunCycle`, `CycleOpts`, `Report`, `VerdictRow`.
- `core/memory/prune/cycle_test.go` (NEW) — fixtures for 6000-row scenario; greedy tie-break; skip-kinds enforcement; pinned-skip; idempotency window; below-threshold short-circuit.
- `core/memory/prune/doc.go` — document `RunCycle` vs legacy `Pruner.Apply`; future-work note for hard-delete compaction.
- Sidecar persistence: `core/memory/prune/cycles_journal.go` (NEW) — atomic JSON read/write of last 30 reports.

**Acceptance**:
- 6000 rows / mixed ages → exactly bottom-decile of those >180d old soft-deleted; no narrative/long_term/pinned ever in `Report.Cycle`.
- Idempotency: second `RunCycle` within 24h returns `SkippedReason="ran_recently"`, no writes.
- Tie-break test: rows with score equal to percentile boundary are KEPT.
- Below `CountThreshold`: no candidates evaluated, `SkippedReason="below_count_threshold"`.

## WP03 — Background scheduler wiring

**Effort**: S
**Deps**: WP02
**Files touched**:

- `core/memory/prune/scheduler.go` — minor: add `WithCycleMode(opts CycleOpts)` option so `RunOnce` can call `RunCycle` instead of `Apply`. Existing 24h ticker + on-startup catch-up logic kept.
- `core/app/boot.go` (or actual rpc-layer wiring file — verify) — read settings dials + env flag at boot; construct scheduler with `CycleOpts` derived from settings; wire `OnSweep` to emit `KindMemoryAutoPruned` and append to sidecar.
- `core/memory/prune/scheduler_test.go` — exercise CycleMode path with fake clock.

**Acceptance**:
- `HARNESS_MEMORY_PRUNE=false` → scheduler not started.
- Any threshold dial = 0 → scheduler not started.
- Boot with `last_cycle_at > 24h ago` → catch-up cycle runs immediately.
- `OnSweep` callback fires once per cycle, payload matches `Report` shape.
- Shutdown ctx cancel → scheduler exits within one tick.

## WP04 — Generalizability classifier + tests

**Effort**: S
**Deps**: none (parallel with WP01-03)
**Files touched**:

- `core/memory/generalizability.go` (NEW) — `Verdict`, `Score(content string) Verdict`, regex/token signal lists.
- `core/memory/generalizability_test.go` (NEW) — table-driven fixtures matching the spec's classifier examples plus edge cases (empty, very long, unicode, mixed signals).
- `core/memory/doc.go` — document classifier scope + LLM-rerank deferral.

**Acceptance**:
- "The user always prefers tabs over spaces." → `Score > 0.5`, `Promote=true`.
- "In this session we fixed the off-by-one in graph.go." → `Score < -0.5`, `Promote=false`.
- "Tool ripgrep returns matches as JSON lines." → `Score > 0.5`, `Promote=true`.
- Empty / whitespace → `Score == 0`, `Promote=false` (orphan-keep).
- Mixed conflicting → `|Score| < 0.3`, `Promote=false` (greedy-keep but not promoted).
- Pure-Go, no network, < 100µs per call on a 1KB string.

## WP05 — Session-delete cascade hook + smart promotion

**Effort**: M
**Deps**: WP01, WP04
**Files touched**:

- `core/rpc/views/sessions/impl.go` — extend `Delete` / `DeleteWithOptions` cascade chain with new `cascadeMemories(ctx, sessionID, opts)` step.
- `core/rpc/views/sessions/impl_delete_cascade_test.go` — extend existing test file with cascade-on, cascade-off (default + smart-promote), pinned-override scenarios.
- `core/memory/cascade.go` (NEW) — `CascadeOnSessionDelete(ctx, store, sessionID, settings, classifier, emit) (Report, error)` — pure logic, callable from session impl + testable in isolation.
- `core/memory/cascade_test.go` (NEW) — unit tests for the four branches (pinned skip, classifier-promote, classifier-no-promote, force-cascade-on).

**Acceptance**:
- Default `MemoryCascadeOnSessionDelete=false`: high-scorer memories get `+5` to score; auto-pin only on Score > 0.7; `KindMemoryPromotedOnSessionDelete` fires.
- Default + low-scorer: chunk untouched, becomes orphan with dangling `source_session_id`.
- `MemoryCascadeOnSessionDelete=true`: every derived chunk soft-deleted with reason=`session_cascade`; classifier NOT consulted; pinned chunks STILL skipped.
- Session-delete RPC contract: even if cascade fails per-row, session row delete still succeeds (best-effort metadata).

## WP06 — Explicit delete + Restore RPCs

**Effort**: S
**Deps**: WP01
**Files touched**:

- `core/rpc/views/memory/api.go` — add `SoftDelete(ctx, id) error`, `Restore(ctx, id) error`, `ListRecentlyDeleted(ctx, filter) ([]Chunk, error)`, `PruneAuditTail(ctx, limit) ([]CycleReport, error)` to `MemoryAPI` interface; new wire types `CycleReport`, `RecentlyDeletedFilter`.
- `core/rpc/views/memory/impl.go` — implementations; `ErrRecoveryWindowExpired` exported; `Forget` keeps existing hard-delete signature but is no longer wired to the panel button.
- `core/rpc/views/memory/impl_test.go` — round-trip; expired-restore yields typed error; ListRecentlyDeleted respects scope filter; PruneAuditTail reads sidecar.
- `frontend/src/lib/types.ts` — new types matching wire shapes.

**Acceptance**:
- `SoftDelete` then `ListRecentlyDeleted` returns the row with `deletedAt` set.
- `SoftDelete` then `Restore` round-trips; row reappears in `ListChunks`.
- Restore after recovery window → `ErrRecoveryWindowExpired` (typed).
- `PruneAuditTail` returns the 30 most-recent cycle reports from the sidecar.

## WP07 — Settings dials + UI rows

**Effort**: S
**Deps**: WP02 (RunCycle reads opts derived from settings)
**Files touched**:

- `core/rpc/views/settings/api.go` — add six new fields (four prune thresholds + cascade bool + bonus int); six new `Effective*` accessors; six new `LoadX/SaveX` pairs on `SettingsStore`.
- `core/rpc/views/settings/impl.go` — JSON file round-trip for new fields.
- `core/rpc/views/settings/impl_test.go` — defaults round-trip; zero-value disable behavior.
- `frontend/src/views/settings/MemorySection.vue` (or wherever Memory settings live — verify) — new "Auto-prune" row group; cascade toggle with explicit copy; advanced disclosure for bonus.
- `frontend/src/lib/types.ts` — extend `Settings` shape.

**Acceptance**:
- Fresh install: defaults 5000 / 0.10 / 180 / 60 / false / 5.
- Setting any threshold to 0 → `EffectiveAutoPruneEnabled() == false`.
- UI dial 0-state shows "Auto-prune disabled" tooltip.
- Cascade toggle copy matches spec ("memories are kept and only forgotten when they age out naturally").

## WP08 — Frontend MemoryView "Recently deleted" + Restore + chips

**Effort**: M
**Deps**: WP06, WP07
**Files touched**:

- `frontend/src/views/memory/MemoryView.vue` — add Live/Recently-deleted tab toggle; "Recently deleted" panel with delete-reason chips, days-until-expiry countdown, Restore button; replace existing Delete button with inline confirm strip ("can be restored for 60 days").
- `frontend/src/views/memory/RecentlyDeletedPanel.vue` (NEW) — extracted component for the deleted-row list.
- `frontend/src/views/memory/PruneAuditPanel.vue` (NEW) — cycle-history view, expandable per-cycle detail, "Run a prune cycle now" button.
- `frontend/src/views/memory/MemoryView.test.ts` — interaction tests (Vitest + @vue/test-utils): tab switch, Restore round-trip, expired-restore graceful banner.

**Acceptance**:
- Tab switch live ↔ recently-deleted with no flicker.
- Each chip color-coded per spec (gray/blue/amber).
- "expires in N days" countdown reflects `recoveryDays - (now - deletedAt)`.
- Restore button calls RPC and removes row from the list optimistically.
- Empty state copy matches spec.

## WP09 — Integration tests + dev seeder

**Effort**: M
**Deps**: WP02, WP05, WP06, WP08
**Files touched**:

- `core/memory/prune/integration_test.go` (NEW) — end-to-end: seed 6000 chunks → RunCycle → verify counts → restore one → SoftDelete one → cascade-on session delete → cascade-off session delete with mixed signals.
- `scripts/dev/seed_memories.go` (NEW, dev-only build tag) — populates the gob with N synthetic chunks, mixed `LastAccessed` ages, mixed `Kind`, mixed `SourceTurn`.
- `core/rpc/views/sessions/impl_delete_cascade_integration_test.go` — extends existing cascade test with the memory dimension, asserting `KindMemoryPromotedOnSessionDelete` fires the expected number of times.
- Manual smoke checklist captured in `core/memory/prune/SMOKE.md` (NEW) — the five steps from plan §4.

**Acceptance**:
- All integration tests green.
- Smoke checklist runs end-to-end on a dev build, screenshots captured for each step.
- No flake on 10× test runs.

## Sequencing diagram

```
              ┌────────────────────────┐
              │ WP01 schema + audit    │
              │ (S)                    │
              └────┬───────────────┬───┘
                   │               │
         ┌─────────▼────┐   ┌──────▼─────┐
         │ WP02 RunCycle│   │ WP04       │
         │ (M)          │   │ classifier │
         │              │   │ (S, parallel)
         └────┬─────────┘   └─────┬──────┘
              │                   │
   ┌──────────┼───────────┐       │
   │          │           │       │
   ▼          ▼           ▼       ▼
┌──────┐  ┌──────┐    ┌─────────────┐
│ WP03 │  │ WP06 │    │ WP05 cascade│
│ sched│  │ RPCs │    │ (M)         │
│ (S)  │  │ (S)  │    │             │
└──────┘  └──┬───┘    └─────────────┘
             │
       ┌─────▼─────┐
       │ WP07      │ (depends on WP02 too — opts shape)
       │ settings  │
       │ (S)       │
       └─────┬─────┘
             │
       ┌─────▼─────┐
       │ WP08 UI   │
       │ (M)       │
       └─────┬─────┘
             │
       ┌─────▼──────────┐
       │ WP09 integ +   │
       │ smoke seeder   │
       │ (M)            │
       └────────────────┘
```

Parallelism notes:
- WP04 (classifier) is fully independent — start immediately alongside WP01.
- WP03 (scheduler) and WP06 (RPCs) can run in parallel after WP02.
- WP07 (settings) depends on WP02 (consumes `CycleOpts` shape) and WP06 (settings UI may surface alongside memory UI elements). Schedule after both reach mid-implementation.
- WP08 is the longest single track on the frontend; start its skeleton (tab plumbing, types) as soon as WP06 lands the wire types.
- WP09 is the integration phase — schedule for last; requires the full chain.
```

### Critical Files for Implementation

- /Users/alecfeeman/PycharmProjects/kaneaz-harness/core/memory/chunk.go
- /Users/alecfeeman/PycharmProjects/kaneaz-harness/core/memory/store.go
- /Users/alecfeeman/PycharmProjects/kaneaz-harness/core/memory/prune/pruner.go
- /Users/alecfeeman/PycharmProjects/kaneaz-harness/core/rpc/views/memory/api.go
- /Users/alecfeeman/PycharmProjects/kaneaz-harness/frontend/src/views/memory/MemoryView.vue
