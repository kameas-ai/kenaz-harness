# v0.104.0 upgrade snapshot — provenance

Hand-written per the release ritual. `scripts/ci/upgrade-snapshot.sh`
writes only `dump.sql`; this file is the human-authored part.

## How this file was produced

    HOME=$(mktemp -d) KENAZ_HARNESS_ENV=test bash scripts/ci/upgrade-snapshot.sh v0.104.0

Replay mode, `v0.103.0 -> v0.104.0`. Generated 2026-11-12 from
`chore/upgrade-snapshot-v0.104.0` (branched at `b2fdf962`, main's tip and
the tagged commit), with `GOMODCACHE` / `GOCACHE` / `GOPATH` pointed at the
real caches so the temporary `HOME` didn't re-download modules.

- Tag: `v0.104.0`, cut by a manual `tag-on-merge` dispatch on 2026-11-12
  (run 43286931719) at `b2fdf962` (PR #480).
- Releases since v0.103.0: #482, #483, #481, #471, #480 (#478 and #479 are
  no-bump).
- Predecessor snapshot: `v0.103.0`
- dump.sql md5: `64273635a86a6166c6ec15f6213c1e07` (byte-identical to
  v0.103.0's, verified with `md5` after generation)

## What changed on the upgrade path

Nothing in `data.db`. No migration landed between v0.103.0 and v0.104.0
(the only change under `core/storage` in that range is the v0.103.0
snapshot itself), so the replay reproduces the predecessor.

#471 changes memory state (account stamps, erase cutoff, sync state),
which lives in the memory store, not `data.db`. Its upgrade path is covered
by `core/fleet/testdata/memory/upgrade/v0.100.2/` and the memory-sync
tests, not by this snapshot.
