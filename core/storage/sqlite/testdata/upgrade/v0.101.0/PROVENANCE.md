# v0.101.0 upgrade snapshot — provenance

Hand-written per the release ritual. `scripts/ci/upgrade-snapshot.sh`
writes only `dump.sql`; this file is the human-authored part.

## How this file was produced

    HOME=$(mktemp -d) KENAZ_HARNESS_ENV=test bash scripts/ci/upgrade-snapshot.sh v0.101.0

Replay mode, `v0.100.3 -> v0.101.0`. Generated 2026-11-08 from
`feat/fleet-enroll-identity` (the first PR to hit the
`upgrade-snapshot-present` gate after the tag), with `GOMODCACHE` /
`GOCACHE` pointed at the real caches so the temporary `HOME` didn't
re-download modules.

- Tag: `v0.101.0`, cut by `tag-on-merge` at `f7670f4c` ("feat(fleet): live
  fleet policy push-down for served workbenches", PR #465).
- Releases since v0.100.3: #472 (v0.100.3 snapshot), #465.
- Predecessor snapshot: `v0.100.3`
- dump.sql md5: `64273635a86a6166c6ec15f6213c1e07` (byte-identical to
  v0.100.3's)

## What changed on the upgrade path

Nothing in `data.db`. No migration landed between v0.100.3 and v0.101.0 (the
only change under `core/storage` is the v0.100.3 snapshot itself), so the
replay applies no pending migrations and reproduces the predecessor.

#465 changed fleet-side files the snapshot doesn't cover (`fleet/bundle.json`
and the bundle id/checksum/apply-meta files under `fleet/`), all additive and
exercised by `core/fleet/config_pull_restore_test.go`.
