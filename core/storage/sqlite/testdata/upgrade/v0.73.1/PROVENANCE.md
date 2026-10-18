# Provenance: `testdata/upgrade/v0.73.1/dump.sql`

**Tag**: `v0.73.1` (commit `2b376348`) — the newest release tag at the time this
snapshot was generated (2026-09-09).

## How this snapshot was produced

Replay, the normal path (`snapshot(N) = replay(snapshot(N-1), tag N)`):

```bash
bash scripts/ci/upgrade-snapshot.sh v0.73.1
```

which reported `replay mode: v0.73.0 -> v0.73.1`, created a detached
`git worktree` at `refs/tags/v0.73.1` (`HEAD is now at 2b376348`), materialised
`v0.73.0/dump.sql` into a fresh `data.db` under
`$TMPDIR/upgrade-snapshot-3086397882/`, ran that tag's `storagesqlite.Open`
against it, and dumped the result through `core/storage/sqlite/upgradesnap`.

## Why it was added after the release — and what caught it this time

`v0.73.1` was cut automatically by `tag-on-merge.yml` when PR #313 squash-merged
with a `fix:` subject. The release ritual's snapshot step did not follow, so the
chain stopped one release short — the same hole `v0.63.2` and `v0.64.0` each
recorded opening in their own PROVENANCE files, on three consecutive releases.

**The difference this time is that nobody had to notice.**
`check-upgrade-snapshot-present.sh` — written for exactly this, after the
convention alone had failed three times — failed the `Go lint + vet + codegen
drift` job on PR #315 with:

```
[upgrade-snapshot-present] FAIL: the upgrade-snapshot chain is behind the newest release tag.
  newest release tag: v0.73.1
  newest snapshot:    v0.73.0
```

It blocked an unrelated PR, which is the point: the gate does not depend on
anyone remembering at release time. This file exists because the gate fired,
not because the ritual was followed.

## Schema review for v0.73.1

**`dump.sql` is byte-identical to `v0.73.0/dump.sql`.**

```
$ cmp core/storage/sqlite/testdata/upgrade/v0.73.0/dump.sql \
      core/storage/sqlite/testdata/upgrade/v0.73.1/dump.sql
$ echo $?
0
```

763 lines, 50,796 bytes; migration high-water `sessions/0340-scheduled-chat-runs-created-by`.

That is the correct and expected result, and it was verified rather than
assumed. `v0.73.0..v0.73.1` contains exactly one non-snapshot commit —
`2b376348 fix(chat): token and context readouts update live instead of freezing
for the whole turn (#313)` — and

```
$ git diff --stat v0.73.0..v0.73.1 -- '*migration*'
(empty)
```

confirms it touched no migration file. A chat/frontend fix registers no
migration, so replaying `v0.73.0`'s database under `v0.73.1`'s code applies
nothing and changes no row.

**An empty schema diff is a finding in its own right** — it is the evidence that
a release did not touch storage, which is worth having on the record precisely
because the alternative (a release that silently *did*) is the failure mode this
chain exists to catch. Same note as `v0.63.2` and `v0.64.0`: this directory
carries no information a reader cannot get from `v0.73.0`, and is a cheap
candidate under whatever growth policy the escalation in
`docs/upgrade-snapshots.md` eventually ratifies. It is kept because the property
that matters — *the chain reaches the newest release tag* — is not expressible
without it, and is now enforced.
