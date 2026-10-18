# v0.95.0 upgrade snapshot — provenance

Hand-written per the release ritual. `scripts/ci/upgrade-snapshot.sh`
writes only `dump.sql`; this file is the human-authored part.

## How this file was produced

    HOME=$(mktemp -d) KENAZ_HARNESS_ENV=test bash scripts/ci/upgrade-snapshot.sh v0.95.0

Replay mode, `v0.94.0 -> v0.95.0`. Generated 2026-10-09 from
`chore/v0.95.0-upgrade-snapshot`.

- Tag: `v0.95.0`
- Squash commit: `c101ea50` ("feat(settings): remove developer surfaces
  (Flags, Health, Logs); fix-it issue banner", PR #405)
- Predecessor snapshot: `v0.94.0`

## What changed on the upgrade path

**Zero-delta**: byte-identical to v0.94.0's dump (`cmp`-verified at
generation time; md5 `2822a4fb9f40be291508b03aa8e06fc3`). No migrations
on this path.

`git diff --name-only v0.94.0..v0.95.0 -- '*migration*' core/storage
core/session` lists exactly two files, both of them the **v0.94.0
snapshot itself** (`testdata/upgrade/v0.94.0/{PROVENANCE.md,dump.sql}`,
landed after the v0.94.0 tag was cut, so they fall inside this tag range
without being a schema change).

The release itself is frontend-only plus comment-only Go edits
(`core/rpc/bindings.go`, `core/rpc/stream_broker.go`). It removes the
Settings Flags/Health/Logs tabs and adds the Settings issue banner, which
reads the existing migration-drift RPCs. None of it reaches the sqlite
schema.
