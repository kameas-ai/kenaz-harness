# v0.94.0 upgrade snapshot — provenance

Hand-written per the release ritual. `scripts/ci/upgrade-snapshot.sh`
writes only `dump.sql`; this file is the human-authored part.

## How this file was produced

    HOME=$(mktemp -d) KENAZ_HARNESS_ENV=test bash scripts/ci/upgrade-snapshot.sh v0.94.0

Replay mode, `v0.93.4 -> v0.94.0`. Generated 2026-10-09 from
`chore/v0.94.0-upgrade-snapshot`.

- Tag: `v0.94.0`
- Squash commit: `985e312a` ("feat: v0.94.0 — tool context on demand:
  exposure tiers, kenaz__load_tools, schema budget, cacheable prefix,
  org pins", PR #403, mission `tool-context-budget-01TCBUD01`)
- Predecessor snapshot: `v0.93.4`

## What changed on the upgrade path

**Not zero-delta.** `dump.sql` hashes `2822a4fb9f40be291508b03aa8e06fc3`
(v0.93.4: `317340793a882e1afa75076b7c7832f6`). Two migrations landed in
this tag range, both additive, `pragma_table_info`-guarded and
idempotent, each with its own populated-upgrade test:

- `sessions/0346-session-usage-cache-tokens`
  (`core/session/migrations_session_usage_cache.go`, version 346,
  ledger row 72): adds `cached_tokens` and `cache_write_tokens` to
  `session_messages` (nullable; rows written since store 0 when the
  provider reports no cache split). Test:
  `core/storage/sqlite/session_usage_cache_upgrade_test.go` from the
  v0.93.2 snapshot.
- `sessions/0347-tool-exposure`
  (`core/session/migrations_tool_exposure.go`, version 347, ledger
  row 73): adds `projects.tool_exposure`, `sessions.tool_exposure` and
  `sessions.tool_activations` (nullable TEXT; NULL / "" = no opinion).
  Test: `core/storage/sqlite/tool_exposure_upgrade_test.go` from the
  v0.93.2 snapshot.

Visible in the dump: the three CREATE statements gain the columns, the
`harness_migrations` ledger gains rows 72 and 73, and the seeded
`projects` / `sessions` / `session_messages` INSERTs carry the new
columns. Every other line-level difference against v0.93.4 is the dump
writer's blank-line placement around unchanged rows, not data.

Migration pins after this release: sqlite ledger 69, session ledger 50,
version lists end `…345, 346, 347`. The `TestUpgradePath` digest
waivers for `session_messages` (0346) and `projects` (0347) are scoped
to snapshots whose tables lack the new columns; from this snapshot on
they do not apply, so a later UPDATE/backfill of those tables' existing
rows would be caught.
