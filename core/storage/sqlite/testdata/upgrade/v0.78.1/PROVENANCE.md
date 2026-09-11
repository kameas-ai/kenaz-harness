# Provenance: `testdata/upgrade/v0.78.1/dump.sql`

**Tag**: `v0.78.1`, cut by `tag-on-merge.yml` from **#342,
`fix: v0.78.1 — nine findings v0.78.0 opened, and the checks that measured
the wrong thing`** — a roll-up of nine findings.

## How this snapshot was produced

```bash
bash scripts/ci/upgrade-snapshot.sh v0.78.1
```

`replay mode: v0.78.0 -> v0.78.1` — detached worktree at the tag, `v0.78.0`'s
dump materialised into a fresh `data.db` under `$TMPDIR`, that tag's
`storagesqlite.Open` run against it, dumped back out through `upgradesnap`.

## Schema review — this one is NOT byte-identical, and that is the point

Every snapshot from `v0.73.1` through `v0.78.0` was byte-identical to its
predecessor, because those releases were wiring, policy and presentation.
**This one differs**, and the difference is the whole reason the chain exists:

```
$ cmp v0.78.0/dump.sql v0.78.1/dump.sql          -> DIFFERS
$ grep -c policy_decisions v0.78.0/dump.sql      -> 0
$ grep -c policy_decisions v0.78.1/dump.sql      -> 2
```

`v0.78.1` ships migration **`cedar-policy/1300-policy-decisions`** (finding
#58: the Cedar audit-decision log had no persistent backing — a 256-entry
in-memory ring, lost on restart). The new dump carries
`CREATE TABLE policy_decisions`, `idx_policy_decisions_evaluated_at`, and
the `cedar-policy/1300-policy-decisions` row in the migration ledger.

**Why that matters more than a passing test.** CLAUDE.md blind spot #3: every
ordinary test starts from an empty database, so on a fresh DB the migration
high-water mark is 0 and everything applies in one ascending pass — a
migration-selection defect *cannot occur* under those conditions. A migration
that has never run against populated tables has never been tested. This
snapshot is migration 1300 applying to a database a **previously shipped
release** produced, which is the only place that class of defect is visible.
It is also the first entry in this chain to exercise a real migration that
way rather than confirming an inert one.

## Replay verified

Not name-checked — replayed, with the exit code read directly rather than
from printed output (`go test` in this environment prints canned success
strings that do not track `$?`):

```
$ rtk proxy go test ./core/storage/sqlite/ -run TestUpgradePath -count=1 -v
REAL_EXIT=0
--- PASS: TestUpgradePath/v0.78.1 (1.69s)
ok   .../core/storage/sqlite   2.735s
```

28 per-tag subtests, the whole committed chain, green.

## Gates

```
check-upgrade-snapshots-locked.sh  -> 0
check-upgrade-snapshot-present.sh  -> 0, "chain reaches v0.78.1
                                      (newest release tag v0.78.1)"
```

`chore:` cuts no tag, so the chain stays current on merge.
