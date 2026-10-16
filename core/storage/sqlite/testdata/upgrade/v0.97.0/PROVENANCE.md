# v0.97.0 upgrade snapshot — provenance

Hand-written per the release ritual. `scripts/ci/upgrade-snapshot.sh`
writes only `dump.sql`; this file is the human-authored part.

## How this file was produced

    HOME=$(mktemp -d) KENAZ_HARNESS_ENV=test bash scripts/ci/upgrade-snapshot.sh v0.97.0

Replay mode, `v0.96.0 -> v0.97.0`. Generated 2026-10-15 from
`chore/upgrade-snapshot-v0.97.0`.

- Tag: `v0.97.0`, cut by a manual `tag-on-merge` dispatch (bump=auto) at
  `bfd52c22` ("fix(settings): show deferrable legal approvals as 'Accept
  by <date>' warnings", PR #414). The minor bump comes from #411 (`feat:`).
- Releases since v0.96.0: #412, #411, #413, #416, #418, #414.
- Predecessor snapshot: `v0.96.0`
- dump.sql md5: `e552d2504b3f78130b7a6915a2dc7169`

## What changed on the upgrade path

**Not zero-delta.** One additive migration from a new owner block:

- `model-calls/1800-model-calls` (owner block `model-calls`, 1800–1899,
  `core/modelcalls/migrations.go`; llm-call-telemetry-01LLMTEL01 WP02):
  creates `model_calls` plus indexes `idx_model_calls_response_id`,
  `idx_model_calls_session_started`, `idx_model_calls_started` and the
  unique `idx_model_calls_span_id`.

The migration is purely additive and touches no existing table, so the new
table starts empty on an upgraded install. The diff against v0.96.0's dump
is exactly the table, its four indexes and the ledger row. No sessions
migration changed. The upgrade probe `assertModelCallsTableMigrated` in
`TestUpgradePath` already existed (added ahead of this snapshot), and
`model_calls` is in its changed-tables set.

#418 (retained context seeds) and #416 / #414 change keychain and wire
behaviour only. They have no schema effect.

## Checks run on this snapshot

- `go test -run TestUpgradePath ./core/storage/sqlite/` passes.
- `check-upgrade-snapshot-present.sh` reports OK (the chain reaches v0.97.0).
- `check-upgrade-snapshots-locked.sh` is clean (116 files).
