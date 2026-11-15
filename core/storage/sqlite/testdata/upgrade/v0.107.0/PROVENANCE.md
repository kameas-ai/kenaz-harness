# v0.107.0 upgrade snapshot — provenance

Hand-written per the release ritual. `scripts/ci/upgrade-snapshot.sh`
writes only `dump.sql`; this file is the human-authored part.

## How this file was produced

    HOME=$(mktemp -d) KENAZ_HARNESS_ENV=test bash scripts/ci/upgrade-snapshot.sh v0.107.0

Replay mode, `v0.106.3 -> v0.107.0`. Generated 2026-11-15 from
`chore/upgrade-snapshot-v0.107.0` (branched at `1914c945`, the tagged
commit), with `GOMODCACHE` / `GOCACHE` / `GOPATH` pointed at the real caches.

- Tag: `v0.107.0`, cut by a manual `tag-on-merge` dispatch on 2026-11-15
  (run 43673926083) at `1914c945` (PR #505).
- Releases since v0.106.3: #505 (#506 is no-bump).
- Predecessor snapshot: `v0.106.3`
- dump.sql md5: `ea4d6478c3071bca355928ff19cbf216` (byte-identical to
  v0.106.3's)

## What changed on the upgrade path

Nothing in `data.db`. No migration landed between v0.106.3 and v0.107.0.
#505's new state is JSON under `<dataDir>/fleet/`: `unit_cursor_v2.txt`,
`unit_pulled_index.json` and `unit_resnapshot.json`. `unit_cursor.txt` is
kept for older builds.
