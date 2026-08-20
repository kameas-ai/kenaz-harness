# Provenance: `testdata/upgrade/v0.64.0/dump.sql`

**Tag**: `v0.64.0` (commit `55029354`).

## Why this was built here, not on the branch that first needed it

`entry-points-and-crash-reporting-01PMZD13`'s spec.md recorded that a
`v0.64.0` snapshot (`dump.sql` + a hand-written `PROVENANCE.md`) had already
been produced and verified — `TestUpgradePath/v0.64.0` passing — but was
**untracked** in the checkout that wrote the spec, so it existed on one
working tree and in no commit. That checkout's untracked files do not carry
into a fresh `git worktree add`-based worktree (only tracked files do), so
this worktree started with the chain stopping at `v0.63.2` and no `v0.64.0`
directory at all — not even an untracked one. Regenerated here rather than
copied, because there was nothing to copy from.

**By the time this ran, `v0.64.0` was no longer the latest release tag** —
`git tag --sort=-v:refname` returned `v0.64.1` first. UNIT-3's mandate is to
commit the snapshot for whatever `LATEST_TAG` the new gate will actually
compute, and the gate computes it dynamically from git tags — it does not
hardcode `v0.64.0`. `v0.64.0`'s snapshot is committed anyway, immediately
below `v0.63.2` in the chain, so the chain has no gap; `v0.64.1`'s snapshot
(committed alongside this one, see its own `PROVENANCE.md`) is what
satisfies the gate's actual requirement.

## How this snapshot was produced

Replay, the normal path (`snapshot(N) = replay(snapshot(N-1), tag N)`):

```bash
bash scripts/ci/upgrade-snapshot.sh v0.64.0
```

which resolved `PREV_TAG=v0.63.2` (the highest committed snapshot below
`v0.64.0` present in this worktree), created a detached `git worktree` at
`refs/tags/v0.64.0` (`HEAD is now at 55029354`), materialised
`v0.63.2/dump.sql` into a fresh `data.db`, ran that tag's
`storagesqlite.Open` against it, and dumped the result through
`core/storage/sqlite/upgradesnap`. The temporary worktree was removed by the
script's own `trap cleanup EXIT` (confirmed via `git worktree list` showing
no leftover entry afterward).

## Schema review for v0.64.0

**`dump.sql` is byte-identical to `v0.63.2/dump.sql`.**

```
$ diff core/storage/sqlite/testdata/upgrade/v0.63.2/dump.sql \
       core/storage/sqlite/testdata/upgrade/v0.64.0/dump.sql
(no output)
```

Consistent with `v0.64.0`'s own headline (onboarding delivery, self-update
installs, policy enforcement, CI upgrade-path visibility) being none of
those things about storage: no new `harness_migrations` row, no schema
change. An empty schema diff between adjacent snapshots is itself the
verification that a release did not touch storage — recorded rather than
asserted, per the same reasoning `v0.63.2/PROVENANCE.md` gives for its own
empty diff against `v0.63.1`.
