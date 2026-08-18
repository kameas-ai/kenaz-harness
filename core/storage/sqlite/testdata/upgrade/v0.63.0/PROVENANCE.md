# Provenance: `testdata/upgrade/v0.63.0/dump.sql`

**Tag**: `v0.63.0` — the release that shipped the `Pending()` max-based-selection
P0 (fixed in `77e0605b`, `v0.63.1`). See `kitty-specs/upgrade-path-coverage-01PMUG01/spec.md`
§1 for the full defect history.

## How this snapshot was produced

This is the **bootstrap** of the snapshot chain (spec §5.3). There is no
`v0.62.x` snapshot to replay forward from, so unlike every later snapshot
(`snapshot(N) = replay(snapshot(N-1), N)`), this one is built directly:

1. `storagesqlite.Open` against a fresh, empty data dir, at this tree's HEAD
   (every migration through `sessions/0335` registers and applies —
   fresh-install order is unaffected by the `Pending()` bug, spec §1.2).
2. `testdata/upgrade/seed.sql` applied against the fully-migrated schema —
   the synthetic corpus described in spec §5.4.
3. **Rewind** to the exact shape a real upgraded install was in the instant
   before the four dormant sessions migrations landed:
   ```sql
   DELETE FROM harness_migrations WHERE owning_mission='sessions' AND version >= 332;
   ALTER TABLE session_messages DROP COLUMN kind;
   ALTER TABLE session_messages DROP COLUMN move_index;
   ALTER TABLE session_messages DROP COLUMN turn_span_id;
   ALTER TABLE session_messages DROP COLUMN model_tool_args;
   ALTER TABLE sessions DROP COLUMN move_history_mode;
   ```
   This is the identical recipe `core/storage/sqlite/repair_upgrade_test.go`
   and `core/storage/sqlite/artifacts_rebuild_test.go` already use, and those
   tests' own doc comments record it as "verified equivalently against a copy
   of the live dev database" during the v0.63.1 fix. This snapshot runs that
   already-validated recipe through the production `Open` path via
   `scripts/ci/upgrade-snapshot/generator_main.go -mode=genesis`, rather than
   re-deriving it as a one-off hand test.
4. Dump, normalised (`core/storage/sqlite/upgradesnap`).

Reproduce with: `bash scripts/ci/upgrade-snapshot.sh v0.63.0`.

## Cross-check against a real upgraded install (spec §5.3, §10 escalation 1)

Spec §10 escalation 1 asks whether the owner can supply a real upgraded
database for a schema+ledger comparison. **They could**: a verified backup of
the harness's own dev database exists at
`~/.kenaz/harness/dev-backups/data.db.20260816-194625` (outside the repo,
never committed, read-only access used for this comparison only — no bytes
from it appear in this snapshot; spec §3 forbids real user data in fixtures
and that rule was followed literally: only structural facts were compared,
never row content).

That backup turned out to be a much stronger witness than a schema diff:

**Its `harness_migrations` ledger, read directly, has the EXACT shape this
snapshot's bootstrap targets** — `sessions` migrations 300 through 331
applied, `user-slash-commands/1000` applied, `units/1100..1103` applied, and
**no row for `sessions/332`, `333`, `334`, or `335` at all**. This is not an
inference from column shapes; it is the literal ledger of an install that
went through the exact defect this mission exists to catch, still sitting in
the un-repaired state at backup time (`2026-08-16`, well after `v0.63.1`
shipped — this install had not been reopened with the fixed binary since it
last upgraded through the units block).

Structural comparison performed (schema only, no row content):

| Object | Backup (`~/.kenaz/harness/dev-backups/...`) | This snapshot | Match |
|---|---|---|---|
| `harness_migrations` ledger version set | {1,2} ∪ {300..331} ∪ {1000} ∪ {1100..1103} | identical | ✅ |
| `session_messages` columns | `id, session_id, sequence, role, content, tool_calls, created_at, content_json, compacted_into_id, compacted_at, archived_at, prompt_tokens, completion_tokens, cost_usd, cost_source, streaming_failed_at, streaming_failure_kind, streaming_recoverable, continuation_of, knobs_override` — **no `kind`, `move_index`, `turn_span_id`, `model_tool_args`** | identical column set (`PRAGMA table_info` on the rewound schema) | ✅ |
| `sessions` columns | no `move_history_mode` | no `move_history_mode` | ✅ |
| `artifacts.source` CHECK | includes `'model_output'` (0327 applied) | includes `'model_output'` (0327 applied — genesis is built from HEAD, which registers 0327) | ✅ |
| `artifacts.scope_kind` CHECK | **no `'global'`** (0332 not applied) | no `'global'` | ✅ |
| Table count | 40 | 40 (post-rewind; matches the backup's table count exactly) | ✅ |

The comparison was read-only, against a local copy
(`/private/tmp/.../scratchpad/backupcheck/data.db`, deleted after use), via
`sqlite3` CLI `PRAGMA table_info` / `SELECT sql FROM sqlite_master` /
`SELECT version, id, applied_at, owning_mission, action FROM harness_migrations`
queries. No write ever touched `~/.kenaz/harness/dev/` or the backup file
itself.

**Verdict**: this is not a hand-built belief about what a broken install
looked like. It reproduces a real one's structure exactly, confirmed against
ground truth. The only thing NOT carried into the committed snapshot is the
real install's actual row content (per spec §3's hard rule) — the seed
corpus (`seed.sql`) is synthetic and fills that role instead.

## What this snapshot proves when opened under a fixed `Pending()`

`Open` must apply `sessions/332`, `333`, `334`, `335` in that order.
`sessions/0332-artifacts-global-scope` runs its create/copy/drop/rename
rebuild against the two `artifact_versions` rows the seed corpus planted
under `seed-artifact-1` (spec §5.4's cascade canary) — if the scratch-table
save/restore ever regresses, those rows silently disappear and this
snapshot's `upgrade_path_test.go` case fails on the artifact_versions count,
independent of `core/storage/sqlite/artifacts_rebuild_test.go`'s own
migration-0332 test (spec §6.3).

## What this snapshot proves when opened under the REVERTED (buggy) `Pending()`

This is the mission's headline falsifiability criterion (spec §6.1). Under
`if m.Version > maxApplied`, `maxApplied = 1103` (from the units block) and
none of `332`/`333`/`334`/`335` are selected. `verifyFullyApplied` (added in
`v0.63.1`) then refuses to `Open` at all — and if that check is also
reverted, the session-list read fails with `no such column:
move_history_mode`. Both were performed and observed; see the WP02 mission
report for the pasted output.
