# v0.106.2 upgrade snapshot — provenance

Hand-written per the release ritual. `scripts/ci/upgrade-snapshot.sh`
writes only `dump.sql`; this file is the human-authored part.

## How this file was produced

    HOME=$(mktemp -d) KENAZ_HARNESS_ENV=test bash scripts/ci/upgrade-snapshot.sh v0.106.2

Replay mode, `v0.106.1 -> v0.106.2`. Generated 2026-11-14 from
`chore/upgrade-snapshot-v0.106.2` (branched at `bdf50b8a`, the tagged
commit), with `GOMODCACHE` / `GOCACHE` / `GOPATH` pointed at the real caches.

- Tag: `v0.106.2`, cut by a manual `tag-on-merge` dispatch on 2026-11-14
  (run 43650546842) at `bdf50b8a` (PR #502).
- Releases since v0.106.1: #502 (#501 is no-bump).
- Predecessor snapshot: `v0.106.1`
- dump.sql md5: `ea4d6478c3071bca355928ff19cbf216` (byte-identical to
  v0.106.1's)

## What changed on the upgrade path

Nothing in `data.db`. No migration landed between v0.106.1 and v0.106.2.
#502 adds one settings.json key (`fleetDeviceNameNeutral`), which is not in
`data.db`.
