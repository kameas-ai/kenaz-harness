# Provenance: `testdata/upgrade/v0.65.1/dump.sql`

**Tag**: `v0.65.1` — cut automatically by `tag-on-merge.yml` when PR #300
(`fix(ci): gate the graph-write seam, and stop the snapshot dumper
freezing its own bugs`) squash-merged. A `fix:` prefix is a patch bump, so
a CI-only change produced a real release tag.

## How this snapshot was produced

```bash
bash scripts/ci/upgrade-snapshot.sh v0.65.1
```

Replay from `v0.65.0`, using HEAD's generator per the rule PR #300 itself
established: the tag supplies the database STATE, HEAD supplies the dump
FORMAT.

## Why it was missing, and what caught it

**The gate caught it. Twice in one day, on consecutive releases.**

`check-upgrade-snapshot-present.sh` shipped in v0.65.0. It fired
immediately on v0.65.0 itself (backfilled at the time), and then again
here on v0.65.1:

```
[upgrade-snapshot-present] FAIL: the upgrade-snapshot chain is behind the newest release tag.
  newest release tag: v0.65.1
  newest snapshot:    v0.65.0
```

The second catch is the more instructive one, because of *how* it was
nearly missed. The integrator ran the full gate sweep on
`release/v0.66.0` and reported **37/37 passing** — against a stale local
tag list, before fetching. `v0.65.1` had been cut minutes earlier by
`tag-on-merge`. An independent adversarial review of the branch fetched
tags first, saw the failure, and reported it as a P1.

That is worth recording plainly: the gate was correct, the local run was
correct *about the tags it could see*, and the discrepancy was invisible
without `git fetch --tags`. **A gate that compares against remote tags is
only as current as the last fetch.** CI fetches tags explicitly
(`pr.yml`'s "fetch tags" step exists for exactly this); a local sweep does
not, and will under-report.

There is a second lesson about the tag itself. Nobody intended v0.65.1 —
it exists because a CI-hygiene PR used the `fix:` prefix, which
`tag-on-merge.yml` classifies as a patch release. Per `CLAUDE.md`'s own
prefix table that is arguably correct (the PR fixed a gate that reported
OK for a real bypass, which is observable wrong behaviour), but it means
routine CI work mints release tags, each of which then owes a snapshot.

## Schema review for v0.65.1

**`dump.sql` is byte-identical to `v0.65.0/dump.sql`.**

51 ledger rows, high-water `event-log/0105-saved-audit-queries`, ASCII,
zero non-printable bytes. Correct and expected: v0.65.1 contains only
CI-script and test changes plus a snapshot backfill — no migration. An
empty schema diff is itself the evidence that a release did not touch
storage.

Note this directory is byte-identical to its predecessor and is kept only
because the property that matters — *the chain reaches the newest release
tag* — is not expressible without it. That property is now enforced rather
than remembered.

## Verification run

`TestUpgradePath` must show `v0.65.1` as its own `RUN`/`PASS` pair and
open a real database. A green suite is not evidence that a new snapshot is
covered — the distinction this chain exists for.
