# v0.93.2 upgrade snapshot — provenance

Hand-written per the release ritual. `scripts/ci/upgrade-snapshot.sh`
writes only `dump.sql`; this file is the human-authored part.

## How this file was produced

    HOME=$(mktemp -d) KENAZ_HARNESS_ENV=test bash scripts/ci/upgrade-snapshot.sh v0.93.2

Replay mode, `v0.93.1 -> v0.93.2`. Generated 2026-10-08 from
`chore/v0.93.2-upgrade-snapshot`.

- Tag: `v0.93.2`
- Squash commit: `8a7c3b65` ("fix(fleet): org_paused is a transient hold
  on every consumer; circuits reopen on unpause; no upsell while paused",
  PR #397)
- Predecessor snapshot: `v0.93.1`

## What changed on the upgrade path

**Zero-delta**: byte-identical to v0.93.1's dump (`cmp`-verified at
generation time; both files hash `3ae89985bd55a3699bcfea0c3ed08f8c`).
No migrations on this path.

`git diff --name-only v0.93.1..v0.93.2 -- '*migration*' core/storage
core/session` lists exactly two files, both of them the **v0.93.1
snapshot itself** (`testdata/upgrade/v0.93.1/{PROVENANCE.md,dump.sql}`,
landed by #395 after the v0.93.1 tag was cut, so they fall inside this
tag range without being a schema change). Nothing under `core/session`
changes and no migration file is touched; the ledger is unchanged.

The patch itself touches `core/fleet` (org_paused transient-hold
handling), `core/rpc` + `core/rpc/views/settings` (fleet session events),
`frontend/` (banner, panels, copy), test deflakes in `core/serve` and
`core/mcp/transport/stdio`, and `docs/unwired-ledger.md`. None of it
reaches sqlite; fleet state lives in fleet / profile-directory JSON, not
`data.db`. Do not hunt for missing migrations.
