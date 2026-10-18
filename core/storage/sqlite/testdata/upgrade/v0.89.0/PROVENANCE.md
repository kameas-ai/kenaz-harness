# v0.89.0 upgrade snapshot — provenance

Hand-written per the release ritual. `scripts/ci/upgrade-snapshot.sh`
writes only `dump.sql`; this file is the human-authored part.

## How this file was produced

    HOME=$(mktemp -d) KENAZ_HARNESS_ENV=test bash scripts/ci/upgrade-snapshot.sh v0.89.0

Replay mode, `v0.88.0 -> v0.89.0`. Generated 2026-10-05 from
`chore/v0.89.0-snapshot-and-cla-runner`.

- Tag: `v0.89.0`
- Squash commit: `2222c91f` ("feat: v0.89.0 — one Capabilities rail
  entry (install-framework Phase 4, pulled forward)", PR #372)
- Predecessor snapshot: `v0.88.0`

## What changed on the upgrade path

**Zero-delta**: byte-identical to v0.88.0's dump (diff-verified).
v0.89.0 ships no migrations — the feature is the frontend/nav
consolidation (one Capabilities rail entry replacing Tools +
Marketplace; MarketplaceView deleted) plus Go copy changes. The
snapshot keeps the chain level with the newest tag.
