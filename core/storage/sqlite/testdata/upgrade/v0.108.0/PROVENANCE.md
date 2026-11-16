# v0.108.0 upgrade snapshot — provenance

Hand-written per the release ritual. `scripts/ci/upgrade-snapshot.sh`
writes only `dump.sql`; this file is the human-authored part.

## How this file was produced

    HOME=$(mktemp -d) KENAZ_HARNESS_ENV=test bash scripts/ci/upgrade-snapshot.sh v0.108.0

Replay mode, `v0.107.0 -> v0.108.0`. Generated 2026-11-15 from
`fix/main-css-token-and-snapshot-v0.108.0` (branched at `21dd1dbd`, the
tagged commit), with `GOMODCACHE` / `GOCACHE` / `GOPATH` pointed at the
real caches.

- Tag: `v0.108.0` (a prerelease under the #509 flow) at `21dd1dbd` (#510).
  Tag-on-merge run: 43716817717.
- Changes since v0.107.0: #507 (docs), #509, #511, #510 (#508 is no-bump).
- Predecessor snapshot: `v0.107.0`
- dump.sql md5: `ea4d6478c3071bca355928ff19cbf216` (byte-identical to
  v0.107.0's)

## What changed on the upgrade path

Nothing in `data.db`. No migration landed between v0.107.0 and v0.108.0.
#510's starter host policy and #511's design-system adoption do not touch
storage.
