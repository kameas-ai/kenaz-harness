# v0.93.4 upgrade snapshot — provenance

Hand-written per the release ritual. `scripts/ci/upgrade-snapshot.sh`
writes only `dump.sql`; this file is the human-authored part.

## How this file was produced

    HOME=$(mktemp -d) KENAZ_HARNESS_ENV=test bash scripts/ci/upgrade-snapshot.sh v0.93.4

Replay mode, `v0.93.3 -> v0.93.4`. Generated 2026-10-09 from
`chore/v0.93.4-upgrade-snapshot`.

- Tag: `v0.93.4`
- Squash commit: `fac2a79f` ("fix: dogfood 2026-10-08 round 2 —
  scheduled-chat outcomes, usage totals, MCP boot servers, slash routing,
  cancel", PR #401)
- Predecessor snapshot: `v0.93.3`

## What changed on the upgrade path

**Not zero-delta.** `dump.sql` hashes `317340793a882e1afa75076b7c7832f6`
(v0.93.3: `3ae89985bd55a3699bcfea0c3ed08f8c`). One migration landed in
this tag range:

- `sessions/0345-scheduled-chat-history-model-cost`
  (`core/session/migrations_scheduled_chat_history_model_cost.go`,
  version 345, ledger row 71): adds `model TEXT NOT NULL DEFAULT ''` and
  `cost_usd REAL NOT NULL DEFAULT 0` to `scheduled_chat_run_history`.
  Additive, guarded by `pragma_table_info`, idempotent; no destructive
  step. Its own populated-upgrade test
  (`core/storage/sqlite/scheduled_chat_history_model_cost_upgrade_test.go`)
  starts from the v0.93.1 snapshot.

Visible in the dump: the `scheduled_chat_run_history` CREATE gains the
two columns (line 490), the `harness_migrations` ledger gains the 345
row, and the seeded `seed-schedrun-hist-1` row carries `model=''`,
`cost_usd=0`. Every other line-level difference against v0.93.3 is the
dump writer's blank-line placement around unchanged INSERTs, not data.

`git diff --name-only v0.93.3..v0.93.4 -- '*migration*' core/storage
core/session` lists the migration, `migrations.go`, the three pin tests
(`migrations_test.go`, `sqlite_test.go`, `repair_upgrade_test.go` —
ledger 67, 48, list +345), the `upgrade_path_test.go` /
`audit_retention_populated_test.go` digest waivers scoped to snapshots
whose `scheduled_chat_run_history` lacks `model`, and the v0.93.3
snapshot files themselves (landed by #400 after the v0.93.3 tag).

Snapshots from v0.94.0 on must carry these columns, so the
`scheduled_chat_run_history` digest waiver no longer applies to them.
