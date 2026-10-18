# v0.84.0 upgrade snapshot — provenance

Hand-written per the release ritual (CLAUDE.md, blind spot #3 →
release-ritual corollary). `scripts/ci/upgrade-snapshot.sh` writes only
`dump.sql`; this file is the part a human has to author.

## How this file was produced

    bash scripts/ci/upgrade-snapshot.sh v0.84.0

Replay mode, `v0.83.0 -> v0.84.0`. The script materialised
`testdata/upgrade/v0.83.0/dump.sql` into a fresh `data.db`, ran `Open()`
under tag `v0.84.0`'s code in a throwaway `git worktree` at `63c8f360`,
and dumped the result back out. Generated 2026-09-30 from
`chore/v0.84.0-snapshot`, branched off the squash commit itself.

- Tag: `v0.84.0`
- Squash commit: `63c8f360` ("feat: v0.84.0 — advisors: local ML
  recommendations, the label corpus, and the sidecar seam", PR #358)
- Predecessor snapshot: `v0.83.0`
- Contents: 799 → 824 lines

## What the replay actually applied

**The first real migration in seven releases** — the streak of six
byte-identical snapshots (v0.79.0 → v0.83.0 transitions) ends here.
One migration became newly pending:

| Migration | Effect in this dump |
|---|---|
| `laya-advisors/1600-advice-labels` | new table `advice_labels` (the local training-label corpus: kind, prompt_version, features JSON, features_complete discriminator, propensity fields, user_action, local session_id) |

The `harness_migrations` ledger goes 58 → 59 rows. Block 1600-1699 is
laya-advisors-01LAYA001's, freshly allocated (verified against finding
#55's collision pairs at review).

## The property this snapshot actually proves

**Migration 1600 ran against a database with LIVE `units` rows and
touched nothing it shouldn't.** This is exactly the forward hazard the
v0.83.0 PROVENANCE recorded when `units.KindDoc` went live: "the next
migration touching `units` will replay against REAL document rows for
the first time — this file is where that evidence will come from."
Delivered: `TestUpgradePath`'s `assertUnitsTableSurvivesUntouched`
asserts the KindDoc seed rows survive by id+title (not count), and the
dump diff shows `advice_labels` as the only new table — every other
table's rows are carried over intact from v0.83.0.

As always, scope: this proves migration selection and this migration's
behavior against a previously-shipped schema. It proves nothing about
the advisor runtime, chips, auto-act, or capture (the generator calls
only `storagesqlite.Open`).

## Caveats

- The next release replays FROM this file — advice_labels now exists in
  the chain, so any future migration touching it gets tested against
  this schema, not an empty one.
- Immutable from here (`check-upgrade-snapshots-locked.sh`).
- Generating this appended to `~/.kenaz/harness.log` (finding #56).
