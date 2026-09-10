# Provenance: `testdata/upgrade/v0.75.0/dump.sql`

**Tag**: `v0.75.0`, cut by `tag-on-merge.yml` from **#319, feat(mcp): add the computer-use connector to the recipe catalog**.

## How this snapshot was produced

```bash
bash scripts/ci/upgrade-snapshot.sh v0.75.0
```

`replay mode: v0.74.0 -> v0.75.0` — detached worktree at the tag, `v0.74.0/dump.sql`
materialised into a fresh `data.db` under `$TMPDIR`, that tag's
`storagesqlite.Open` run against it, dumped through
`core/storage/sqlite/upgradesnap`.

## Schema review

**Byte-identical to `v0.74.0/dump.sql`**, and verified rather than assumed:

```
$ cmp v0.74.0/dump.sql v0.75.0/dump.sql          -> 0
$ git diff --stat v0.74.0..v0.75.0 -- '*migration*'  -> (empty)
```

Neither a connector-catalog entry nor an egress-guard fix registers a
migration, so replaying under this tag's code applies nothing.

## Why five snapshots landed in one PR

This directory now records the same lesson five times, which is itself the
finding. Each of `v0.73.2`, `v0.73.3`, `v0.74.0`, `v0.75.0` and
`v0.75.1` was cut **automatically** by a merge nobody thought of as a
release — two sub-agent fixes, a CI runner-tier flag, a catalog entry, an
egress-guard fix. Every one owed a snapshot, and until it existed
`check-upgrade-snapshot-present.sh` failed the shared
`Go lint + vet + codegen drift` job on **every open PR**.

The tags accrued *while the PR paying the debt was itself in review*, which is
why they were folded in rather than chased with a new PR each time: that chase
does not terminate while merges continue.

**The rule:** any `feat|fix|perf|revert|deps` prefix cuts a tag, and a
tag-cutting merge is not complete until its snapshot is committed. Batching is
the correct response to a burst of merges — get every PR green
simultaneously, merge back-to-back on recorded results (the ruleset does not
require branches to be up to date), then pay the whole snapshot debt once.
