# Provenance: `testdata/upgrade/v0.77.1/dump.sql`

**Tag**: `v0.77.1`, cut by `tag-on-merge.yml` from **#330,
`fix(autonomy): WP23 — the autonomy tier reaches the Cedar prompt registry`**.

## How this snapshot was produced

```bash
bash scripts/ci/upgrade-snapshot.sh v0.77.1
```

`replay mode: v0.77.0 -> v0.77.1` — detached worktree at the tag, `v0.77.0`'s
dump materialised into a fresh `data.db` under `$TMPDIR`, that tag's
`storagesqlite.Open` run against it, dumped through `upgradesnap`.

## Schema review

**Byte-identical to `v0.77.0/dump.sql`**, verified rather than assumed:

```
$ cmp v0.77.0/dump.sql v0.77.1/dump.sql              -> 0
$ git diff --stat v0.77.0..v0.77.1 -- '*migration*'  -> (empty)
```

The autonomy tier is carried on `context.Context` and read by the Cedar
prompt registry at decision time. It is deliberately *not* persisted, so
there is nothing for a migration to add.

## Replay verified

Unlike the v0.74.0–v0.75.2 entries — whose PROVENANCE files recorded that
only `check-upgrade-snapshot-present.sh` (a *filename* comparison) had been
run — this snapshot was checked by actually replaying it:
`go test ./core/storage/sqlite/ -run TestUpgradePath -count=1`, the whole
committed chain, green. A filename check cannot tell a replayable dump from
an unreplayable one; only booting it can.
