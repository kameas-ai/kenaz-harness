# Provenance: `testdata/upgrade/v0.65.0/dump.sql`

**Tag**: `v0.65.0` (commit `97399c01`, *"feat: v0.65.0 — unwired and
unreachable: scheduled jobs, trust surfaces, audit persistence"*, PR #299)
— the latest release tag at the time this snapshot was generated
(2026-08-20).

## How this snapshot was produced

Replay, the normal path (`snapshot(N) = replay(snapshot(N-1), tag N)`):

```bash
bash scripts/ci/upgrade-snapshot.sh v0.65.0
```

which reported `replay mode: v0.64.1 -> v0.65.0`, created a detached
`git worktree` at `refs/tags/v0.65.0` (`HEAD is now at 97399c01`),
materialised `v0.64.1/dump.sql` into a fresh `data.db`, ran that tag's
`storagesqlite.Open` against it, and dumped the result through
`core/storage/sqlite/upgradesnap`.

## The streak ended here

`v0.63.2`, `v0.64.0` and `v0.64.1` each shipped without a snapshot. Every
one of their PROVENANCE.md files correctly diagnosed the missing gate and
then left it missing, because the convention depended on a human
remembering it at release time.

**This one was not remembered. It was caught.**
`scripts/ci/check-upgrade-snapshot-present.sh` shipped in this very
release, and minutes after `tag-on-merge` cut `v0.65.0` it failed:

```
[upgrade-snapshot-present] FAIL: the upgrade-snapshot chain is behind the newest release tag.

  newest release tag: v0.65.0
  newest snapshot:    v0.64.1
```

v0.65.0 would have been the fourth consecutive release to ship without a
snapshot. That is the difference between a documented convention and an
enforced one.

## Schema review for v0.65.0

**This is the first snapshot since `v0.63.1` that is NOT byte-identical to
its predecessor.** The three before it recorded no schema change; this one
records eight migrations.

Ledger rows: **43 → 51**, matching `TestOpen_ApplyIdempotent`'s registered
count of 51 exactly. New migration IDs:

```
sessions/0336-stream-checkpoints          (chat-turn-integrity-01PMZ606 WP02)
bundle/700-trust-anchors-init             (bundle-download-and-verify-01PMZ909 UNIT-3)
event-log/0100-events                     (audit-that-tells-the-truth-01PMZA10 UNIT-2)
event-log/0101-event-chain-heads
event-log/0102-redaction-rules
event-log/0103-retention-config
event-log/0104-schema-version
event-log/0105-saved-audit-queries
```

New tables: `stream_checkpoints`, `trust_anchors`, `events` (+ its four
`events_fts_*` shadow tables), `event_chain_heads`, `redaction_rules`,
`retention_config`, `saved_audit_queries`.

**Why this snapshot matters more than the last three.** The `event-log`
block is `100-199`, far BELOW the `sessions/03xx` high-water mark any
upgraded install already carries. That is the exact shape of the v0.63.0
P0, where a migration numbered below the global max was silently skipped
and every upgraded install broke while the whole suite stayed green. This
file is the state a v0.65.0 install actually has, so the next release's
`TestUpgradePath` exercises that path against real rows rather than a
fresh database.

## Note on the six `event-log` migrations

These are the migrations a sub-agent accidentally applied to a live
production profile on 2026-08-20 by running `wails generate module`
without overriding `HOME`/`KENAZ_HARNESS_ENV` (see `CLAUDE.md`'s tooling
footguns). That profile was remediated — the six tables dropped and the
six ledger rows deleted — after confirming against `v0.64.1/dump.sql`
that none of the tables exist in any shipped schema. As of this tag they
legitimately DO ship, which is why they appear here.

## Verification run

See the commit for the `TestUpgradePath/v0.65.0` output. The subtest must
appear as its own `RUN` line and open a real database — a green suite is
not evidence that a new snapshot is covered, which is the whole reason
this chain exists.
