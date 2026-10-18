# v0.91.0 upgrade snapshot — provenance

Hand-written per the release ritual. `scripts/ci/upgrade-snapshot.sh`
writes only `dump.sql`; this file is the human-authored part.

## How this file was produced

    HOME=$(mktemp -d) KENAZ_HARNESS_ENV=test bash scripts/ci/upgrade-snapshot.sh v0.91.0

Replay mode, `v0.90.0 -> v0.91.0`. Generated 2026-10-06 from
`chore/v0.91.0-upgrade-snapshot`.

- Tag: `v0.91.0`
- Squash commit: `e19a2d0f` ("feat: v0.91.0 — fleet wire-contract
  conformance and the mandated-items envelope", PR #386)
- Predecessor snapshot: `v0.90.0`

## What changed on the upgrade path

**Zero-delta**: byte-identical to v0.90.0's dump (`cmp`-verified at
generation time). No migrations on this path —
`git diff --name-only v0.90.0..v0.91.0` touches no migration or schema
files; the only storage-tree changes are the v0.89.4/v0.90.0 snapshot
testdata and `upgrade_path_test.go` (the snapshot-gate dump.sql
hardening from PR #385), none of which alter the live schema. The
snapshot keeps the chain level with the newest tag.

## State outside sqlite's purview

The v0.91.0 conformance work persists its state in **files**, not
sqlite — do not hunt for missing migrations:

- `fleet/wire_id_salt`
- `fleet/bundle_apply_meta.json`
- `mandated_applied.json`

These are file-based sidecar state under the profile directory; the
empty-turn fix in this release touches no storage at all.
