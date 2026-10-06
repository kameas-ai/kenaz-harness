# v0.89.3 upgrade snapshot — provenance

Hand-written per the release ritual. `scripts/ci/upgrade-snapshot.sh`
writes only `dump.sql`; this file is the human-authored part.

## How this file was produced

    HOME=$(mktemp -d) KENAZ_HARNESS_ENV=test bash scripts/ci/upgrade-snapshot.sh v0.89.3

Replay mode, `v0.89.2 -> v0.89.3`. Generated 2026-10-06 from
`chore/v0.89.3-upgrade-snapshot`.

- Tag: `v0.89.3`
- Squash commits on the path: PR #378 ("fix(sessions): the composer
  never resurrects sent text from the persisted draft") and PR #380
  ("fix(fleet): the harness matches the real fleet contract").
- Predecessor snapshot: `v0.89.2`

## What changed on the upgrade path

**Zero-delta**: byte-identical to v0.89.2's dump (diff-verified).
Neither fix ships a migration — frontend draft handling and fleet
client/contract changes only. The snapshot keeps the chain level with
the newest tag.
