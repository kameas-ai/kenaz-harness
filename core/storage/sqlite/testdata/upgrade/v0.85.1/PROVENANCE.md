# v0.85.1 upgrade snapshot — provenance

Hand-written per the release ritual (CLAUDE.md, blind spot #3 →
release-ritual corollary). `scripts/ci/upgrade-snapshot.sh` writes only
`dump.sql`; this file is the part a human has to author.

## How this file was produced

    bash scripts/ci/upgrade-snapshot.sh v0.85.1

Replay mode, `v0.85.0 -> v0.85.1`. The script materialised
`testdata/upgrade/v0.85.0/dump.sql` into a fresh `data.db`, ran
`Open()` under tag `v0.85.1`'s code in a throwaway `git worktree`,
and dumped the result back out. Generated 2026-10-03 from
`chore/v0.85.1-upgrade-snapshot`, branched off the squash commit.

- Tag: `v0.85.1`
- Squash commit: `ccff41f6` ("fix(mlsidecar): run the install
  current+record flip under the spawn lock", PR #362)
- Predecessor snapshot: `v0.85.0`

## What changed on the upgrade path

**Zero-delta**: `dump.sql` is byte-identical to v0.85.0's (verified
with `diff`). v0.85.1 ships no migrations — the patch is the M1
cross-client install-flip serialization in `core/mlsidecar`, which
touches the shared ML root's files, not the harness database. The
snapshot exists so the chain stays unbroken and
`check-upgrade-snapshot-present.sh` keeps covering the next release.
