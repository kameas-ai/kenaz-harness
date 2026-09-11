# Provenance: `testdata/upgrade/v0.77.0/dump.sql`

**Tag**: `v0.77.0`, cut by `tag-on-merge.yml` from **#328,
`feat(frontend): UNIT-7 — RunView offers Approve and Reject`**.

## How this snapshot was produced

```bash
bash scripts/ci/upgrade-snapshot.sh v0.77.0
```

`replay mode: v0.76.1 -> v0.77.0` — detached worktree at the tag, `v0.76.1`'s
dump materialised into a fresh `data.db` under `$TMPDIR`, that tag's
`storagesqlite.Open` run against it, dumped through `upgradesnap`.

## Schema review

**Byte-identical to `v0.76.1/dump.sql`**, verified rather than assumed:

```
$ cmp v0.76.1/dump.sql v0.77.0/dump.sql              -> 0
$ git diff --stat v0.76.1..v0.77.0 -- '*migration*'  -> (empty)
```

A frontend surface mounted over approval RPCs that already existed. The
approval records it reads were already persisted; no new table or column.

## Replay verified

Unlike the v0.74.0–v0.75.2 entries — whose PROVENANCE files recorded that
only `check-upgrade-snapshot-present.sh` (a *filename* comparison) had been
run — this snapshot was checked by actually replaying it:
`go test ./core/storage/sqlite/ -run TestUpgradePath -count=1`, the whole
committed chain, green. A filename check cannot tell a replayable dump from
an unreplayable one; only booting it can.
