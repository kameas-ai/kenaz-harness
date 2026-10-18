# v0.92.0 upgrade snapshot — provenance

Hand-written per the release ritual. `scripts/ci/upgrade-snapshot.sh`
writes only `dump.sql`; this file is the human-authored part.

## How this file was produced

    HOME=$(mktemp -d) KENAZ_HARNESS_ENV=test bash scripts/ci/upgrade-snapshot.sh v0.92.0

Replay mode, `v0.91.0 -> v0.92.0`. Generated 2026-10-07 from
`chore/v0.92.0-upgrade-snapshot`.

- Tag: `v0.92.0`
- Squash commit: `4a450e00` ("feat(mlsidecar): bake the kenaz-ml engine
  release public key", PR #388)
- Predecessor snapshot: `v0.91.0`

## What changed on the upgrade path

**Zero-delta**: byte-identical to v0.91.0's dump (`cmp`-verified at
generation time). No migrations on this path —
`git diff --name-only v0.91.0..v0.92.0` touches exactly one file,
`core/mlsidecar/release_signing_key.pub`, which bakes the kenaz-ml
engine release public key into the binary. No migration, schema, or
storage-tree files change; the live schema is untouched. The snapshot
keeps the chain level with the newest tag.

## State outside sqlite's purview

The baked public key is a compile-time embedded constant used by
mlsidecar release-signature verification. It persists nothing — not in
sqlite and not in profile-directory files. Do not hunt for missing
migrations.
