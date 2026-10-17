# v0.98.0 upgrade snapshot — provenance

Hand-written per the release ritual. `scripts/ci/upgrade-snapshot.sh`
writes only `dump.sql`; this file is the human-authored part.

## How this file was produced

    HOME=$(mktemp -d) KENAZ_HARNESS_ENV=test bash scripts/ci/upgrade-snapshot.sh v0.98.0

Replay mode, `v0.97.0 -> v0.98.0`. Generated 2026-10-17 from
`chore/upgrade-snapshot-v0.98.0`.

- Tag: `v0.98.0`, cut by a manual `tag-on-merge` dispatch (bump=auto) at
  `71e45602` ("fix(settings): the in-memory settings store validates
  exactly like settings.json", PR #437). The minor bump comes from the
  roll-up #435 (`feat:`) and #428 (`feat:`).
- Releases since v0.97.0: #427, #428, #421, #430, #415, #422, #435 (which
  rolled up #419, #423, #424, #425, #429, #431, #432, #433, #434), #436,
  #437.
- Predecessor snapshot: `v0.97.0`
- dump.sql md5: `e552d2504b3f78130b7a6915a2dc7169`

## What changed on the upgrade path

**Zero-delta.** `dump.sql` is byte-identical to v0.97.0's (same md5). No
release in the range adds or edits a `data.db` migration
(`git diff v0.97.0..v0.98.0` touches no migration source).

The new local ML activity store (#429, ml-producer-01MLPRD01 WP11) is a
**separate** sqlite file, `<dataDir>/ml/activity.db` (DDL in
`docs/telemetry/ml-activity-db.sql`), not part of `data.db`, so it is
outside this snapshot chain. It starts empty on an upgraded install,
the same as on a fresh one.

## Checks run on this snapshot

- `go test -run TestUpgradePath ./core/storage/sqlite/`
- `check-upgrade-snapshot-present.sh`
- `check-upgrade-snapshots-locked.sh`
