# v0.104.1 upgrade snapshot — provenance

Hand-written per the release ritual. `scripts/ci/upgrade-snapshot.sh`
writes only `dump.sql`; this file is the human-authored part.

## How this file was produced

    HOME=$(mktemp -d) KENAZ_HARNESS_ENV=test bash scripts/ci/upgrade-snapshot.sh v0.104.1

Replay mode, `v0.104.0 -> v0.104.1`. Generated 2026-11-12 from
`chore/upgrade-snapshot-v0.104.1` (branched at `310708a0`, main's tip and
the tagged commit), with `GOMODCACHE` / `GOCACHE` / `GOPATH` pointed at the
real caches so the temporary `HOME` didn't re-download modules.

- Tag: `v0.104.1`, cut by a manual `tag-on-merge` dispatch on 2026-11-12
  (run 43351150640) at `310708a0` (PR #488).
- Releases since v0.104.0: #486, #487, #488 (#485 is no-bump).
- Predecessor snapshot: `v0.104.0`
- dump.sql md5: `64273635a86a6166c6ec15f6213c1e07` (byte-identical to
  v0.104.0's, verified with `md5` after generation)

## What changed on the upgrade path

Nothing in `data.db`. No migration landed between v0.104.0 and v0.104.1
(the only change under `core/storage` in that range is the v0.104.0
snapshot itself), so the replay reproduces the predecessor.

#488 adds two durable sync-block codes (`erased_before_cutoff`,
`created_at_in_future`) to the memory outbox. Those live in the memory
store, not `data.db`, and are covered by the memory-sync tests.
