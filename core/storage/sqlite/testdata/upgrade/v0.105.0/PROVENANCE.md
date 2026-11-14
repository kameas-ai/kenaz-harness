# v0.105.0 upgrade snapshot — provenance

Hand-written per the release ritual. `scripts/ci/upgrade-snapshot.sh`
writes only `dump.sql`; this file is the human-authored part.

## How this file was produced

    HOME=$(mktemp -d) KENAZ_HARNESS_ENV=test bash scripts/ci/upgrade-snapshot.sh v0.105.0

Replay mode, `v0.104.1 -> v0.105.0`. Generated 2026-11-14 from
`chore/upgrade-snapshot-v0.105.0` (branched at `417015f6`, the tagged
commit), with `GOMODCACHE` / `GOCACHE` / `GOPATH` pointed at the real caches
so the temporary `HOME` didn't re-download modules.

- Tag: `v0.105.0`, cut by a manual `tag-on-merge` dispatch on 2026-11-14
  (run 43554583638) at `417015f6` (release roll-up #494 of #490, #491,
  #492, #493).
- Predecessor snapshot: `v0.104.1`
- dump.sql md5: `ea4d6478c3071bca355928ff19cbf216`

## What changed on the upgrade path

One migration: `units/1106-sync-state-server-load-policy` (from #493).

- `unit_sync_state` gains `server_load_policy TEXT NOT NULL DEFAULT ''`, the
  load_policy Fleet last served for a pulled unit. Only a pulled `always`
  makes a team/org unit eager-load into prompts. Existing rows read `''`
  (not eager) until their next pull. The one-time unit cursor reset in the
  same release re-pulls them.
- `harness_migrations` gains the matching applied row (version 1106).

The diff against v0.104.1's dump is exactly those two lines. The change is
additive. `migration_1105_test.go` exercises 1106 against populated
sidecar rows, since no shipped snapshot has `unit_sync_state` rows.

The other release content (#490 step-up, #491 device name, #492 notice
acks) keeps its state in the keychain, in-memory or in JSON files under the
data dir, not in `data.db`.
