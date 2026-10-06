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
