# Provenance: `testdata/upgrade/v0.63.2/dump.sql`

**Tag**: `v0.63.2` (commit `a0ddabfa`) — the latest release tag at the time
this branch was cut, and the base commit of `feat/ug` itself.

## How this snapshot was produced

Replay, the normal path (`snapshot(N) = replay(snapshot(N-1), tag N)`):

```bash
bash scripts/ci/upgrade-snapshot.sh v0.63.2
```

which resolved `PREV_TAG=v0.63.1`, created a detached `git worktree` at
`refs/tags/v0.63.2`, materialised `v0.63.1/dump.sql` into a fresh
`data.db`, ran that tag's `storagesqlite.Open` against it, and dumped the
result through `core/storage/sqlite/upgradesnap`.

## Why it was added in review, not at release time

`docs/upgrade-snapshots.md` states that **every release must add a
snapshot**, and notes that the lock gate catches *modification* but nothing
catches *absence*. `v0.63.2` was already tagged when the snapshot mechanism
landed, so the chain shipped stopping one release short of the latest tag —
the exact silent hole the doc warns about, present on the day the doc was
written. Added here so the chain's claim ("a database the previous release
produced still opens under HEAD") is about the **actual** previous release.

## Schema review for v0.63.2

**`dump.sql` is byte-identical to `v0.63.1/dump.sql`.**

```
$ cmp core/storage/sqlite/testdata/upgrade/v0.63.1/dump.sql \
      core/storage/sqlite/testdata/upgrade/v0.63.2/dump.sql
$ echo $?
0
```

That is the correct and expected result: `v0.63.2` registered no new
migration, so replaying `v0.63.1`'s database under its code applies nothing
and changes no row. An empty schema diff is a finding in its own right —
it is the evidence that a patch release did not touch storage.

It also makes this directory the cheapest candidate for pruning under
whatever growth policy spec §10 escalation 4 eventually ratifies (see the
*Snapshot growth* note in `docs/upgrade-snapshots.md`): ~42 KB carrying no
information a reader cannot get from `v0.63.1`. It is kept for now because
the property that matters — *the chain reaches the latest release tag* — is
not expressible without it, and nothing in CI enforces that property yet.
