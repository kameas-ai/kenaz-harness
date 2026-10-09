# v0.93.3 upgrade snapshot — provenance

Hand-written per the release ritual. `scripts/ci/upgrade-snapshot.sh`
writes only `dump.sql`; this file is the human-authored part.

## How this file was produced

    HOME=$(mktemp -d) KENAZ_HARNESS_ENV=test bash scripts/ci/upgrade-snapshot.sh v0.93.3

Replay mode, `v0.93.2 -> v0.93.3`. Generated 2026-10-08 from
`chore/v0.93.3-upgrade-snapshot`.

- Tag: `v0.93.3`
- Squash commit: `57f66346` ("fix: dogfood 2026-10-08 — audit default
  filter, draft persistence, fleet chip, log flood, team-layer publish",
  PR #399)
- Predecessor snapshot: `v0.93.2`

## What changed on the upgrade path

**Zero-delta**: byte-identical to v0.93.2's dump (`cmp`-verified at
generation time; both files hash `3ae89985bd55a3699bcfea0c3ed08f8c`).
No migrations on this path.

`git diff --name-only v0.93.2..v0.93.3 -- '*migration*' core/storage
core/session` lists exactly two files, both of them the **v0.93.2
snapshot itself** (`testdata/upgrade/v0.93.2/{PROVENANCE.md,dump.sql}`,
landed after the v0.93.2 tag was cut, so they fall inside this tag range
without being a schema change). Nothing under `core/session` changes and
no migration file is touched.

The patch itself touches `core/fleet/config_pull.go`, `core/rpc/api.go`,
`core/rpc/views/{audit,contexts,llm}`, `frontend/` (audit view, draft
persistence, fleet chip, contexts, settings, tools), `docs/` and one CI
allowlist. None of it reaches the sqlite schema.
