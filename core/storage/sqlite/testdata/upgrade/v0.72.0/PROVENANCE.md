# Provenance: `testdata/upgrade/v0.72.0/dump.sql`

**Tag**: `v0.72.0` (squash commit `e50168cd`) — the newest release tag at the
time this snapshot was generated (2026-08-25).

## How this snapshot was produced

Replay, the normal path (`snapshot(N) = replay(snapshot(N-1), tag N)`):

```bash
bash scripts/ci/upgrade-snapshot.sh v0.72.0
```

which reported `replay mode: v0.71.0 -> v0.72.0`, created a detached
`git worktree` at `refs/tags/v0.72.0` (`HEAD is now at e50168cd`),
materialised `v0.71.0/dump.sql` into a fresh `data.db`, ran that tag's
`storagesqlite.Open` against it, and dumped the result through
`core/storage/sqlite/upgradesnap`.

## Schema review for v0.72.0

**`dump.sql` is byte-identical to `v0.71.0/dump.sql`.**

```
$ cmp core/storage/sqlite/testdata/upgrade/v0.71.0/dump.sql \
      core/storage/sqlite/testdata/upgrade/v0.72.0/dump.sql
$ echo $?
0
```

762 lines. That is the correct and expected result: **v0.72.0 registered no
new migration**, so replaying v0.71.0's database under its code applies
nothing and changes no row.

An empty schema diff is a finding in its own right, and for this release it is
worth stating why it is *not* suspicious. v0.72.0 wired the SQLite capability
cache (review finding B3) — the first production code path that can write to
`provider_capabilities`. No migration accompanies it because that table has
existed since `sessions/0329-provider-capabilities`, shipped in v0.63.0. A new
*writer* to an existing table is not a schema change.

## `expectedChangedTables["v0.72.0"]` is ONE entry, not two

v0.70.0 and v0.71.0 each needed `{scheduled_chat_runs, tasks}`. v0.72.0 needs
only `{tasks}`, and the difference is load-bearing:

- `scheduled_chat_runs` changed on those tags because of `sessions/0340`'s
  `DEFAULT` backfill. v0.72.0 runs no migration, so there is nothing to
  backfill.
- `tasks` changes because `assertTasksTableMigrated` performs its **own probe
  insert**. That is a test artifact, not a migration.

Listing `scheduled_chat_runs` here anyway — copying the previous tag's entry
without checking — would silently excuse a real future change to that table.
The durable fix is still owed: stop the probe writing to a watched table so a
no-migration release needs no entry at all. Owner: alec.

## Verification run

```
$ go test ./core/storage/sqlite/ -run TestUpgradePath -count=1 -v
=== RUN   TestUpgradePath/v0.72.0
--- PASS: TestUpgradePath/v0.72.0 (1.15s)
```

Checked that the subtest genuinely RAN rather than the suite merely staying
green: `TestUpgradePath/v0.72.0` appears as its own `RUN` line with its own
`PASS` line and opens a real database under
`/T/TestUpgradePathv0.72.0*/00{1,2}/data.db`. A green suite is not evidence
that a new snapshot is covered — that distinction is the whole reason this
chain exists.

**It was not green on the first attempt.** The freshly generated snapshot
failed `TestUpgradePath/v0.72.0` with `table tasks row count changed: 0 -> 1
(not in expectedChangedTables["v0.72.0"])`. That is the gate working: the
snapshot was correct and the *pin* was missing. Recorded here because the
same failure will greet whoever cuts v0.73.0.
