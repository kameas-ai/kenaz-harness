# v0.83.0 upgrade snapshot — provenance

Hand-written per the release ritual (CLAUDE.md, blind spot #3 →
release-ritual corollary). `scripts/ci/upgrade-snapshot.sh` writes only
`dump.sql`; this file is the part a human has to author.

## How this file was produced

    bash scripts/ci/upgrade-snapshot.sh v0.83.0

Replay mode, `v0.82.1 -> v0.83.0`. The script materialised
`testdata/upgrade/v0.82.1/dump.sql` into a fresh `data.db`, ran `Open()`
under tag `v0.83.0`'s code in a throwaway `git worktree` at `59b51679`,
and dumped the result back out. Generated 2026-09-27 from
`chore/v0.83.0-snapshot`, branched off the squash commit itself.

- Tag: `v0.83.0`
- Squash commit: `59b51679` ("feat: v0.83.0 — engineering
  workbenches, real usage reporting and knowledge sites", PR #356)
- Predecessor snapshot: `v0.82.1`
- Contents: 799 → 799 lines

## What the replay actually applied

**Nothing.** Byte-identical to `v0.82.1/dump.sql` (`cmp`-verified) —
fourth consecutive zero-delta. The release's review of record verified
independently that the branch introduces **no migration**: the documents
feature persists through the pre-existing generic `units` table
(`units.KindDoc`, previously unused, no schema change), `UpdateAtVersion`
is an app-level CAS over the existing version column, and the fleet
usage pipeline's state is in-memory + OTLP egress, not sqlite. The
`harness_migrations` ledger stays at 58 rows.

## The property this snapshot actually proves

**Migration-selection stability, and only that** — tag `v0.83.0`'s
`Open()` reads a `v0.82.1` database, selects zero pending migrations,
mutates nothing. It proves nothing about the usage pipeline, documents,
served agents, or any other runtime path (the generator calls only
`storagesqlite.Open`; scoping rule per the v0.81.0 snapshot review).

## Caveats

- Four consecutive zero-delta snapshots do NOT make the ritual
  skippable; the chain is per-tag, and `units.KindDoc` going live means
  the next migration touching `units` will replay against REAL document
  rows for the first time — this file is where that evidence will come
  from.
- Immutable from here (`check-upgrade-snapshots-locked.sh`).
- Generating this appended to `~/.kenaz/harness.log` (finding #56).
