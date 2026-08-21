# Provenance: `testdata/upgrade/v0.66.0/dump.sql`

**Tag**: `v0.66.0` (commit `4d34cf4a`, *"feat: v0.66.0 — the sandbox stops
out-ranking the host, and the audit log tells the truth"*, PR #301) — the
latest release tag at the time this snapshot was generated (2026-08-20),
produced while closing out `audit-that-tells-the-truth-01PMZA10`'s final
two units (UNIT-9 / UNIT-10).

## Why this was missing

`check-upgrade-snapshot-present.sh` reported the chain behind the newest
release tag when this work started:

```
[upgrade-snapshot-present] FAIL: the upgrade-snapshot chain is behind the newest release tag.
  newest release tag: v0.66.0
  newest snapshot:    v0.65.1
```

v0.66.0 shipped `audit-that-tells-the-truth-01PMZA10` WP02–WP10 (UNIT-1
through UNIT-8: the six event-log migrations at versions 100–105, the
SQLite backend, the durable audit producer/reader wiring, the eight
emitter sites, Export + saved queries, `VerifyChain`, and the retention
sweep + FTS-sync migration at version 106) — seven migrations, more than
any other in-flight mission at the time, per plan.md's "Release-ritual
obligation (UNIT-10)" note. WP12/WP-PI (this mission's last unit) is the
one now closing the mission out, so per that note's rule ("if it lands
last, it holds the obligation") this mission's own WP-PI cuts the
snapshot for the tag it shipped into.

## How this snapshot was produced

```bash
bash scripts/ci/upgrade-snapshot.sh v0.66.0
```

Replay from `v0.65.1`, using HEAD's generator per the rule PR #300
established: the tag supplies the database STATE (a `git worktree add
--detach` checkout of `v0.66.0` under a `mktemp -d` scratch directory —
never `~/.kenaz` or any real profile), HEAD supplies the dump FORMAT.
Verified: the generator opened its scratch `data.db` under
`/var/folders/.../T/upgrade-snapshot-*/data.db`, not a real profile
directory.

## Schema review for v0.66.0

**One new ledger row versus `v0.65.1`: `event-log/0106-events-fts-sync`
(version 106, owning_mission `event-log`).** Ledger count moves from 51
to **52**, matching `core/storage/sqlite/sqlite_test.go`'s
`TestOpen_ApplyIdempotent` fresh-database assertion (`count != 52`) —
this snapshot now agrees with what a brand-new install already produces,
closing the gap `check-upgrade-snapshot-present.sh` exists to catch.

Migration 106 is UNIT-8's FTS-sync fix: an `UPDATE retention_config`
correction of migration 103's bad seed value, plus the
`events_fts_au`/`events_fts_ad` triggers that migration 0001 shipped
without (`0001_events.sql:33`'s "No update / delete triggers" comment was
false the day it shipped — see `scripts/ci/check-fts-sync.sh`, added in
this same unit, for the standing guard against a third instance of this
class). Non-destructive: `UPDATE ... WHERE` scoped to a known-bad
literal (a no-op everywhere else) plus two `CREATE TRIGGER IF NOT
EXISTS` statements. `check-destructive-migration-coverage.sh` sees no
new violation from it.

No other schema changes landed between `v0.65.1` and `v0.66.0` outside
this mission's own migrations 100–106 (all six of which were already
reflected in `v0.65.0`/`v0.65.1`'s snapshots — 106 is the only delta).

## Verification run

`TestUpgradePath/v0.66.0` — `RUN`/`PASS`, 4.43s, real sqlite via
`storagesqlite.Open` against the replayed database, not a fresh/empty
one. Full output pasted in this mission's WP-PI report.
