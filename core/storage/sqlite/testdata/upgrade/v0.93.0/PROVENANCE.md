# v0.93.0 upgrade snapshot — provenance

Hand-written per the release ritual. `scripts/ci/upgrade-snapshot.sh`
writes only `dump.sql`; this file is the human-authored part.

## How this file was produced

    HOME=$(mktemp -d) KENAZ_HARNESS_ENV=test bash scripts/ci/upgrade-snapshot.sh v0.93.0

Replay mode, `v0.92.0 -> v0.93.0`. Generated 2026-10-07 from
`chore/v0.93.0-upgrade-snapshot`.

- Tag: `v0.93.0`
- Squash commit: `bc4ebfd4` ("feat: v0.93.0 — sharing that works
  end-to-end, memory sync, the org skill library, and messages that say
  when they didn't arrive", PR #390)
- Predecessor snapshot: `v0.92.0`

## What changed on the upgrade path

NON-ZERO delta — exactly ONE migration on the v0.92.0 -> v0.93.0 path
(ledger: 65 -> 66 rows; sessions block: 44 -> 45). `diff` of the two
dumps is two hunks and nothing else:

1. **`sessions/0344-turn-run-outcome`** (undelivered-message-retry,
   additive): nine `ALTER TABLE session_turn_runs ADD COLUMN`s, every
   one defaulted or nullable so every pre-0344 row — and any run still
   in flight at upgrade — reads as an empty outcome ("unknown"), which
   the transcript renders as nothing, never as NOT DELIVERED:

   | column             | type                          | meaning                                            |
   |--------------------|-------------------------------|----------------------------------------------------|
   | `outcome`          | `TEXT NOT NULL DEFAULT ''`    | `''` \| `completed` \| `failed` \| `stopped`       |
   | `delivered`        | `INTEGER` (NULL = unknown)    | whether the model accepted the request             |
   | `failure_class`    | `TEXT NOT NULL DEFAULT ''`    | `llm.FailureClass`; empty unless `outcome='failed'` |
   | `failure_code`     | `TEXT NOT NULL DEFAULT ''`    | `llm.FailureCode*` (payment_required, rate_limited…) |
   | `failure_status`   | `INTEGER NOT NULL DEFAULT 0`  | provider HTTP status, 0 when none                  |
   | `failure_provider` | `TEXT NOT NULL DEFAULT ''`    | adapter kind ("openrouter")                        |
   | `failure_summary`  | `TEXT NOT NULL DEFAULT ''`    | one-line copy                                      |
   | `failure_message`  | `TEXT NOT NULL DEFAULT ''`    | provider message, `llm.SanitizeProviderMessage`d   |
   | `finished_at`      | `INTEGER` (NULL = in flight)  | unix nanos of the terminal write                   |

   Ledger row: `(rowid 70, version 344, 'sessions/0344-turn-run-outcome',
   owning_mission 'sessions', action 'applied')`, content hash
   `c3ba6629…f734`. Source: `core/session/migrations_turn_run_outcome.go`,
   registered as the last entry of `session.Migrations()`.

   **Idempotency guard:** each ALTER is wrapped in a
   `SELECT COUNT(*) FROM pragma_table_info('session_turn_runs') WHERE name=?`
   probe and skipped when the column already exists (same shape as
   0333), so the ledger-rewind repair path
   (`TestOpen_RepairsDatabaseMissingLateSessionsMigrations`) re-runs it
   as a no-op instead of failing Open with "duplicate column name".
   `Down` is a no-op (package convention for additive defaulted columns;
   see 0317).

   No existing column, row, index or constraint is touched, so
   `check-destructive-migration-coverage.sh` has nothing to cover. The
   upgrade-path proof is
   `core/storage/sqlite/turn_run_outcome_upgrade_test.go`
   (`TestTurnRunOutcome_UpgradesPopulatedV0920Database`), which boots
   the **v0.92.0** snapshot with a planted pre-0344 `session_turn_runs`
   row and asserts the row survives with an empty outcome. That test
   pins `v0.92.0` by name on purpose — it must keep selecting the
   newest snapshot that PREDATES 0344, and this directory's arrival does
   not change that.

Nothing else on this path reaches sqlite. `git diff --name-only
v0.92.0..v0.93.0 -- '*migration*' core/storage core/session` lists
`core/session/{migrations,migrations_test,migrations_turn_run_outcome,
manager,moves,store,types,handoff_transcript*}.go` and the sqlite
tests — the non-migration session files are the writer/reader of the
new columns (terminal UPDATE at stream end; read via the existing
`Sessions_TurnRuns` list), not schema. The rest of v0.93.0 (memory
sync, org skill library, team-sharing routes, device-keys handoff)
persists in fleet or in profile-directory JSON (`memory_sync` lane
state under the fleet data dir), not in `data.db`.

## Consumers owed by this snapshot

- The next destructive or selection-sensitive migration boots from
  THIS snapshot (the newest). Any migration that reads or rewrites
  `session_turn_runs` must be tested against a database that already
  carries the nine 0344 columns — this is the first such snapshot.
- `turn_run_outcome_upgrade_test.go` keeps booting v0.92.0 (pre-0344);
  do not "modernise" it to the newest snapshot or it stops proving the
  upgrade and starts proving a fresh-schema no-op.
- The repair path's re-application window
  (`repair_upgrade_test.go`, ledger entry "v0.87.0 adversarial review
  F2": window ends at 0341) still does not reach 0344; 0344's
  pragma-guard is what makes a future widening safe, not the window
  itself.
