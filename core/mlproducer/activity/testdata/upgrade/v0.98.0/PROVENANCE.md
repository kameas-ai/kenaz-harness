# activity.db v0.98.0 upgrade fixture — provenance

Hand-written. `dump.sql` is a populated `<dataDir>/ml/activity.db` written
by the RELEASED v0.98.0 store (schema v1), the shape every real install
that ran v0.98.0 has on disk. `upgrade_test.go` materialises it and opens
it under HEAD (CLAUDE.md blind spot #3: an empty database cannot show an
upgrade defect).

## How this file was produced

Generated 2026-10-17 from a detached worktree at tag `v0.98.0`
(`71e45602`, PR #437), removed afterwards:

    git worktree add --detach ~/.worktrees/kenaz-harness-v0980-fixture v0.98.0
    # throwaway, never committed: core/mlproducer/activity/zz_fixture_gen_test.go
    T=$(mktemp -d)
    FIXTURE_OUT=$T/ml/activity.db go test ./core/mlproducer/activity \
      -run TestZZGenerateUpgradeFixture -count=1
    sqlite3 $T/ml/activity.db .dump > dump.sql   # sqlite3 3.51.0
    git worktree remove ~/.worktrees/kenaz-harness-v0980-fixture

The throwaway test opened the file with v0.98.0's `activity.Open`, wrote
four tasks (ages 1, 30, 100 and 400 days before 2026-10-16T00:00Z) with
two events each through v0.98.0's `Store.Commit`, and inserted one
`ml_predictions` and one `ml_events` row per age with plain INSERTs (the
engine's write path; the harness store has no API for them), then
checkpointed the WAL and closed.

- Tag: `v0.98.0` (activity.db schema v1; ledger row version 1)
- Rows: events 8, tasks 4, ml_predictions 4, ml_events 4
- dump.sql md5: `83e5c8adce382433d315738601ee2dee`

## What the test asserts under HEAD

Every row survives; the ledger is at 2 with `harness_retention_choice`
present and empty; `RetentionChoice` reports no choice (not an error);
a reopen is idempotent; a choice round-trips.

A frozen fixture: never regenerate or edit `dump.sql`. A future schema
change adds a new `testdata/upgrade/<tag>/` beside it.
