# Provenance: `testdata/upgrade/v0.63.1/dump.sql`

**Tag**: `v0.63.1` (commit `77e0605b`) — the fix for the `v0.63.0` P0: the
`Pending()` set-membership repair (`core/storage/migrations/registry.go`) and
`verifyFullyApplied` (`core/storage/sqlite/sqlite.go`).

## How this snapshot was produced

Standard replay (spec §5.3), not a bootstrap: `snapshot(v0.63.1) =
replay(snapshot(v0.63.0), v0.63.1)`.

`scripts/ci/upgrade-snapshot.sh v0.63.1` was run for real against this repo:

1. `git worktree add --detach <tmp> v0.63.1` — a genuine checkout of the
   historical fix commit, not a simulation.
2. `mkdir -p frontend/dist && touch frontend/dist/.gitkeep` (the embed target
   note in spec §7 — a fresh worktree needs this before any `go build`).
3. `scripts/ci/upgrade-snapshot/generator_main.go` injected into the worktree
   (that tag's tree has no such file) and built with `go build`. It compiled
   cleanly against `v0.63.1`'s tree with no changes — confirming the file's
   own self-containment claim (only `storage.Config`, `storagesqlite.Open`,
   `database/sql`, stdlib).
4. `<generator> -mode=replay -prev testdata/upgrade/v0.63.0/dump.sql -out ...`
   — materialised the `v0.63.0` genesis into a fresh `data.db`, then ran
   `storagesqlite.Open` **under `v0.63.1`'s actual fix code**. This is the
   real repair running against the real bug shape, not a description of it.
5. Dumped and normalised; copied back into the main tree.
6. Worktree removed.

## What actually happened when replayed

The ledger in this snapshot ends with `sessions/332`, `333`, `334`, `335` all
`applied` — the fixed `Pending()` selected them where `v0.63.0`'s could not.
Confirmed directly in the generated dump:

- `sessions.move_history_mode` column present (migration 334 landed).
- `artifact_versions` still holds **both** seed rows for `seed-artifact-1`
  (versions 1 and 2) after migration 0332's create/copy/drop/rename rebuild —
  the cascade-canary check (spec §1.3/§5.4) passed for real, on this exact
  historical fix commit, not merely in a same-tree unit test.

## Determinism, verified

Re-running `bash scripts/ci/upgrade-snapshot.sh v0.63.1` against the
committed `v0.63.0` genesis reproduces this file **byte-identically** — this
was performed (not merely claimed) as part of WP01's acceptance run; see the
mission report for the diff output (empty) and the companion mutation
(perturbing the schema-object sort order in `generator_main.go` and
confirming a 1000+ line divergence, then reverting and confirming
byte-identity returns).

## Relationship to the mission's falsifiability criterion

This snapshot is a **secondary** witness, not the headline one — the
headline is the `v0.63.0` genesis case in `upgrade_path_test.go`, which is
what the reverted-`Pending()` mutation (spec §6.1) targets. This `v0.63.1`
snapshot instead demonstrates the chain's steady-state replay mechanism
working end-to-end against a real historical tag, and gives
`upgrade_path_test.go` a second, already-healed case to assert idempotence
and full-surface reads against without relying solely on the genesis
rewind path.
