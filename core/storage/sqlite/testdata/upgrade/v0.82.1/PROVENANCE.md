# v0.82.1 upgrade snapshot — provenance

Hand-written per the release ritual (CLAUDE.md, blind spot #3 →
release-ritual corollary). `scripts/ci/upgrade-snapshot.sh` writes only
`dump.sql`; this file is the part a human has to author.

## How this file was produced

    bash scripts/ci/upgrade-snapshot.sh v0.82.1

Replay mode, `v0.82.0 -> v0.82.1`. The script materialised
`testdata/upgrade/v0.82.0/dump.sql` into a fresh `data.db`, ran `Open()`
under tag `v0.82.1`'s code in a throwaway `git worktree` at `af9ed68a`,
and dumped the result back out. Generated 2026-09-16 from
`chore/v0.82.1-snapshot`, branched off the squash commit itself.

- Tag: `v0.82.1`
- Squash commit: `af9ed68a` ("fix: v0.83.0 — the consent tier writes
  what it promises, and the permission dial finally drives behavior",
  PR #354). **Version-vs-title drift, on purpose**: the PR title's
  "v0.83.0" was planning framing; the `fix:` prefix correctly produced
  a PATCH bump per tag-on-merge, and per the house rule the actual tag
  wins. The roadmap records the mapping.
- Predecessor snapshot: `v0.82.0`
- Contents: 799 → 799 lines

## What the replay actually applied

**Nothing.** Byte-identical to `v0.82.0/dump.sql` (`cmp`-verified) —
third consecutive zero-delta. No migration became pending; the ledger
stays at 58 rows. Consistent with the release content: the telemetry
opt-in pusher persists to a JSON file under `dataDir/fleet/` (not
sqlite), and the PermissionMode wiring touches only settings/knob
resolution.

## The property this snapshot actually proves

**Migration-selection stability, and only that** — tag `v0.82.1`'s
`Open()` reads a `v0.82.0` database, selects zero pending migrations,
mutates nothing. Nothing about telemetry, PermissionMode, or any other
runtime path (the generator calls only `storagesqlite.Open`; scoping
rule per the v0.81.0 snapshot review).

## Caveats

- Three consecutive zero-delta snapshots do NOT make the ritual
  skippable; the chain is per-tag and the next migration-bearing
  release replays from this file.
- Immutable from here (`check-upgrade-snapshots-locked.sh`).
- Generating this appended to `~/.kenaz/harness.log` (finding #56).
