# v0.106.3 upgrade snapshot — provenance

Hand-written per the release ritual. `scripts/ci/upgrade-snapshot.sh`
writes only `dump.sql`; this file is the human-authored part.

## How this file was produced

    HOME=$(mktemp -d) KENAZ_HARNESS_ENV=test bash scripts/ci/upgrade-snapshot.sh v0.106.3

Replay mode, `v0.106.2 -> v0.106.3`. Generated 2026-11-15 from
`chore/upgrade-snapshot-v0.106.3` (branched at `2bc930e7`, the tagged
commit), with `GOMODCACHE` / `GOCACHE` / `GOPATH` pointed at the real caches.

- Tag: `v0.106.3`, cut by a manual `tag-on-merge` dispatch on 2026-11-15
  (run 43665032563) at `2bc930e7` (PR #504).
- Releases since v0.106.2: #504 (#503 is no-bump).
- Predecessor snapshot: `v0.106.2`
- dump.sql md5: `ea4d6478c3071bca355928ff19cbf216` (byte-identical to
  v0.106.2's)

## What changed on the upgrade path

Nothing in `data.db`. No migration landed between v0.106.2 and v0.106.3.
#504 binds filesystem grants and queued-prompt release to the location
checked at prompt time. That is in-process prompt state; no stored policy
or grant format changed.
