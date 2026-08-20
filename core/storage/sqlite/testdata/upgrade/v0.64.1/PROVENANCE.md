# Provenance: `testdata/upgrade/v0.64.1/dump.sql`

**Tag**: `v0.64.1` (commit `88b7a75e`, *"fix(fleet): bake prod realm
defaults into source so prod builds are Configured() (#295)"*) — the
latest release tag at the time this snapshot was generated (2026-08-19).

## How this snapshot was produced

Replay, the normal path (`snapshot(N) = replay(snapshot(N-1), tag N)`):

```bash
bash scripts/ci/upgrade-snapshot.sh v0.64.1
```

which reported `replay mode: v0.64.0 -> v0.64.1`, created a detached
`git worktree` at `refs/tags/v0.64.1` (`HEAD is now at 88b7a75e`),
materialised `v0.64.0/dump.sql` into a fresh `data.db`, ran that tag's
`storagesqlite.Open` against it, and dumped the result through
`core/storage/sqlite/upgradesnap`.

## Why it was added after the release — for the THIRD consecutive release

`docs/upgrade-snapshots.md` states that every release must add a snapshot,
and `CLAUDE.md`'s release ritual repeats it. The record of this hole is now
three releases long, each one documented by the snapshot that came after it:

- **v0.63.2** — its own PROVENANCE.md named the gap in its own words: *"the
  lock gate catches modification but nothing catches absence."*
- **v0.64.0** — the release whose PR title claims *"CI can finally see an
  upgrade"*, and which built the `upgrade-path` job precisely so the v0.63.0
  P0 could not repeat, shipped with no snapshot of its own. Its PROVENANCE.md
  concluded: *"This is now twice. A convention that depends on a human
  remembering it at release time has failed on two consecutive releases."*
- **v0.64.1** — this one. It failed a third time.

Each of those files correctly identified the fix owed — *"a CI check that
fails when `max(testdata/upgrade/v*)` is behind `max(git tag v*)`"* — and
each stopped at recording it. Writing the diagnosis into a document that only
gets read while performing the ritual that keeps being skipped is not a fix.

**This time the gate was written**, in the same change that backfills this
snapshot: `scripts/ci/check-upgrade-snapshot-present.sh`, wired into
`pr.yml`, with a planted-violation proof in
`scripts/ci/gates_can_fail_test.go`. It was authored against this exact
live violation — the gate was run before the backfill and failed with
`newest release tag: v0.64.1 / newest snapshot: v0.64.0`, which is the
observed evidence that it detects the real defect rather than a mocked one.

## Schema review for v0.64.1

**`dump.sql` is byte-identical to `v0.64.0/dump.sql`.**

```
$ cmp core/storage/sqlite/testdata/upgrade/v0.64.0/dump.sql \
      core/storage/sqlite/testdata/upgrade/v0.64.1/dump.sql
$ echo $?
0
```

628 lines, 42,878 bytes, migration high-water
`sessions/0335-search-fts-exclude-tool-rows` — unchanged from `v0.63.1`
onward.

That is the correct and expected result: `v0.64.1` is a single-commit fleet
fix that baked prod realm defaults into source. It registered no migration,
so replaying `v0.64.0`'s database under its code applies nothing and changes
no row. An empty schema diff is a finding in its own right — it is the
evidence that the release did not touch storage.

Same pruning note as `v0.63.2` and `v0.64.0`: this directory carries no
information a reader cannot get from `v0.63.1`. It is kept because the
property that matters — *the chain reaches the latest release tag* — is not
expressible without it. Unlike the two releases before it, that property is
now enforced rather than merely asserted, so the growth-policy escalation in
`docs/upgrade-snapshots.md` becomes a real question: the chain will now grow
by one directory per release, unconditionally, and three of the five present
are byte-identical to each other.

## Verification run

See the commit for the `TestUpgradePath/v0.64.1` output. The subtest must
appear as its own `RUN` line and open a real database — a green suite is not
evidence that a new snapshot is covered, which is the whole reason this
chain exists.

## Independent corroboration (2026-08-20)

`entry-points-and-crash-reporting-01PMZD13` UNIT-3 generated this snapshot
**independently**, in a separate worktree, unaware this one existed (it had
been told the guard was already landed and correctly declined to trust an
unverifiable citation — `docs/` is gitignored and absent from worktrees).

Its `dump.sql` is **byte-identical** to this one (`cmp` exit 0 at merge).
Two separate `upgrade-snapshot.sh v0.64.1` runs, from different base
commits, producing the same bytes is the strongest available evidence that
the replay is deterministic and that this file records the real v0.64.1
schema rather than an artefact of one machine's state.

That mission also implemented the absence check by extending
`check-upgrade-snapshots-locked.sh`. The standalone
`check-upgrade-snapshot-present.sh` was kept instead — one gate, one rule,
matching the repo's convention, and because the lock gate's own header
states it "structurally cannot see absence", which folding absence into it
would falsify. ZD13's duplicate was dropped at merge; nothing else from
that mission was.
