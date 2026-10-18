# v0.89.4 upgrade snapshot — provenance

Hand-written per the release ritual. `scripts/ci/upgrade-snapshot.sh`
writes only `dump.sql`; this file is the human-authored part.

## How this file was produced

    HOME=$(mktemp -d) KENAZ_HARNESS_ENV=test bash scripts/ci/upgrade-snapshot.sh v0.89.4

Replay mode, `v0.89.3 -> v0.89.4`. Generated 2026-10-06 from
`chore/v0.89.3-upgrade-snapshot` (one PR carries both the v0.89.3 and
v0.89.4 snapshots — the two tags were cut by back-to-back merges, #378
and #380, before the first snapshot PR landed).

- Tags: `v0.89.3` + `v0.89.4` (PRs #378, #380 in either order per
  tag-on-merge sequencing)
- Predecessor snapshot: `v0.89.3` (sibling directory in this PR)

## What changed on the upgrade path

**Zero-delta**: byte-identical to v0.89.3's dump (diff-verified).
No migrations on this path. The snapshot keeps the chain level with the
newest tag.

## Backfill note (2026-10-06)

The `dump.sql` this file describes was never committed: PR #382 landed
this PROVENANCE.md alone, and nothing noticed — the presence gate
(`check-upgrade-snapshot-present.sh`) keyed on the DIRECTORY name and
`TestUpgradePath` silently skipped dump-less directories, so the chain
actually stopped at v0.89.3 while the gate reported v0.89.4. Caught by
the v0.90.0 release review. The dump was backfilled 2026-10-06 by
re-running the command above; the regenerated dump was re-verified
byte-identical to v0.89.3's (`cmp`), confirming the zero-delta claim.
The same PR hardens the gate and the test so a provenance-only
directory now fails both.
