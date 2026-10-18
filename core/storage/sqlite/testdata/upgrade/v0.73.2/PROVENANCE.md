# Provenance: `testdata/upgrade/v0.73.2/dump.sql`

**Tag**: `v0.73.2` (commit `57361742`).

## How this snapshot was produced

Replay, the normal path (`snapshot(N) = replay(snapshot(N-1), tag N)`):

```bash
bash scripts/ci/upgrade-snapshot.sh v0.73.2
```

which reported `replay mode: v0.73.1 -> v0.73.2`, created a detached `git worktree` at
`refs/tags/v0.73.2` (`HEAD is now at 57361742`), materialised `v0.73.1/dump.sql` into a
fresh `data.db` under `$TMPDIR`, ran that tag's `storagesqlite.Open`
against it, and dumped the result through `core/storage/sqlite/upgradesnap`.

## Why two snapshots landed together, and the structural lesson

`v0.73.2` was cut automatically by `tag-on-merge.yml` when a `fix:`-prefixed PR
squash-merged:

    fix(subagent): UNIT-14 — timeout cancels the stream, not just the waiter (#315)

**Neither tag was a deliberate release.** They are the automatic consequence of
the patch lane: **every `fix:` or `feat:` merge cuts a tag, and every tag owes
a snapshot.** Merging two `fix:` PRs in succession therefore created two
snapshot debts at once — and because
`check-upgrade-snapshot-present.sh` compares `max(testdata/upgrade/v*)` to
`max(git tag v*)`, that debt turned **every open PR red on the
`Go lint + vet + codegen drift` job simultaneously**, including four that had
nothing to do with storage.

That is the gate working correctly and is worth recording plainly: the gate
**serialises the patch lane.** Merge a `fix:`, and no further PR goes green
until its snapshot lands. On 2026-09-09 that surfaced as four
unrelated PRs (#321-#324) failing one shared job within minutes of two merges.
The remedy is not to weaken the gate — it is to expect the snapshot commit as
part of merging a `fix:`, not as a later chore.

## Schema review for v0.73.2

**`dump.sql` is byte-identical to `v0.73.1/dump.sql`.**

```
$ cmp core/storage/sqlite/testdata/upgrade/v0.73.1/dump.sql \
      core/storage/sqlite/testdata/upgrade/v0.73.2/dump.sql
$ echo $?
0
```

Verified rather than assumed:

```
$ git diff --stat v0.73.1..v0.73.2 -- '*migration*'
(empty)
```

The tag registered no migration, so replaying `v0.73.1`'s database under `v0.73.2`'s
code applies nothing and changes no row. An empty schema diff is the evidence
that this release did not touch storage — worth having on the record precisely
because the alternative (one that silently did) is what this chain exists to
catch.
