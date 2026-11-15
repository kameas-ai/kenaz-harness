# v0.106.0 upgrade snapshot — provenance

Hand-written per the release ritual. `scripts/ci/upgrade-snapshot.sh`
writes only `dump.sql`; this file is the human-authored part.

## How this file was produced

    HOME=$(mktemp -d) KENAZ_HARNESS_ENV=test bash scripts/ci/upgrade-snapshot.sh v0.106.0

Replay mode, `v0.105.0 -> v0.106.0`. Generated 2026-11-14 from
`chore/upgrade-snapshot-v0.106.0` (branched at `b4dec8dd`, the tagged
commit), with `GOMODCACHE` / `GOCACHE` / `GOPATH` pointed at the real caches.

- Tag: `v0.106.0`, cut by a manual `tag-on-merge` dispatch on 2026-11-14
  (run 43564229528) at `b4dec8dd` (PR #495).
- Releases since v0.105.0: #495 (#496 is no-bump).
- Predecessor snapshot: `v0.105.0`
- dump.sql md5: `ea4d6478c3071bca355928ff19cbf216` (byte-identical to
  v0.105.0's)

## What changed on the upgrade path

Nothing in `data.db`. No migration landed between v0.105.0 and v0.106.0
(the only change under `core/storage` is the v0.105.0 snapshot itself).
#495's new state is the context share ledger at
`<dataDir>/fleet/context_shares.json`, not `data.db`.
