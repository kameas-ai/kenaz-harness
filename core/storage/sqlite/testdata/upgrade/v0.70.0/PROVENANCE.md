# Provenance: `testdata/upgrade/v0.70.0/dump.sql`

**Tag**: `v0.70.0` — cut by `tag-on-merge.yml` when PR #306 squash-merged
("six missions, and the workflow dispatcher that has been missing since
v0.65.0").

## How this snapshot was produced

```bash
bash scripts/ci/upgrade-snapshot.sh v0.70.0
```

Replay from `v0.69.0`, using HEAD's generator: the tag supplies the
database STATE, HEAD supplies the dump FORMAT. Cut during the merge, not
after a red gate — second release running.

## The first schema change in five releases

`v0.66.0` through `v0.69.0` were all byte-identical: four consecutive
releases that touched no storage. This one is not.

```
761 lines (was 744), 53 ledger rows (was 52)

+ CREATE TABLE tasks (
+ CREATE INDEX idx_tasks_owner  ON tasks(owner_session_id);
+ CREATE INDEX idx_tasks_status ON tasks(status);
+ INSERT INTO harness_migrations ... (1200, 'tasks/1200-tasks-init', ..., 'tasks', 'applied')
```

`subagent-control-and-background-tasks-01PMZB11` UNIT-2 is the cause. The
`tasks` package had been carrying a hand-rolled `Migration{ID,SQL}` type
and its own `MigrationRegistry` whose `Register(owner string, …)`
signature never matched the real `migrations.Registry.Register(m
Migration)`. It could not have been registered; the table did not exist
on any install. The unit rewrote it into the real framework shape with
byte-identical SQL and registered it beside `units` in `sqlite.go`.

**Block 1200-1299 was claimed for `tasks`** in
`core/storage/migrations/blocks.go`. The previous maximum was `units` at
1199, and the range was confirmed free before claiming — this matters
because two mission pairs already share blocks (a2a/signed-cards-trust at
600-699, bundle/shared-context-distribution at 700-799), and a collision
fires `ErrVersionCollision` inside `storagesqlite.Open`, i.e. an install
that will not start. That hazard is still open and unowned; it did not
grow here.

## Why this one deserves more than the usual glance

An empty schema diff is cheap to accept. This is the first non-empty one
since the absence-gate shipped, and it is a migration that had **never
run against populated tables** — the exact shape CLAUDE.md blind spot #3
names, and the shape that shipped the v0.63.0 P0.

The migration is additive: `CREATE TABLE IF NOT EXISTS` and `CREATE INDEX
IF NOT EXISTS` only, no DELETE, no DROP, no unqualified UPDATE, no table
rebuild. `check-destructive-migration-coverage.sh` does not flag it, and
that is correct rather than a gap. `Down` drops the table but is
reachable only through an explicit `Registry.Rollback`, never at boot.

Coverage is not the usual "TestUpgradePath stayed green": `TestUpgradePath`
gained `assertTasksTableMigrated`, which boots **every** committed
snapshot v0.63.0 → v0.70.0 and then inserts through
`coretasks.NewSQLiteStore(db.SQL())` — the production writer, not a
hand-rolled INSERT. Independently re-verified during the PR #306 review,
which also tried and failed to make the migration lose data.

## Verification

`TestUpgradePath` must show `v0.70.0` as its own `RUN`/`PASS` pair
opening a real database, and `expectedChangedTables` must name `tasks`
for this tag — a new table appearing with no entry there means the test
is not actually comparing what it claims to.
