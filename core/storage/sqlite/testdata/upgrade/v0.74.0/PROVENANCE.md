# Provenance: `testdata/upgrade/v0.74.0/dump.sql`

**Tag**: `v0.74.0` (commit `69e92e10`).

## How this snapshot was produced

```bash
bash scripts/ci/upgrade-snapshot.sh v0.74.0
```

reported `replay mode: v0.73.3 -> v0.74.0`, created a detached worktree at
`refs/tags/v0.74.0` (`HEAD is now at 69e92e10`), materialised
`v0.73.3/dump.sql` into a fresh `data.db` under `$TMPDIR`, ran that tag's
`storagesqlite.Open` against it, and dumped through
`core/storage/sqlite/upgradesnap`.

## Why this is the third snapshot in one PR — the serialisation, demonstrated

`v0.74.0` was cut by `tag-on-merge.yml` when **#312** squash-merged with a
`feat(ci):` subject — a **minor** bump, per CLAUDE.md's table.

That PR changed CI runner selection. It touched no storage, no migration, and
no Go code the harness ships. It still cut a tag, and **that tag still owed a
snapshot**, and until this file existed every other open PR was red on the
shared `Go lint + vet + codegen drift` job.

This is the third tag in this PR precisely because the debt accrued *while the
PR that pays it was in review*: `v0.73.2` and `v0.73.3` from #315/#316, then
`v0.74.0` from #312 landing mid-flight. Folding it in here rather than opening
a fourth snapshot PR was deliberate — a chase in which each snapshot PR is
invalidated by the next merge does not terminate.

**The operational rule this establishes**, and the reason it is written down
three times in this directory: a tag-cutting merge is not complete until its
snapshot is committed. Any prefix in the `feat|fix|perf|revert|deps` family
cuts one — including a `feat(ci):` change with no product surface at all.

## Schema review for v0.74.0

**`dump.sql` is byte-identical to `v0.73.3/dump.sql`.**

```
$ cmp core/storage/sqlite/testdata/upgrade/v0.73.3/dump.sql \
      core/storage/sqlite/testdata/upgrade/v0.74.0/dump.sql
$ echo $?
0
```

Verified rather than assumed:

```
$ git diff --stat v0.73.3..v0.74.0 -- '*migration*'
(empty)
```

A CI-configuration change registers no migration, so replaying `v0.73.3`'s
database under `v0.74.0`'s code applies nothing and changes no row. The empty
schema diff is the evidence — and for a minor-version bump it is worth having
on the record, because a reader seeing `v0.73.x -> v0.74.0` would reasonably
expect the schema to have moved.
