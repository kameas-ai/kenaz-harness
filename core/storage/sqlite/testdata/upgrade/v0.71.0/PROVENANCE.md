# Provenance: `testdata/upgrade/v0.71.0/dump.sql`

**Tag**: `v0.71.0` — cut by `tag-on-merge.yml` when PR #307 squash-merged
("the fail-safes for three live capabilities, and the cron path that never
called a gate").

## How this snapshot was produced

```bash
bash scripts/ci/upgrade-snapshot.sh v0.71.0
```

Replay from `v0.70.0` using HEAD's generator: the tag supplies the database
STATE, HEAD supplies the dump FORMAT.

## The gate caught it again, and that is the system working

`check-upgrade-snapshot-present.sh` fired on v0.71.0 — the **sixth** release
it has caught out of the seven since it shipped in v0.65.0. The one
exception was v0.70.0, cut during the merge.

The honest reading has not changed across six provenance files: the gate did
not teach anyone to remember. It made forgetting cheap and loud instead of
silent and compounding. Before it existed the ritual was skipped on three
consecutive releases, each documenting the gap and leaving it open.

## Schema change for v0.71.0

```
762 lines (was 744), 54 ledger rows (was 53)
+ INSERT INTO harness_migrations (340, 'sessions/0340-scheduled-chat-runs-created-by', 'sessions', 'applied')
+ scheduled_chat_runs gains created_by and tool_allowlist
```

`sessions/0340` (model-scheduled-jobs-01PMSJ01 WP09) is the fail-safe for a
capability that had been executing unattended since v0.65.0 with no policy
able to stop it. `created_by NOT NULL DEFAULT 'user'` is the load-bearing
part: on an upgraded install every schedule written before the column
existed must read back as user-created, because `GateScheduledChatExecute`
fails closed only for `created_by == "model"`. A wrong default here would
either strand every existing schedule or, worse, leave the model arm
reachable without provenance.

## A verification note worth keeping

The first check run against this dump compared `grep '^CREATE'` between
v0.70.0 and v0.71.0 and reported **no difference** — which would have meant
the migration had not been captured. It had. `CREATE TABLE
scheduled_chat_runs` spans multiple lines, and the added columns live on
continuation lines that a `^CREATE` filter never sees.

The shallow check, not the snapshot, was wrong. Confirmed properly by
counting the column names across both files: `created_by` and
`tool_allowlist` appear twice each in v0.71.0 and **zero** times in
v0.70.0. Recorded because a filter that silently matches the wrong thing is
the same failure class the upgrade chain exists to catch, and it nearly
produced a false "the snapshot is broken" alarm.

## Verification

`TestUpgradePath` must show `v0.71.0` as its own `RUN`/`PASS` pair opening a
real database, and `expectedChangedTables` already covers
`scheduled_chat_runs` for every tag — see `scheduledChatRunsProvenanceNote`,
which now also records what that allowlisting costs.
