# Provenance: `testdata/upgrade/v0.68.0/dump.sql`

**Tag**: `v0.68.0` — cut by `tag-on-merge.yml` when PR #303 squash-merged
("Bedrock tool round-trips, settings that survive a restart, and the
harness-self attach").

## How this snapshot was produced

```bash
bash scripts/ci/upgrade-snapshot.sh v0.68.0
```

Replay from `v0.67.0`, using HEAD's generator: the tag supplies the
database STATE, HEAD supplies the dump FORMAT.

## The gate's fifth consecutive catch

`check-upgrade-snapshot-present.sh` shipped in v0.65.0 and has now fired on
**every release since, without exception**:

| Release | Caught |
|---|---|
| v0.65.0 | ✅ minutes after its own tag |
| v0.65.1 | ✅ a tag nobody meant to cut (`fix(ci):` is a patch prefix) |
| v0.66.0 | ✅ backfilled by an agent, unprompted |
| v0.67.0 | ✅ |
| v0.68.0 | ✅ this file |

Five for five. Before the gate existed, the ritual was skipped on three
consecutive releases, each one documenting the gap and leaving it open.

The honest reading has not changed and is worth restating: **the gate did
not make anyone remember.** It made forgetting harmless. A convention that
depends on a human at release time has now failed on eight consecutive
releases. The only difference since v0.65.0 is that the failure is loud,
and cheap to repair, instead of silent and compounding.

That is the correct design conclusion, not a complaint: rituals that depend
on memory should be assumed to fail, and the engineering effort belongs in
detection rather than in reminders.

## Schema review for v0.68.0

744 lines, 52 ledger rows, high-water `event-log/0106-events-fts-sync` —
**byte-identical to `v0.67.0/dump.sql`**.

Correct and expected: v0.68.0 shipped four missions (Bedrock tool
round-trip, compaction persistence, harness-self attach, controls) and
registered no migration. `scroll_position` rides a column that already
existed. An empty schema diff is itself the evidence that a release of
that size did not touch storage.

## Verification

`TestUpgradePath` must show `v0.68.0` as its own `RUN`/`PASS` pair opening
a real database.
