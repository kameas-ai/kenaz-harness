# v0.80.0 upgrade snapshot — provenance

Hand-written per the release ritual (CLAUDE.md, blind spot #3, release-ritual
corollary). `scripts/ci/upgrade-snapshot.sh` writes only `dump.sql`.

## How this file was produced

    bash scripts/ci/upgrade-snapshot.sh v0.80.0

Replay mode, `v0.79.0 -> v0.80.0`: the previous snapshot materialised into a
fresh `data.db`, `Open()` run under tag `v0.80.0`'s code in a throwaway
worktree, result dumped back out. Generated 2026-09-15 from
`chore/v0.80.0-snapshot`, branched off the squash commit.

- Tag: `v0.80.0`
- Squash commit: `ed55cfd1` ("feat: v0.80.0 — onboarding stops lying, two
  critical-path P0s, the fleet storm, and the rater", PR #346)
- Predecessor snapshot: `v0.79.0`

## What the replay applied: nothing — and that is the finding

`diff v0.79.0/dump.sql v0.80.0/dump.sql` is **empty**. Zero new migration
ledger rows, zero schema changes, zero row changes. Verified by diffing the
two dumps' migration-ID sets (empty) and the full files (0 lines).

That is correct, not a failure of the ritual: v0.80.0 is a pure code-fix
release — onboarding provider-key wiring, the partial-persist tail fix, the
MCP probe mutex, fleet singleflights, context-publish logging, the rater
(shipped disabled), and CI-gate hardening. None of its six streams touched
`core/storage/migrations`, and the byte-identical dump is the *proof* of
that claim, not merely its restatement. If a future reader finds this dump
differing from v0.79.0's, something rewrote history.

The snapshot still earns its place in the chain: `TestUpgradePath/v0.80.0`
boots this exact state under every future HEAD, so the chain's contiguity
(`check-upgrade-snapshot-present.sh` compares `max(snapshot)` against
`max(tag)`) is preserved without a gap that the next schema-bearing release
would otherwise have to reason around.

## Caveats

- Immutable from here (`check-upgrade-snapshots-locked.sh`).
- Generating this file appended to `~/.kenaz/harness.log` (finding #56,
  known and unowned).
