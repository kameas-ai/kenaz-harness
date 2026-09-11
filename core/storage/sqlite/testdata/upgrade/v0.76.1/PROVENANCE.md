# Provenance: `testdata/upgrade/v0.76.1/dump.sql`

**Tag**: `v0.76.1`, cut by `tag-on-merge.yml` from **#322,
`fix(harness-vm): UNIT-2 — the approval capability stops overclaiming what it can broker`**.

## How this snapshot was produced

```bash
bash scripts/ci/upgrade-snapshot.sh v0.76.1
```

`replay mode: v0.76.0 -> v0.76.1` — detached worktree at the tag, `v0.76.0`'s
dump materialised into a fresh `data.db` under `$TMPDIR`, that tag's
`storagesqlite.Open` run against it, dumped through `upgradesnap`.

## Schema review

**Byte-identical to `v0.76.0/dump.sql`**, verified rather than assumed:

```
$ cmp v0.76.0/dump.sql v0.76.1/dump.sql              -> 0
$ git diff --stat v0.76.0..v0.76.1 -- '*migration*'  -> (empty)
```

A capability-advertisement correction in `cmd/harness-vm`. Nothing in the
harness schema participates.

## Replay verified

Unlike the v0.74.0–v0.75.2 entries — whose PROVENANCE files recorded that
only `check-upgrade-snapshot-present.sh` (a *filename* comparison) had been
run — this snapshot was checked by actually replaying it:
`go test ./core/storage/sqlite/ -run TestUpgradePath -count=1`, the whole
committed chain, green. A filename check cannot tell a replayable dump from
an unreplayable one; only booting it can.
