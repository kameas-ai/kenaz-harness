# Provenance: `testdata/upgrade/v0.66.0/dump.sql`

**Tag**: `v0.66.0` — "the sandbox stops out-ranking the host, and the audit
log tells the truth" (PR #301).

## How this snapshot was produced

```bash
bash scripts/ci/upgrade-snapshot.sh v0.66.0
```

Replay from `v0.65.1`, using HEAD's generator per the standing rule: the
tag supplies the database STATE (its migrations run against the previous
snapshot, checked out at the `v0.66.0` git tag), HEAD supplies the dump
FORMAT.

## Why it was missing

Found by `controls-and-readouts-that-tell-the-truth-01PMZ808` UNIT-8
(WP13, mandatory `WP-persistence-integrity`), which needed a real
previous-release database to test `SaveScrollPosition`'s SQL round-trip
against (AC-036 / AC-PI-1) and, while picking a snapshot to boot from,
ran `check-upgrade-snapshot-present.sh` first as due diligence:

```
[upgrade-snapshot-present] FAIL: the upgrade-snapshot chain is behind the newest release tag.
  newest release tag: v0.66.0
  newest snapshot:    v0.65.1
```

Not this mission's regression — `v0.66.0` was tagged by `tag-on-merge.yml`
when PR #301 squash-merged, and nobody ran the release ritual afterward.
Same class of gap as `v0.65.1`'s own `PROVENANCE.md` describes for the
release before it: routine merges keep minting tags, and each one owes a
snapshot whether or not anyone remembers.

## Schema review for v0.66.0

Diffed against `v0.65.1/dump.sql` (byte-identical to `v0.65.0`'s). Three
changes, all attributable to `audit-that-tells-the-truth-01PMZA10` UNIT-8
(`event-log/0106-events-fts-sync`), which shipped IN v0.66.0 — this is the
first snapshot where that migration's effect is baked into the frozen
tag state, rather than something `TestUpgradePath` applies on top of an
older frozen snapshot (see `expectedChangedTables["v0.65.0"]` /
`["v0.65.1"]` in `upgrade_path_test.go`, which cover exactly that
"applied on top of an older snapshot" case and are unaffected by this
addition):

1. `harness_migrations` gains ledger row 56 —
   `event-log/0106-events-fts-sync`, applied.
2. `retention_config`'s single row is corrected from
   `{"kind":"keep_all"}` (an invalid `RetentionStrategy` value, per
   `core/event/log`) to `{"kind":"keep_forever"}`.
3. `events_fts_ad` (AFTER DELETE) and `events_fts_au` (AFTER UPDATE)
   triggers are added alongside the pre-existing `events_fts_ai` (AFTER
   INSERT) trigger — closing the gap CLAUDE.md's unwired-sweep blind
   spot #3 addendum names: an external-content FTS5 index with an
   insert trigger and no delete trigger doesn't merely go stale on a
   retention sweep, it breaks permanently for every purged term.

No migration in this mission's own WP13 (`SaveScrollPosition`'s
client/binding/serve wiring) touches storage shape — `scroll_position`
is a pre-existing column with a pre-existing `UPDATE` (see
`docs/unwired-ledger.md` and this mission's WP-PI report). This snapshot
exists to give that WP a **previous-release database to test the SQL
round-trip against**, not because WP13 itself changed the schema.

## Verification run

```
$ go test ./core/storage/sqlite/... -run TestUpgradePath -v -count=1
=== RUN   TestUpgradePath
=== RUN   TestUpgradePath/v0.63.0
=== RUN   TestUpgradePath/v0.63.1
=== RUN   TestUpgradePath/v0.63.2
=== RUN   TestUpgradePath/v0.64.0
=== RUN   TestUpgradePath/v0.64.1
=== RUN   TestUpgradePath/v0.65.0
=== RUN   TestUpgradePath/v0.65.1
=== RUN   TestUpgradePath/v0.66.0
--- PASS: TestUpgradePath (0.00s)
    --- PASS: TestUpgradePath/v0.64.0 (0.65s)
    --- PASS: TestUpgradePath/v0.64.1 (0.65s)
    --- PASS: TestUpgradePath/v0.63.2 (0.67s)
    --- PASS: TestUpgradePath/v0.66.0 (0.67s)
    --- PASS: TestUpgradePath/v0.65.0 (0.67s)
    --- PASS: TestUpgradePath/v0.65.1 (0.67s)
    --- PASS: TestUpgradePath/v0.63.1 (0.67s)
    --- PASS: TestUpgradePath/v0.63.0 (0.69s)
PASS
ok  	github.com/kameas-ai/kenaz-harness/core/storage/sqlite	1.093s
```

`v0.66.0` passed with **no new `expectedChangedTables` entry** — expected,
because migration 0106 is already baked into this tag's own frozen state
(item 1-3 above), not something HEAD applies on top of it.

## A footgun this run surfaced, for the next person

`scripts/ci/upgrade-snapshot/generator_main.go` uses the package default
logger, which (with no `KENAZ_HARNESS_ENV` override in this shell)
resolves to the real `~/.kenaz/harness.log` — it appends informational
`storage.opened` lines there even though the actual SQLite work happens
entirely inside a `mktemp` working directory / `git worktree`. No
`~/.kenaz/**` *data* file (settings, profile databases) was touched —
confirmed by grepping the log for `storage.opened` entries from this run
and finding only `/var/folders/…` / `/private/var/folders/…` temp paths —
but the log write itself is still a write under `~/.kenaz/`, the exact
class of thing the harness's own operating doctrine says to avoid. Not
new to this run: every plain `go test` invocation in a shell with no
`KENAZ_HARNESS_ENV` override does the same append, for the same reason.
Tracked here rather than silently worked around.
