# Provenance: `testdata/upgrade/v0.64.0/dump.sql`

**Tag**: `v0.64.0` (commit `55029354`) — the latest release tag at the time
this snapshot was generated (2026-08-18).

## How this snapshot was produced

Replay, the normal path (`snapshot(N) = replay(snapshot(N-1), tag N)`):

```bash
bash scripts/ci/upgrade-snapshot.sh v0.64.0
```

which reported `replay mode: v0.63.2 -> v0.64.0`, created a detached
`git worktree` at `refs/tags/v0.64.0` (`HEAD is now at 55029354`),
materialised `v0.63.2/dump.sql` into a fresh `data.db`, ran that tag's
`storagesqlite.Open` against it, and dumped the result through
`core/storage/sqlite/upgradesnap`.

## Why it was added after the release, not at release time — again

`docs/upgrade-snapshots.md` states that **every release must add a
snapshot**, and `CLAUDE.md`'s release ritual repeats it. `v0.63.2`'s own
PROVENANCE.md already recorded this hole opening once, in its own words:
*"the lock gate catches modification but nothing catches absence"*, and
*"the chain shipped stopping one release short of the latest tag — the exact
silent hole the doc warns about, present on the day the doc was written."*

**It recurred on the very next release.** `v0.64.0` — the release whose PR
title claims *"CI can finally see an upgrade"*, and which built the
`upgrade-path` job precisely so the v0.63.0 P0 could not repeat — shipped
with no snapshot of its own. From the moment it was tagged until this file
was written, `TestUpgradePath` covered nothing newer than `v0.63.2`, and it
passed the whole time. Every user upgrading from `v0.64.0` to the next
release would have traversed a migration path no test had ever exercised.

Found on 2026-08-18 by the coverage-gap sweep (finding `CT-3`), which swept
`scripts/ci/` — a directory the main 16-cluster sweep's `core/*/` and
`frontend/src/*/` globs did not reach.

**This is now twice.** A convention that depends on a human remembering it at
release time has failed on two consecutive releases. The gate owed here is
not another reminder in a doc: it is a CI check that fails when
`max(testdata/upgrade/v*)` is behind `max(git tag v*)`. Absence, not
modification — `check-upgrade-snapshots-locked.sh` covers the latter and
structurally cannot see the former.

## Schema review for v0.64.0

**`dump.sql` is byte-identical to `v0.63.2/dump.sql`.**

```
$ cmp core/storage/sqlite/testdata/upgrade/v0.63.2/dump.sql \
      core/storage/sqlite/testdata/upgrade/v0.64.0/dump.sql
$ echo $?
0
```

628 lines, 42,878 bytes, migration high-water `sessions/0335` — unchanged
from `v0.63.1` onward.

That is the correct and expected result: `v0.64.0` registered no new
migration, so replaying `v0.63.2`'s database under its code applies nothing
and changes no row. An empty schema diff is a finding in its own right — it
is the evidence that a feature release did not touch storage, which for a
release of this size is worth having on the record.

Same pruning note as `v0.63.2`: this directory carries no information a
reader cannot get from `v0.63.1`, and is the cheapest candidate under
whatever growth policy the escalation in `docs/upgrade-snapshots.md`
eventually ratifies. It is kept because the property that matters — *the
chain reaches the latest release tag* — is not expressible without it, and
**nothing in CI enforces that property yet.**

## Verification run

```
$ go test ./core/storage/sqlite/ -run Upgrade -count=1 -v
=== RUN   TestUpgradePath/v0.64.0
--- PASS
ok  github.com/kameas-ai/kenaz-harness/core/storage/sqlite  0.587s
```

Checked that the subtest genuinely ran rather than the suite merely staying
green: `TestUpgradePath/v0.64.0` appears as its own `RUN`/`CONT` pair and
opens a real database under
`/T/TestUpgradePathv0.64.0*/00{1,2}/data.db`. A green suite is not evidence
that a new snapshot is covered — that distinction is the whole reason this
chain exists.
