# v0.85.0 upgrade snapshot — provenance

Hand-written per the release ritual (CLAUDE.md, blind spot #3 →
release-ritual corollary). `scripts/ci/upgrade-snapshot.sh` writes only
`dump.sql`; this file is the part a human has to author.

## How this file was produced

    bash scripts/ci/upgrade-snapshot.sh v0.85.0

Replay mode, `v0.84.0 -> v0.85.0`. The script materialised
`testdata/upgrade/v0.84.0/dump.sql` into a fresh `data.db`, ran `Open()`
under tag `v0.85.0`'s code in a throwaway `git worktree` at `369db0bb`,
and dumped the result back out. Generated 2026-09-30 from
`chore/v0.85.0-upgrade-snapshot`, branched off the squash commit itself.

- Tag: `v0.85.0`
- Squash commit: `369db0bb` ("feat: v0.85.0 — the harness speaks to the
  real ML engine", PR #360)
- Predecessor snapshot: `v0.84.0`

## What changed on the upgrade path (NON-zero delta)

Migration `advice/1601-label-revision-and-push-cursor` became newly
pending relative to the v0.84.0 snapshot and applied during the replay:

- `advice_labels` gained the `revision` column (monotonic per-row
  revision for the upsert/ack contract, design Amendment A3/A4) plus
  the `idx_advice_labels_revision` and
  `idx_advice_labels_kind_revision` indexes.
- New table `advice_label_push_cursor` (`sink`, `kind`, `cursor_ts`,
  `cursor_revision`): the label push lane's durable resume point —
  what makes a push resumable across restarts and a full re-push
  possible on mirror loss.

This is exactly the migration the WP14 label-push work shipped; a
future release that touches `advice_labels` or the cursor table will
now be tested against THIS populated shape, not an empty database.

## Why this snapshot matters

Every test that starts from `Open()` on an empty directory is
structurally blind to upgrade-path defects (the v0.63.0 P0). This
snapshot is the previous-release state the `upgrade-path` CI job will
boot under every future HEAD, and `check-upgrade-snapshot-present.sh`
fails any PR while `max(testdata/upgrade/v*)` lags `max(git tag v*)`.
