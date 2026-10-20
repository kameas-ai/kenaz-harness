# v0.99.0 upgrade snapshot — provenance

Hand-written per the release ritual. `scripts/ci/upgrade-snapshot.sh`
writes only `dump.sql`; this file is the human-authored part.

## How this file was produced

    HOME=$(mktemp -d) KENAZ_HARNESS_ENV=test bash scripts/ci/upgrade-snapshot.sh v0.99.0

Replay mode, `v0.98.0 -> v0.99.0`. Generated 2026-10-20 from
`chore/upgrade-snapshot-v0.99.0`.

- Tag: `v0.99.0`, cut by a manual `tag-on-merge` dispatch (bump=auto) at
  `129f39b8` ("fix(bash): escape Allow-always patterns in generated Cedar
  policy …", PR #443). The minor bump comes from #310 (`feat:`).
- Releases since v0.98.0: #438, #439, #440, #441, #310, #442, #443.
- Predecessor snapshot: `v0.98.0`
- dump.sql md5: `e552d2504b3f78130b7a6915a2dc7169`

## What changed on the upgrade path

**Zero-delta.** `dump.sql` is byte-identical to v0.98.0's (same md5). No
release in the range adds or edits a `data.db` migration. The only
`migrate`-named change, `core/tools/bash/migrate.go` (#443), writes Cedar
policy files under `<dataDir>/policy/`, not `data.db`.

#441 changes the separate `<dataDir>/ml/activity.db` (schema v2,
`harness_retention_choice`), which is outside this snapshot chain and has
its own fixture under `core/mlproducer/activity/testdata/upgrade/`.

## Checks run on this snapshot

- `go test -run TestUpgradePath ./core/storage/sqlite/`
- `check-upgrade-snapshot-present.sh`
- `check-upgrade-snapshots-locked.sh`
