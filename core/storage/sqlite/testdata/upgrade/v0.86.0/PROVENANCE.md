# v0.86.0 upgrade snapshot — provenance

Hand-written per the release ritual. `scripts/ci/upgrade-snapshot.sh`
writes only `dump.sql`; this file is the human-authored part.

## How this file was produced

    bash scripts/ci/upgrade-snapshot.sh v0.86.0

Replay mode, `v0.85.2 -> v0.86.0`. Generated 2026-10-04 from
`chore/v0.86.0-upgrade-snapshot`.

- Tag: `v0.86.0`
- Squash commit: `3cc68996` ("feat(mlsidecar): engine publication seams —
  signer, baked anchor, build-time pin, bounded downloads", PR #366)
- Predecessor snapshot: `v0.85.2`

## What changed on the upgrade path

**Zero-delta**: byte-identical to v0.85.2's dump (diff-verified).
v0.86.0 ships no migrations — the feature is the engine-publication
seams (signer CLI, baked trust anchor, build-time pin, bounded
downloads), none of which touches the harness database. The snapshot
keeps the chain level with the newest tag.
