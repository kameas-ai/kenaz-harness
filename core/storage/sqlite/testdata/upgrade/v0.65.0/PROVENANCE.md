# Provenance: `testdata/upgrade/v0.65.0/dump.sql`

**Tag**: `v0.65.0` (commit `97399c01`, *"feat: v0.65.0 — unwired and
unreachable: scheduled jobs, trust surfaces, audit persistence (#299)"*) —
the latest release tag at the time this snapshot was generated
(2026-08-20).

## Why this snapshot was missing

`v0.65.0` registered `audit-that-tells-the-truth-01PMZA10` UNIT-2's six
`event-log/010x` migrations (versions 100-105) into the production
registry — the first migrations this mission landed. Per the release
ritual (`CLAUDE.md`), cutting a release that adds migrations includes
running `bash scripts/ci/upgrade-snapshot.sh <tag>` and committing the
result. That step was not done when `v0.65.0` was tagged: `git tag
--sort=-v:refname` showed `v0.65.0` as the newest tag while
`testdata/upgrade/` topped out at `v0.64.1`, and
`bash scripts/ci/check-upgrade-snapshot-present.sh` failed on this
exact tree with:

```
[upgrade-snapshot-present] FAIL: the upgrade-snapshot chain is behind the newest release tag.
  newest release tag: v0.65.0
  newest snapshot:    v0.64.1
```

This directory closes that gap, discovered and produced by
`audit-that-tells-the-truth-01PMZA10` UNIT-5..8 work (this mission's own
migration 106 needs a snapshot to test *from*, and `v0.64.1` predates
`events`/`events_fts` entirely — the schema its own AC-PI-1/AC-PI-3 work
must boot from is this one).

## A generator defect found and fixed while producing it

The first attempt (`bash scripts/ci/upgrade-snapshot.sh v0.65.0`,
unmodified) produced a `dump.sql` that `file(1)` reported as `data`, not
text — a literal NUL byte at offset 27105, inside
`INSERT INTO "events_fts_data" ... VALUES (10, '\x00\x00\x00\x00\x00\x00\x00')`.

Root cause, in `core/storage/sqlite/upgradesnap/upgradesnap.go` (and its
self-contained duplicate `scripts/ci/upgrade-snapshot/generator_main.go`,
which the shell script actually builds and runs when the target tag's
own checkout already contains a generator copy — as `v0.65.0`'s does):

1. `isFTSShadow` consulted a **hardcoded map** — `{"messages_fts": true}`
   — to decide which tables are FTS5-internal shadow storage (`_data`,
   `_idx`, `_docsize`, `_config`) and therefore excluded from the dump.
   `events_fts` (event-log's own external-content FTS5 table, migration
   `event-log/0100-events`) is the **second** FTS5 virtual table this
   repo has ever had, and the hardcoded map had no entry for it — so its
   shadow tables were dumped as ordinary data tables.
2. `sqlLiteral`'s `[]byte` case wrapped raw bytes in a quoted SQL string
   literal (`'...'`) rather than a SQLite `X'<hex>'` BLOB literal.
   `events_fts_data`'s shadow-table columns hold real compressed FTS5
   index bytes — the first BLOB column this dump format ever carried
   binary (rather than incidentally-ASCII) content for — so the raw-byte
   wrap embedded literal NUL/control bytes into the file text.

Fix, in both copies (kept in sync per the generator's own header
comment): `isFTSShadow` now takes a DDL-derived set built by scanning
`sqlite_master` for `CREATE VIRTUAL TABLE ... USING fts5(...)`, so a new
FTS5 table can never again be invisible to it; `sqlLiteral`'s `[]byte`
case now emits `X'<hex>'`. Regression test:
`TestDumpHandlesSecondFTS5TableAndBinaryBlobs`
(`core/storage/sqlite/upgradesnap/upgradesnap_test.go`) — a second,
non-`messages_fts` FTS5 table over 200 rows of BLOB-bearing content,
asserting (a) no embedded NUL byte anywhere in the dump, (b) the shadow
tables are excluded by name, (c) at least one `X'...'` BLOB literal is
emitted, (d) full Materialize→Dump round-trip is byte-identical, (e) the
BLOB value itself survives the round trip unchanged, and (f) FTS5 search
still matches every row after materialise (proving the shadow tables
were genuinely rebuilt via trigger replay, not silently left empty).
Mutation-verified: reverting `ftsVirtualTableNames` to the old hardcoded
map and `sqlLiteral`'s blob case to the old string-wrap makes this test
fail with `dump.sql contains a literal NUL byte at offset 1098` — the
exact defect class this test exists to catch.

**This snapshot's `dump.sql` was regenerated with the fixed generator**
(built from this worktree, which is `v0.65.0`'s own tree plus only the
two generator-file fixes — no storage/migration code differs from the
tag). `file(1)` now reports it as `ASCII text, with very long lines`.

## How this snapshot was produced

Replay, the normal path (`snapshot(N) = replay(snapshot(N-1), tag N)`),
run directly against this checkout (equivalent to, but not literally,
`scripts/ci/upgrade-snapshot.sh v0.65.0`'s tag-worktree dance — done
this way specifically so the **fixed** generator was used rather than
the unfixed copy already committed at the `v0.65.0` tag):

```bash
go build -o /tmp/gen_fixed ./scripts/ci/upgrade-snapshot/
/tmp/gen_fixed -mode=replay \
  -prev core/storage/sqlite/testdata/upgrade/v0.64.1/dump.sql \
  -out  core/storage/sqlite/testdata/upgrade/v0.65.0/dump.sql
```

which materialised `v0.64.1/dump.sql` into a fresh `data.db`, ran
`storagesqlite.Open` (this tree's code — identical to `v0.65.0`'s
storage/migration code) against it, and dumped the result.

## Schema review for v0.65.0

Purely additive over `v0.64.1`: **106 new lines, 0 removed**
(`diff v0.64.1/dump.sql v0.65.0/dump.sql`). The additions are exactly
the six `event-log` migrations' schema and their ledger rows:

- `CREATE TABLE events` (+ 4 indexes) and `CREATE VIRTUAL TABLE
  events_fts USING fts5(...)` + its `AFTER INSERT` trigger
  (`event-log/0100-events`)
- `CREATE TABLE event_chain_heads` (`event-log/0101-event-chain-heads`)
- `CREATE TABLE redaction_rules` (`event-log/0102-redaction-rules`)
- `CREATE TABLE retention_config`, seeded with
  `INSERT INTO "retention_config" (...) VALUES (1, '{"kind":"keep_all"}', 0)`
  (`event-log/0103-retention-config`) — **this seed is the known bug
  spec §1.7d documents** (`keep_all` is not a valid `RetentionStrategy`
  value). It is captured here **verbatim, uncorrected**, because
  `event-log/0103-retention-config`'s `UpSource` is now a *shipped,
  already-applied* migration (this snapshot's own ledger row proves an
  install upgrading through `v0.65.0` already has this exact row) —
  `assertLedgerHashesUnchanged` freezes its SQL text from this point
  forward, same as every other migration that has ever shipped. Fixing
  the seed is UNIT-8's job and MUST be a new migration (106) correcting
  the row in place, never an edit to `0004_retention_config.sql`'s
  `UpSource`.
- `ALTER TABLE events ADD COLUMN schema_version` (`event-log/0104-schema-version`)
- `CREATE TABLE saved_audit_queries` (`event-log/0105-saved-audit-queries`)
- Six `harness_migrations` ledger rows, `owning_mission = 'event-log'`,
  versions 100-105, `action = 'applied'` — the direct evidence
  `TestUpgradePath` needs to prove these six migrations actually run
  against a database a previous release produced (§1.3 of the mission
  spec: they are the first migrations in this repo's history to land
  numerically below an install's existing high-water mark).

No pre-existing table's schema or row content changed.

## Verification run

`TestUpgradePath` boots every committed snapshot under
`testdata/upgrade/*` — see the commit for its `v0.65.0` subtest output.
The subtest must appear as its own `RUN` line and open a real database.
