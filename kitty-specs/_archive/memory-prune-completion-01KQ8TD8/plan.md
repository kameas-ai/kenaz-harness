# plan.md — Memory prune completion (`memory-prune-completion-01KQ8TD8`)

> Builds the user-facing soft-delete + auto-prune surface on top of the
> existing `core/memory/prune` engine and the dual-layer narrative store
> shipped by `memory-narrative-layer-01KQ8TD1`. Greedy retention is the
> default at every choice point — when in doubt, KEEP.

## 1. Branch contract

| Item | Value |
|---|---|
| Branch | `feature/memory-prune-completion-01KQ8TD8` |
| Base | `main` |
| Soft dep | `memory-narrative-layer-01KQ8TD1` (chunk `Kind` column + `narrative_metrics` table). Skip rules read `Kind` and `scope_kind == "long_term"`. |
| Coordination | Existing `core/memory/prune` package (pruner + scheduler + metrics) is the engine; this mission wires it end-to-end and adds soft-delete semantics, smart session cascade, and explicit-delete UX. |
| Feature flag | `HARNESS_MEMORY_PRUNE` env (default ON). Setting any of the four threshold dials to 0 also disables auto-prune at the per-user layer. |
| Public Go API additions | `core/memory.SoftDelete(ctx, id, reason) error`, `core/memory.Restore(ctx, id) error`, `core/memory.IsDeleted(c) bool` (filter helper); `core/memory/prune.RunCycle(ctx, opts) (Report, error)`; `core/memory/generalizability.Score(content string) Verdict`; new `MemoryAPI` methods `SoftDelete`, `Restore`, `ListRecentlyDeleted`, `PruneAuditTail`. |
| RPC additions | `Memory_SoftDelete`, `Memory_Restore`, `Memory_ListRecentlyDeleted`, `Memory_PruneAuditTail`, plus six new `Settings_*` get/set bindings. |
| Merge gate | All 9 WPs green; integration test "5000-memory cycle prunes ~bottom decile only" passes; "session delete with mixed memory types" promotes high-scorers and orphan-keeps low-scorers; soft-deleted memories never surface in active retrieval; "narrative" + `long_term` chunks never auto-pruned regardless of score; zero new lint/vet/staticcheck violations. |

## 2. Architecture

### 2.1 Soft-delete schema migration

Touches `core/memory/chunk.go` and the chromem-go-replacement gob loader.

- Add to `Chunk` struct:
  - `DeletedAt time.Time `json:"deleted_at,omitempty"`` (zero ⇒ live).
  - `DeleteReason string `json:"delete_reason,omitempty"`` enum: `"" | "auto_prune" | "user_explicit" | "session_cascade"`.
- Migration is purely additive — gob round-trips unknown-field-tolerant; `backfillChunkDefaults` defaults the new fields to zero on legacy load.
- New filter helper `IsDeleted(c Chunk) bool` (DeletedAt non-zero). Every active read path (`List`, `Query`, retriever, system-prompt prelude loader) MUST exclude deleted rows. The store's `Query` and `List` gain an internal `includeDeleted bool` plumbed through a new `WithIncludeDeleted` ScopeFilter option (default false).
- The on-disk gob path is unchanged; index on `deleted_at` is virtual (a slice walk — store size is bounded by the prune ceiling and re-indexing on every write would dominate cost). If row count grows beyond the ceiling we revisit a SQLite materialised view.

### 2.2 Auto-prune cycle (`core/memory/prune/cycle.go`)

New file alongside `pruner.go`. Public surface:

```go
type CycleOpts struct {
    CountThreshold   int           // skip cycle if total live memories < this
    ScorePercentile  float64       // 0.10 = bottom decile by promotion score
    AgeDays          int           // require last_retrieved_at older than this
    RecoveryDays     int           // soft-delete window length (set on each row)
    Now              func() time.Time
    SkipKinds        []string      // narrative + long_term + artifact
}

type Report struct {
    CycleID            string
    RanAt              time.Time
    CandidatesEvaluated int
    SoftDeleted        int
    KeptUnderThreshold int       // count of memories saved by greedy bias (e.g. score-tied with surviving row)
    SkippedReason      string    // "" | "below_count_threshold" | "feature_disabled" | "ran_recently"
    Cycle              []VerdictRow // per-id action log for audit
}

func RunCycle(ctx context.Context, store memory.Store, opts CycleOpts) (Report, error)
```

Algorithm (greedy retention biased):

1. List all live (`!IsDeleted`) chunks for all scopes.
2. If `len(chunks) < CountThreshold` → return Report with `SkippedReason="below_count_threshold"`, no writes.
3. Filter out skip-kinds (narrative*, long_term, artifact-sourced). These are NEVER candidates.
4. Filter out pinned (`Pinned == true`). NEVER candidates.
5. Compute promotion_score for each remaining candidate using the existing `retrievals*1 + citations*3 + pins*10` formula (read from `narrative_metrics` table when chunk has a row, else compute from `RecallCount` only). Helper lives in the narrative-layer package; we import it.
6. Filter to `LastAccessed < now - AgeDays`. Memories accessed in the last 180 days are NEVER candidates.
7. Sort by promotion_score ascending. Take bottom `ScorePercentile` slice.
8. **Tie-break greedy**: any row whose score equals the row at the percentile boundary is KEPT (counted into `KeptUnderThreshold`) — we never drop a row whose score matches a survivor's.
9. For each remaining victim: call `store.SoftDelete(ctx, id, "auto_prune")` setting `DeletedAt = now`, `DeleteReason = "auto_prune"`. NOT a hard delete.
10. Emit a single batched audit event `KindMemoryAutoPruned` carrying `{cycle_id, ran_at, candidates_evaluated, soft_deleted, kept_under_threshold}`. Per-row detail stays in `Report.Cycle` for inspector use; we do NOT spam one event per row (R-008).
11. Persist the new `last_cycle_at` timestamp (sidecar `prune_cycles.json` next to the gob, schema `{last_cycle_at, last_cycle_id, recent_reports[N=30]}`). The `Recently deleted` panel reads this for the audit log.
12. Idempotency: if `last_cycle_at > now - 24h`, return Report with `SkippedReason="ran_recently"` without re-evaluating.

### 2.3 Background scheduler

Reuses existing `core/memory/prune/scheduler.go` (already implements 24h ticker + on-startup catch-up). Wire-up changes only:

- `NewScheduler` constructed at process boot inside `core/app/boot.go` (or wherever the rpc layer wires `MemoryAPI` today — verify the exact site during implementation).
- `WithOnSweep` callback writes the Report to the sidecar JSON and emits `KindMemoryAutoPruned`.
- Reads the four settings dials at start; if `MemoryPruneCountThreshold == 0` (or any other dial) → scheduler is constructed but `Start` is skipped (auto-prune disabled).
- Reads the `HARNESS_MEMORY_PRUNE` env at boot — false → never construct scheduler.
- Cancellable via the app shutdown ctx (existing pattern).
- The cycle runs in `RunCycle` mode (the new opinionated single-pass cycle) NOT the legacy `Pruner.Apply` (which uses the multi-signal scoring designed for narrative-layer's heavier tuning).

### 2.4 Generalizability classifier (`core/memory/generalizability.go`)

Pure-Go heuristic — no LLM. Triggered by the session-delete cascade hook.

```go
type Verdict struct {
    Score    float64  // -1.0 .. +1.0
    Promote  bool     // Score > Threshold
    Signals  []string // human-readable trace for audit / debug
}

func Score(content string) Verdict
```

Reusable signals (each contributes +1 to numerator AND denominator):

- Regex `\b(user|the user)\s+(prefers|likes|uses|wants|always|never|hates|insists)\b`
- Regex `\bthe project (uses|is built on|targets|requires|conventions|style)\b`
- Regex `\btool\s+\w+\s+(returns|expects|emits|produces)\b`
- Tokens `always`, `never`, `convention`, `canonical`, `standard`, `every (file|module|test)`
- Pattern: declarative starting with "X is Y" without temporal markers ("currently", "today", "in this run").

Session-specific signals (each contributes -1 to numerator, +1 to denominator):

- Regex `\b(in this session|earlier in this turn|we just|right before|a moment ago)\b`
- Regex `\b(this debug|that bug we|the issue we hit)\b`
- ISO-8601 timestamp / date / time-of-day strings.
- References to ephemeral session entities (turn IDs, message IDs, tool-call IDs by hash shape).
- Tokens `currently`, `temporarily`, `for now`, `quick fix`.

Score formula: `(reusable - session) / max(reusable + session, 1)`.

Threshold for `Promote=true` (used by cascade): **0.3** — biased toward keep-and-promote. Every signal hit recorded into `Signals` for the audit emission.

Tests fixtures cover:

- "The user always prefers tabs over spaces." → ~+1.0, Promote.
- "In this session we fixed the off-by-one in graph.go." → ~-1.0, no Promote.
- "Tool ripgrep returns matches as JSON lines." → +1.0, Promote.
- Empty / whitespace → 0.0, no Promote (orphan-keep).
- Mixed: "The user uses Vue 3 (we discovered this earlier in this session)." → near-zero, no Promote (orphan-keep — both signals fire, denominator dominates).

LLM-based reranking explicitly deferred (out of scope; tracked as future-work note in `core/memory/generalizability.go` doc.go).

### 2.5 Session-delete cascade hook

Wires into the existing `core/rpc/views/sessions` `Delete` / `DeleteWithOptions` path. Add a new step AFTER attachments/media cascade and BEFORE the actual session row delete:

1. Load `MemoryAPI`'s store reference (already available via the rpc-layer wiring).
2. Look up live chunks where `SourceTurn`/`SessionID == deletedID` (existing field — verify naming during implementation).
3. **If `Settings.MemoryCascadeOnSessionDelete == true`** (user opted into hard-cascade): for each chunk call `store.SoftDelete(ctx, id, "session_cascade")`. Skip the classifier entirely. Emit `KindMemoryAutoPruned` with reason="session_cascade" carrying counts. Done.
4. **Else (default, orphan-keep + smart promotion)**: for each chunk:
   - If `Pinned == true`: leave alone (pin overrides cascade — the user already said keep).
   - Else run `generalizability.Score(c.Content)`.
   - If `Verdict.Promote`: bump `promotion_score += GeneralizabilityBonus` (default `+5`, ~half a pin) by writing to the narrative-layer `narrative_metrics` table; set `Pinned = max(Pinned, false→true if Score > 0.7)` (only auto-pin truly high-confidence reusable rows so we don't pollute the user's pinset). Emit `KindMemoryPromotedOnSessionDelete` carrying `{memory_id, score, signals, bonus_applied, auto_pinned}`.
   - If `!Verdict.Promote`: leave alone — the chunk becomes an orphan, `source_session_id` stays as a dangling reference, frontend renders "deleted session" chip in the memory panel.
5. The session row delete proceeds.

Implementation note: the cascade runs in the same transaction-equivalent the artifacts-storage cascade uses (sequential within session-delete RPC handler; no cross-DB transaction available because the store is gob-on-disk). Failure to classify or promote is logged but does NOT abort session deletion — this is best-effort metadata.

### 2.6 Explicit user delete (panel "Delete" button)

Replace the existing hard `Memory.Forget` call site:

- Add new `MemoryAPI.SoftDelete(ctx, id) error`. Implementation: store call setting `DeletedAt = now`, `DeleteReason = "user_explicit"`. Emits `KindMemoryDeleted` with `{memory_id, reason: "user_explicit"}`.
- Keep `Forget` (hard delete) as a privileged path used ONLY by the post-recovery-window compaction sweep (future work). The frontend NEVER calls Forget anymore.
- No reason-prompt UX. No negative-signal feedback loop. No topic blacklist. Honest soft-delete with an obvious Restore.

### 2.7 Restore RPC

`MemoryAPI.Restore(ctx, id) error`:

- Loads chunk; if not deleted → no-op (idempotent).
- If `now > DeletedAt + RecoveryDays` → return `ErrRecoveryWindowExpired` (typed; UI renders "this memory expired N days ago" gracefully — no crash).
- Else: clears `DeletedAt`, `DeleteReason`. Emits `KindMemoryRestored` with `{memory_id, was_deleted_at, age_in_window_days}`.

Out of scope this mission (documented as future work in `core/memory/prune/doc.go`):

- Hard-delete compaction sweep that purges `DeletedAt < now - RecoveryDays` rows.
- A separate scheduler tick OR a rider on `RunCycle` (preferred) will own that. Until then, expired soft-deleted rows simply accumulate — at the 5000-memory ceiling that is bounded; we accept the storage overhang for now.

### 2.8 Settings dials

Additions to `core/rpc/views/settings/api.go`:

| Field | Type | Default | Disable behavior |
|---|---|---|---|
| `MemoryPruneCountThreshold` | `int` | 5000 | 0 = auto-prune disabled |
| `MemoryPruneScorePercentile` | `float64` | 0.10 | 0 = auto-prune disabled |
| `MemoryPruneAgeDays` | `int` | 180 | 0 = auto-prune disabled |
| `MemoryPruneRecoveryDays` | `int` | 60 | 0 = soft-delete becomes immediate hard-delete (we honor user intent — but UI warns) |
| `MemoryCascadeOnSessionDelete` | `bool` | false | true = derived memories follow session into soft-deleted bucket |
| `MemoryGeneralizabilityBonus` | `int` | 5 | bump applied to promotion_score on classifier `Promote=true`; advanced setting |

Storage: each gets its own `LoadX/SaveX` pair on `SettingsStore` for hot-path reads (mirrors `LoadMemory`). Effective accessors `Settings.EffectiveMemoryPruneXxx()` apply zero-value-means-default logic.

UI: new Settings → Memory section row group "Auto-prune" (four threshold dials + cascade toggle + advanced disclosure for bonus). Each dial labelled with its plain-English meaning; tooltip explains "set to 0 to disable". The cascade toggle has explicit copy: "When you delete a session, also delete the memories created during that session. Off by default — memories are kept and only forgotten when they age out naturally."

### 2.9 Frontend memory panel extensions (`MemoryView.vue`)

Tabs (additions; existing All/Global/Project/Session pill row stays):

- New top-level tab toggle: **Live** / **Recently deleted** (default Live = today's behavior).
- "Recently deleted" view:
  - Calls `Memory_ListRecentlyDeleted({ scopeKind?, sortBy: "deleted_at" | "delete_reason" })`.
  - Renders each row with: content preview, `delete_reason` chip (color-coded — auto-pruned: gray, deleted by you: blue, session removed: amber), days-until-expiry countdown ("expires in 47 days"), Restore button.
  - Empty state: "No recently deleted memories. Deleted memories appear here for {{ recoveryDays }} days before being permanently removed."
- "Live" view:
  - Existing rows gain a "Recently deleted" link in the header showing the count.
  - Each row's "Delete" button now opens an inline confirm strip ("Deleted memories can be restored from the Recently deleted tab for 60 days. [Delete] [Cancel]") — NOT a modal, NOT a reason prompt.

New "Prune audit log" subview (linked from Settings → Memory → "View prune history"):

- Calls `Memory_PruneAuditTail({ limit: 30 })` returning the 30 most-recent Report rows.
- Renders one row per cycle: `ran_at`, `candidates_evaluated`, `soft_deleted`, `kept_under_threshold`, expandable to show per-id verdicts.
- "Run a prune cycle now" button in this view (already exists as `RunPruneNow` — relabel UI to make it explicit it goes to soft-delete bucket, not hard delete).

Frontend types (`frontend/src/lib/types.ts`):

- `MemoryDeleteReason = "auto_prune" | "user_explicit" | "session_cascade"`.
- `MemoryChunk` gains optional `deletedAt?: string` and `deleteReason?: MemoryDeleteReason`.
- New `PruneCycleReport` and `RecentlyDeletedRow` types.

### 2.10 Audit emission

`core/context/audit/audit.go` Kind constants (additions):

- `KindMemoryAutoPruned = "memory.auto_pruned"` — payload `{cycle_id, ran_at, candidates_evaluated, soft_deleted, kept_under_threshold, skipped_reason}`.
- `KindMemoryDeleted = "memory.deleted"` — payload `{memory_id, score, age_days, delete_reason}`.
- `KindMemoryRestored = "memory.restored"` — payload `{memory_id, was_deleted_at, age_in_window_days}`.
- `KindMemoryPromotedOnSessionDelete = "memory.promoted_on_session_delete"` — payload `{memory_id, score, signals, bonus_applied, auto_pinned}`.

Each kind gets a typed payload struct in `audit.go` matching the existing pattern (e.g. `MemoryAutoPrunedPayload`). Frontend audit log view renders the new kinds with localized labels.

## 3. Risk register

| ID | Risk | Likelihood | Impact | Mitigation |
|---|---|---|---|---|
| R-001 | Scheduler fires during a long-running chat → lock contention against `Add` writes | Med | Med | The chromem store's `s.mu` write lock is brief per row; the cycle takes a snapshot via `List` (RLock), then issues per-id soft-delete writes with the standard lock. No long-held write lock. Add explicit metric for cycle duration; budget < 500ms for 5000 rows. |
| R-002 | Prune deletes a memory that is being retrieved concurrently (race) | Low | Med | Soft-delete is an in-place flag flip on a row already loaded; `Query` filters by `IsDeleted` BEFORE returning. A retrieval-in-flight that already holds a copy of the chunk surfaces it that turn — acceptable (the citation may complete). Next-turn retrieval excludes it. |
| R-003 | Generalizability classifier false-positive: session-specific row marked reusable, never gets pruned | Med | Low | Bonus is +5 (half a pin), not infinite — the row stays prune-eligible after enough age. Auto-pin only fires on Score > 0.7 (high confidence). User can manually unpin from Memory panel. |
| R-004 | Generalizability classifier false-negative: genuinely reusable row marked session-specific, gets pruned | Med | Med | Even on classifier "no promote", the row is orphan-kept (default Q20.2=D); it only enters the auto-prune candidate set if it ALSO ages out >180 days AND falls in bottom decile. Two independent signals must both fail. User can also pin proactively. |
| R-005 | Soft-deleted rows pollute active-memory query | Low | High | Single chokepoint: every `List`/`Query` path reads through the new `IsDeleted` filter at the store layer. Unit test asserts every retriever path excludes deleted rows. No way for a caller to accidentally surface them. |
| R-006 | Restore after recovery window | Low | Low | Typed error `ErrRecoveryWindowExpired`; UI renders graceful "expired" message; no crash. Behavior is the documented contract. |
| R-007 | Session-cascade interaction with manually-pinned memories | Med | Med | Pin overrides cascade — pinned memories are NEVER auto-deleted regardless of `MemoryCascadeOnSessionDelete` setting (Q20.2 explicit: "pinned memories never auto-deleted"). Documented in cascade implementation; covered by integration test. |
| R-008 | Audit log spam from a 5000-memory cycle | Low | Med | Single batched event per cycle carrying counts; per-row verdicts stay in the in-memory Report and the sidecar JSON. Frontend audit log shows one cycle row per run, expandable. |
| R-009 | Sidecar `prune_cycles.json` corruption blocks scheduler | Low | Med | Atomic write (tmp+rename, mirrors `saveLocked`). Corrupt file on read → log + start with empty history. Scheduler continues. |
| R-010 | Soft-deleted rows linger past recovery window forever (compaction not implemented this mission) | Med | Low | Documented as future work. At 5000-row ceiling, even with 100% turnover, on-disk size is bounded. Prune cycle counts only LIVE rows toward the threshold, so soft-deleted rows do NOT inflate or suppress trigger conditions. |
| R-011 | Narrative + long_term chunks accidentally pruned | Low | High | `RunCycle` opts include `SkipKinds: ["narrative_extractive", "narrative_synthesised"]` and skips `scope_kind == "long_term"`. Integration test asserts neither is ever in `Report.Cycle` rows. |
| R-012 | Disabling auto-prune (any threshold = 0) leaves memories growing forever | Low | Low | User-explicit choice. UI shows "Auto-prune disabled" banner in Memory panel when disabled. Manual `RunPruneNow` still available. |

## 4. Rollout

Feature flag `HARNESS_MEMORY_PRUNE` defaults ON. Settings dial defaults are conservative (5000 / 0.10 / 180d / 60d).

### Acceptance smoke (5-step manual checklist)

1. **Seed**: in dev mode, populate 6000 fake memories with mixed scopes and `LastAccessed` times spanning 0–365 days ago (script in `scripts/dev/seed_memories.go` — read-only contract here, will be added in WP09).
2. **Trigger**: invoke `Memory_RunPruneNow` (or wait for scheduler tick). Verify Report shows `candidates_evaluated == 6000`, `soft_deleted ≈ 600` (bottom decile of those older than 180d), `kept_under_threshold > 0`.
3. **Restore**: open Recently Deleted tab; pick one row; click Restore; verify it returns to Live tab; verify `KindMemoryRestored` event in audit log.
4. **Smart cascade off (default)**: create a session, generate 6 memories (mix of "user prefers X", "in this session we did Y", and ambiguous content). Delete the session via existing UI. Verify: high-scorers (≈2 rows) get `KindMemoryPromotedOnSessionDelete` + score bumped; low-scorers (≈2 rows) become orphans (still listed in Live tab with "deleted session" chip); ambiguous rows orphan-kept.
5. **Smart cascade on (opt-in)**: in Settings → Memory, toggle `MemoryCascadeOnSessionDelete` ON. Repeat step 4. Verify ALL 6 derived memories appear in Recently Deleted tab with reason chip "session removed", regardless of generalizability score.

### Phased rollout

- **Phase 1**: Land all WPs; flag default ON; cycle never fires unless threshold breached. New users with <5000 memories experience zero behavior change.
- **Phase 2 (post-merge cycle +1)**: Telemetry review of first cycles — confirm `kept_under_threshold` ratio is non-trivial (greedy bias is firing) and false-positive rate (user-Restore within 24h of auto-prune) is < 5%.
- **Phase 3**: If false-positive rate > 5%, raise default `MemoryPruneScorePercentile` floor (e.g. 0.05 = bottom twentieth) without code change — pure settings rollback.

## 5. Cross-mission coordination

- **`memory-narrative-layer-01KQ8TD1`** (soft dep): import the promotion_score helper and `narrative_metrics` reader. SkipKinds list reads its `Kind` constants. If the narrative layer mission has not landed when this one merges, RunCycle gracefully falls back to using `RecallCount` only as the score input (FR-008's full formula degrades to `retrievals × 1`).
- **`built-in-save-artifact-01KQ8TD0`**: SkipKinds also includes `kaneaz__save_artifact`-sourced rows (existing skip rule from FR-005 of the legacy spec). Read the same `Source` field used there.

## 6. Critical Files for Implementation

- `/Users/alecfeeman/PycharmProjects/kaneaz-harness/core/memory/chunk.go` (schema + filter helpers)
- `/Users/alecfeeman/PycharmProjects/kaneaz-harness/core/memory/store.go` (soft-delete + restore + filter wiring)
- `/Users/alecfeeman/PycharmProjects/kaneaz-harness/core/memory/prune/pruner.go` (new sibling `cycle.go` for opinionated single-pass cycle)
- `/Users/alecfeeman/PycharmProjects/kaneaz-harness/core/rpc/views/memory/api.go` (new RPC methods + types)
- `/Users/alecfeeman/PycharmProjects/kaneaz-harness/frontend/src/views/memory/MemoryView.vue` (Live/Recently-deleted tabs + Restore + audit log)
