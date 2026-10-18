# v0.96.0 upgrade snapshot — provenance

Hand-written per the release ritual. `scripts/ci/upgrade-snapshot.sh`
writes only `dump.sql`; this file is the human-authored part.

## How this file was produced

    HOME=$(mktemp -d) KENAZ_HARNESS_ENV=test bash scripts/ci/upgrade-snapshot.sh v0.96.0

Replay mode, `v0.95.0 -> v0.96.0`. Generated 2026-10-10 from
`chore/v0.96.0-upgrade-snapshot`.

- Tag: `v0.96.0`
- Squash commit: `3f6445dc` ("feat: v0.96.0 — harness ML producer (agent
  activity to Fleet's ML lane), approvals hub, org exclusions", PR #407)
- Predecessor snapshot: `v0.95.0`
- dump.sql md5: `2e0b5b3950d0df6ce9adcb2846cbae30`

## What changed on the upgrade path

**Not zero-delta.** One additive migration from a new owner block:

- `ml-producer/1700-ml-outbox-and-tasks` (owner block `ml-producer`,
  1700–1799, `core/mlproducer/mlstore/migrations.go`): creates `ml_outbox`
  (AUTOINCREMENT `seq`, so device event ids are never reused after a purge),
  `ml_tasks`, and index `idx_ml_tasks_open_last_active`.

The migration is purely additive (`CREATE TABLE IF NOT EXISTS` /
`CREATE INDEX IF NOT EXISTS`). It touches no existing table, and both new
tables start empty on an upgraded install. The diff against v0.95.0's dump
is exactly those three CREATE statements plus the ledger row. No sessions
migration changed; the sessions-family pins are untouched. Upgrade coverage
for it already exists in
`core/storage/sqlite/ml_producer_upgrade_test.go` (from the v0.94.0
snapshot) and the `assertMLProducerTablesMigrated` probe in
`TestUpgradePath`.
