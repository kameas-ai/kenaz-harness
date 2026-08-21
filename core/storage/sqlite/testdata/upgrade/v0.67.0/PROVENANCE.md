# Provenance: `testdata/upgrade/v0.67.0/dump.sql`

**Tag**: `v0.67.0` — cut by `tag-on-merge.yml` when PR #302 squash-merged
("the audit log and the bundle installer both stop lying").

## How this snapshot was produced

```bash
bash scripts/ci/upgrade-snapshot.sh v0.67.0
```

Replay from `v0.66.0`, using HEAD's generator per the rule PR #300
established: the tag supplies the database STATE, HEAD supplies the dump
FORMAT.

## The gate caught it — the fourth consecutive release

`check-upgrade-snapshot-present.sh` shipped in v0.65.0 and has now fired on
**every release since**:

| Release | Caught | Note |
|---|---|---|
| v0.65.0 | ✅ | its own release, minutes after tagging |
| v0.65.1 | ✅ | a tag nobody meant to cut — `fix(ci):` is a patch prefix |
| v0.66.0 | ✅ | backfilled by the ZA10 agent, unprompted |
| v0.67.0 | ✅ | this file |

Before the gate existed the ritual had been skipped on three consecutive
releases, each one documenting the gap and leaving it open. The pattern is
now inverted: the ritual is skipped just as often, and caught every time.

That is the honest reading. The gate did not make anyone remember — it made
forgetting harmless. A convention that depends on a human at release time
has failed on **seven consecutive releases**; the only difference since
v0.65.0 is that the failure is now loud instead of silent.

## Schema review for v0.67.0

**`dump.sql` is byte-identical to `v0.66.0/dump.sql`** — 52 ledger rows,
high-water `event-log/0106-events-fts-sync`.

Correct and expected: v0.67.0 shipped five missions but registered no new
migration. ZA10's `event-log/0106` landed in v0.66.0; Z909's `bundle/700`
in v0.65.0. An empty schema diff is itself evidence — a release of that
size touching no storage is worth having on the record.

Kept because the property that matters, *the chain reaches the newest
release tag*, is not expressible without it.

## Verification

`TestUpgradePath` must show `v0.67.0` as its own `RUN`/`PASS` pair opening
a real database. A green suite is not evidence that a new snapshot is
covered — the distinction this chain exists for.
