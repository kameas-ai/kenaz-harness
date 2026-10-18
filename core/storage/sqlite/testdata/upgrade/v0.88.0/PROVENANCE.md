# v0.88.0 upgrade snapshot — provenance

Hand-written per the release ritual. `scripts/ci/upgrade-snapshot.sh`
writes only `dump.sql`; this file is the human-authored part.

## How this file was produced

    HOME=$(mktemp -d) KENAZ_HARNESS_ENV=test bash scripts/ci/upgrade-snapshot.sh v0.88.0

Replay mode, `v0.87.0 -> v0.88.0`. Generated 2026-10-05 from
`chore/v0.88.0-upgrade-snapshot`.

- Tag: `v0.88.0`
- Squash commit: `b91c3c14` ("feat: v0.88.0 — one install framework
  (phases 1–2), the legacy-artifact drop, per-run graph truth, and
  folder promote", PR #370)
- Predecessor snapshot: `v0.87.0`

## What changed on the upgrade path

NON-ZERO delta — two migrations on the v0.87.0 -> v0.88.0 path
(ledger: 63 -> 65 rows):

1. **`sessions/0343-agent-graph-run-specs`** (graph-resolved-spec,
   additive): creates `agent_graph_run_specs` (per-run resolved graph
   spec). Present and empty in this dump. No retention policy exists
   (ledgered; same lifecycle as `agent_graph_events`).
2. **`units/1105-drop-artifacts-legacy`** (units-debt-01UNITD01,
   destructive): DROPS `artifacts_legacy` and
   `artifact_versions_legacy` after copy-verification (damage-evidence
   checks; refuses fail-closed), quarantining orphaned rows into
   `artifacts_legacy_orphans`. On this seed: copy verified, nothing
   orphaned — `artifacts_legacy_orphans` exists and is EMPTY, and the
   legacy tables are GONE. The seed artifact lives only in
   `units`/`unit_versions`.

**This is the first snapshot with no `artifacts` table in ANY form.**
Generation classification must key on table-DDL PRESENCE
(`CREATE TABLE artifacts` / `artifacts_legacy`), never absence — the
selection helpers in `snapshot_generation_test.go` (review finding H1,
units-debt) exist precisely because this snapshot's arrival broke the
absence-keyed logic. `TestMigration1104_*` must keep selecting
pre-1104 snapshots (v0.86.0 and older) and
`TestMigration1105_V087SnapshotBoots` must keep selecting v0.87.0 (the
newest snapshot that CARRIES `artifacts_legacy`) with this directory
present.

## Consumers owed by this snapshot

- The next destructive or selection-sensitive migration boots from
  THIS snapshot (the newest), and from v0.87.0 for anything that needs
  the legacy tables.
- Nothing may ever write to `artifacts_legacy_orphans` after 1105; the
  quarantine refusal (`ErrLegacyArtifactsUnverified` on id collision)
  depends on it.
