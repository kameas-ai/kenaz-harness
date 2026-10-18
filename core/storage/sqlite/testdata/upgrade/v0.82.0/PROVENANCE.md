# v0.82.0 upgrade snapshot — provenance

Hand-written per the release ritual (CLAUDE.md, "Release ritual: unwired
sweep" → blind spot #3 → release-ritual corollary).
`scripts/ci/upgrade-snapshot.sh` writes only `dump.sql`; this file is the
part a human has to author.

## How this file was produced

    bash scripts/ci/upgrade-snapshot.sh v0.82.0

Replay mode, `v0.81.0 -> v0.82.0`. The script materialised
`testdata/upgrade/v0.81.0/dump.sql` into a fresh `data.db`, ran `Open()`
under tag `v0.82.0`'s code in a throwaway `git worktree` at `773d3b06`,
and dumped the result back out. Generated 2026-09-16 from
`chore/v0.82.0-snapshot`, branched off the squash commit itself.

- Tag: `v0.82.0`
- Squash commit: `773d3b06` ("feat: v0.82.0 — risk-rated autonomy live
  end to end", PR #352)
- Predecessor snapshot: `v0.81.0`
- Contents: 799 → 799 lines

## What the replay actually applied

**Nothing.** `dump.sql` is byte-identical to `v0.81.0/dump.sql`
(`cmp`-verified). No migration became newly pending between `v0.81.0`
and `v0.82.0`; the `harness_migrations` ledger stays at 58 rows. This
matches the release's own persistence claims, verified independently at
review: the WP05 rater cache is in-process memory only, and the WP10
audit additions are optional JSON fields inside the existing
`eventlog.Row.Payload` blob column — no schema change anywhere.

## The property this snapshot actually proves

**Migration-selection stability, and only that**: tag `v0.82.0`'s
`Open()` reads a `v0.81.0` database, selects zero pending migrations,
and mutates nothing. It proves nothing about the risk gate, the rater
cache, or any other runtime path — the snapshot generator calls only
`storagesqlite.Open` and dumps (see the v0.81.0 PROVENANCE.md for the
review that pinned this scoping rule; it applies verbatim here).

The chain link is owed regardless of content:
`check-upgrade-snapshot-present.sh` compares versions, not deltas, and
the next release's replay needs this predecessor.

## Caveats for whoever reads this next

- **Two consecutive zero-delta snapshots (v0.81.0, v0.82.0) do not make
  the ritual skippable.** The next migration-bearing release will replay
  from this file; if it is missing, that release's coverage silently
  shrinks to nothing.
- **This snapshot is immutable from here.**
  `check-upgrade-snapshots-locked.sh` fails on any edit to a committed
  snapshot directory, and `upgrade-snapshot.sh` re-run for this tag must
  reproduce `dump.sql` byte-identically.
- The generator opens a real `storagesqlite.DB`, so producing this file
  appended to `~/.kenaz/harness.log` (finding #56, known and unowned).
