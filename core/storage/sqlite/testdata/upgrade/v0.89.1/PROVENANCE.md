# v0.89.1 upgrade snapshot — provenance

Hand-written per the release ritual. `scripts/ci/upgrade-snapshot.sh`
writes only `dump.sql`; this file is the human-authored part.

## How this file was produced

    HOME=$(mktemp -d) KENAZ_HARNESS_ENV=test bash scripts/ci/upgrade-snapshot.sh v0.89.1

Replay mode, `v0.89.0 -> v0.89.1`. Generated 2026-10-05 from
`chore/v0.89.1-upgrade-snapshot`.

- Tag: `v0.89.1`
- Squash commit: PR #374 ("fix(settings): Branch Advisor pane head +
  stale sidecar-probe comment")
- Predecessor snapshot: `v0.89.0`

## What changed on the upgrade path

**Zero-delta**: byte-identical to v0.89.0's dump (diff-verified).
v0.89.1 ships no migrations — the patch is a Settings section-head fix
plus a resolved stale comment. The snapshot keeps the chain level with
the newest tag.
