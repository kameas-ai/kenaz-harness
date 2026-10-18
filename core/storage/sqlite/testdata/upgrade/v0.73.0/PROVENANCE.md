# Provenance: `testdata/upgrade/v0.73.0/dump.sql`

**Tag**: `v0.73.0` (squash commit `f3809950`) — the newest release tag at the
time this snapshot was generated (2026-09-09).

## How this snapshot was produced

Replay, the normal path (`snapshot(N) = replay(snapshot(N-1), tag N)`):

```bash
bash scripts/ci/upgrade-snapshot.sh v0.73.0
```

which reported `replay mode: v0.72.0 -> v0.73.0`, created a detached
`git worktree` at `refs/tags/v0.73.0` (`HEAD is now at f3809950`),
materialised `v0.72.0/dump.sql` into a fresh `data.db`, ran that tag's
`storagesqlite.Open` against it, and dumped the result.

## Schema review for v0.73.0

763 lines. **The only delta from `v0.72.0/dump.sql` is one ledger row**:

```
sessions/0337-repair-checkpoint-rows
```

That is correct and worth stating precisely, because this release DID ship a
destructive migration. `sessions/0337` is the transcript repair (WP05, owner
ruling on escalation E-002) — an irreversible `DELETE FROM session_messages`
for checkpoint junk written before v0.65.0. It ran here and changed **no row**,
because no committed snapshot in this chain carries that junk: every snapshot
was produced by replay, never by a real polluted install.

**So this snapshot does NOT exercise 0337's delete path.** That coverage lives
in `core/storage/sqlite/migration_0337_test.go`, which boots the `v0.64.0`
snapshot (a tag predating the WP03 fix) and seeds all four shapes by hand —
junk, genuine error-path partial, resumed partial, and the cross-turn
coincidence that review round 3 found. Do not read a passing
`TestUpgradePath/v0.73.0` as evidence that the repair migration is safe.

## ⚠️ This snapshot freezes a defect: 0337's content hash is empty

The ledger row above carries
`content_hash = e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855`
— that is **SHA-256 of the empty string**. Every other migration in the chain
carries a real hash.

0337 therefore registered without hashing its SQL source, so it has **no drift
protection**: the runner's `ErrLedgerHashMismatch` check cannot fire for it, and
its `Up` could change in a future release with no ledger complaint. Discovered
2026-09-09 while checking whether a mid-release edit to
`sqlCheckpointRepairUpSource` would strand an already-migrated developer
profile (it would not — precisely because the hash is empty).

Committing this snapshot makes the empty hash **immutable**, which is the
"snapshot dumper froze its own bugs into every future release" pattern from
PR #300. Recording it here so the next reader does not mistake it for
normal. **Owed (owner: alec): give 0337 a real content hash, and consider a
gate that rejects an empty one at registration.**

## `expectedChangedTables["v0.73.0"]` is `{tasks}` alone

Same as v0.72.0, and for the same reason: `assertTasksTableMigrated` performs
its own probe insert. That is a test artifact, not a schema change.
`scheduled_chat_runs` is deliberately absent — `sessions/0340`'s DEFAULT
backfill has nothing left to do, and copying the previous tag's entry
wholesale would silently excuse a real future change to it.

This is the **third consecutive tag** needing a hand-written entry for a probe
that writes to a watched table. The durable fix — stop the probe writing there
— remains owed.

## Verification run

```
$ go test ./core/storage/sqlite/ -run TestUpgradePath -count=1 -v
=== RUN   TestUpgradePath/v0.73.0
--- PASS: TestUpgradePath/v0.73.0 (1.42s)
```

Checked that the subtest genuinely RAN rather than the suite merely staying
green: it appears as its own `RUN`/`PASS` pair. It was **not** green on first
generation — it failed with `table tasks content digest changed (not in
expectedChangedTables["v0.73.0"])`, i.e. the snapshot was correct and the pin
was missing. That is the gate working; expect the same on v0.74.0.
