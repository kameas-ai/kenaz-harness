# v0.87.0 upgrade snapshot — provenance

Hand-written per the release ritual. `scripts/ci/upgrade-snapshot.sh`
writes only `dump.sql`; this file is the human-authored part.

## How this file was produced

    HOME=$(mktemp -d) KENAZ_HARNESS_ENV=test bash scripts/ci/upgrade-snapshot.sh v0.87.0

Replay mode, `v0.86.0 -> v0.87.0`. Generated 2026-10-05 from
`chore/v0.87.0-upgrade-snapshot`.

- Tag: `v0.87.0`
- Squash commit: `c5874cc7` ("feat: v0.87.0 — the dogfood release: chat
  single-writer, fleet session truth, and the nav IA overhaul", PR #369)
- Predecessor snapshot: `v0.86.0`

## What changed on the upgrade path

**NON-ZERO delta** — the first multi-migration release since the chain
started. Three migrations applied on the v0.86.0 -> v0.87.0 path
(ledger: 60 -> 63 rows):

1. **`sessions/0341-dedupe-user-turns`** (chat-single-writer-01DOGF0G,
   destructive): deletes doubled user rows (same session, adjacent
   sequence, byte-equal content, <=2s apart) re-pointing their
   `turn_span_id`s, and failed-streaming assistant twins. On this seed:
   0 pairs found (the seed has no duplicates) — the migration's row
   effects are covered by the dedicated 0341 snapshot-seeded tests, not
   by this dump. Hardened to tolerate re-application after units/1104
   (the repair path re-applies 0341+).
2. **`sessions/0342-session-turn-runs`** (agentgraph-settings-linkage-
   01DOGF0D, additive): creates `session_turn_runs` (turn -> graph-run
   links). Present and empty in this dump.
3. **`units/1104-artifacts-to-units`** (artifacts-as-units-01DOGF0C,
   destructive-by-rename): copies `artifacts`/`artifact_versions` into
   `units` (kind='artifact') + `unit_versions` inside one transaction
   with count+hash verification, then RENAMES the originals to
   `artifacts_legacy`/`artifact_versions_legacy` (retained read-only;
   their drop is owed to the NEXT release — mission
   units-debt-01UNITD01, migration units/1105). In this dump: the
   seed's artifact lives in `units` (3 rows, was 2) + `unit_versions`
   (2), and `artifacts_legacy`(1)/`artifact_versions_legacy`(2) retain
   the originals. There is no `artifacts` table — any tool reading this
   snapshot must classify its generation by the PRESENCE of table DDL,
   never by absence (see the units-debt H1 finding).

## Consumers owed by this snapshot

- `units/1105-drop-artifacts-legacy` (units-debt-01UNITD01, next
  release) boots from THIS snapshot: its `TestMigration1105_
  V087SnapshotBoots` skips until this directory exists and must show
  PASS (not SKIP) on the first CI run after this commit.
- The cross-block ordering proven here (sessions 0341/0342 before
  units/1104 on replay; fresh installs apply blocks independently) is
  pinned by the 0341/1104 composition tests.
