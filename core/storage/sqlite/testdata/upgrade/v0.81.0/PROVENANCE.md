# v0.81.0 upgrade snapshot — provenance

Hand-written per the release ritual (CLAUDE.md, "Release ritual: unwired
sweep" → blind spot #3 → release-ritual corollary).
`scripts/ci/upgrade-snapshot.sh` writes only `dump.sql`; this file is the
part a human has to author.

## How this file was produced

    bash scripts/ci/upgrade-snapshot.sh v0.81.0

Replay mode, `v0.80.1 -> v0.81.0`. The script materialised
`testdata/upgrade/v0.80.1/dump.sql` into a fresh `data.db`, ran `Open()`
under tag `v0.81.0`'s code in a throwaway `git worktree` at `e43ad6d0`,
and dumped the result back out. Generated 2026-09-15 from
`chore/v0.81.0-snapshot`, branched off the squash commit itself.

- Tag: `v0.81.0`
- Squash commit: `e43ad6d0` ("feat: v0.81.0 — four missions finished, the
  keyring seam, and the cache that shipped dormant since v0.72", PR #350)
- Predecessor snapshot: `v0.80.1`
- Contents: 799 → 799 lines

## What the replay actually applied

**Nothing — and that is the finding.** `dump.sql` is byte-identical to
`v0.80.1/dump.sql` (verified with `cmp`, not `diff` heuristics). No
migration became newly pending between `v0.80.1` and `v0.81.0`: the four
missions in this release (Z808, NORGX01, Z101, Z505), the keyring seam,
and the capabilities-cache wiring all changed code, not schema. The
`harness_migrations` ledger stays at 58 rows.

## The property this snapshot actually proves

**Tag `v0.81.0`'s `Open()` reads a `v0.80.1` database and mutates
nothing.** The capabilities-cache change in this release is the reason
that is worth proving rather than assuming: `DefaultCache` now takes the
SQLite backend in production for the first time since v0.72, so `Open()`
under the new code touches a table lineage (`llm_capabilities`) that the
previous release's production path never exercised. A byte-identical
round-trip is positive evidence that first-touch is read-compatible and
non-destructive against a really-shipped schema.

The zero-delta also keeps the chain honest: `check-upgrade-snapshot-present.sh`
requires max(snapshot) ≥ max(tag), and a "no migrations this release"
release still owes its link so the *next* release's replay starts from
the right predecessor.

## Caveats for whoever reads this next

- **Do not skip the next snapshot because this one was empty.** The chain
  property is per-tag, not per-schema-change; the gate compares versions,
  not content.
- **This snapshot is immutable from here.**
  `check-upgrade-snapshots-locked.sh` fails on any edit to a committed
  snapshot directory, and `upgrade-snapshot.sh` re-run for this tag must
  reproduce `dump.sql` byte-identically.
- The generator opens a real `storagesqlite.DB`, so producing this file
  appended to `~/.kenaz/harness.log` (finding #56, known and unowned — an
  unsandboxed log sink, not a data hazard).
