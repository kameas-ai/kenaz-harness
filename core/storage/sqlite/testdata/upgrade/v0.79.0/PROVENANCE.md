# v0.79.0 upgrade snapshot — provenance

Hand-written per the release ritual (CLAUDE.md, "Release ritual: unwired
sweep" → blind spot #3 → release-ritual corollary).
`scripts/ci/upgrade-snapshot.sh` writes only `dump.sql`; this file is the
part a human has to author, and the gate's own message notes that every
provenance file to date was hand-written — "one reason this ritual keeps
being half-executed."

## How this file was produced

    bash scripts/ci/upgrade-snapshot.sh v0.79.0

Replay mode, `v0.78.1 -> v0.79.0`. The script materialised
`testdata/upgrade/v0.78.1/dump.sql` into a fresh `data.db`, ran `Open()`
under tag `v0.79.0`'s code in a throwaway `git worktree` at `1ba6a209`,
and dumped the result back out. Generated 2026-09-13 from
`chore/v0.79.0-snapshot`, branched off the squash commit itself.

- Tag: `v0.79.0`
- Squash commit: `1ba6a209` ("feat: v0.79.0 — fourteen finishing missions
  and eight release findings, including a kill switch that gated nothing",
  PR #344)
- Predecessor snapshot: `v0.78.1`
- Contents: 777 → 799 lines

## What the replay actually applied

Two migrations became newly pending between `v0.78.1` and `v0.79.0`, both
from `model-scheduled-jobs-01PMSJ01`. Verified by diffing the two dumps'
migration-ledger IDs, not by reading the mission's commits:

| Migration | Effect in this dump |
|---|---|
| `sessions/0338-blocked-permission-requests` | new table `blocked_permission_requests` |
| `sessions/0339-scheduled-chat-runs-trigger-kind` | `scheduled_chat_runs` gains `trigger_kind TEXT NOT NULL DEFAULT 'cron'` and `run_at INTEGER` |

The `harness_migrations` ledger goes 56 → 58 rows. These two IDs close the
`0337 → 0340` gap recorded in finding #88: they had been *allocated* in the
block registry but never written, so the ledger skipped them.

## The property this snapshot actually proves

**Migration `0339` ran against a populated table and the existing row
survived.** The diff shows the single pre-existing `scheduled_chat_runs`
row re-emerging with the two new columns filled from their defaults:

    - INSERT INTO "scheduled_chat_runs" (…, "created_by", "tool_allowlist")
    + INSERT INTO "scheduled_chat_runs" (…, "created_by", "tool_allowlist", "trigger_kind", "run_at")

That is the whole point of the chain. CLAUDE.md's corollary is blunt about
it: *"a migration that has never run against populated tables has never
been tested."* On a fresh database `0339` would ALTER an empty table and
prove nothing. Here it altered a table with a real row in it, carried over
from a previously-shipped schema.

No other table changed. No rows were lost: the only new `INSERT` lines are
the two ledger entries, and every other table's row count is identical to
`v0.78.1`. That negative result is deliberate evidence — `0338` is additive
and `0339` is a column add, so *any* other delta would have been a finding.

## Caveats for whoever reads this next

- **`0338` carries no foreign key to `scheduled_chat_runs`,** by design
  (`01PMSJ01` AC-007). The mission asserted it with a
  `pragma_foreign_key_list` check and mutation-proved it: re-adding
  `ON DELETE CASCADE` turns two tests red. If a future migration adds that
  FK, this snapshot is where the cascade would first become visible against
  real rows — see finding #88 and the `sessions/0332` / `sessions/0327`
  cascade history in `docs/unwired-ledger.md` for why that matters.
- **This snapshot is immutable from here.**
  `check-upgrade-snapshots-locked.sh` fails on any edit to a committed
  snapshot directory, and `upgrade-snapshot.sh` re-run for this tag must
  reproduce `dump.sql` byte-identically.
- The generator opens a real `storagesqlite.DB`, so producing this file
  appended to `~/.kenaz/harness.log` (finding #56, known and unowned — an
  unsandboxed log sink, not a data hazard).
