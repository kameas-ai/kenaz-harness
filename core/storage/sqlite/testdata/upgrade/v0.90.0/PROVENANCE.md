# v0.90.0 upgrade snapshot — provenance

Hand-written per the release ritual. `scripts/ci/upgrade-snapshot.sh`
writes only `dump.sql`; this file is the human-authored part.

## How this file was produced

    HOME=$(mktemp -d) KENAZ_HARNESS_ENV=test bash scripts/ci/upgrade-snapshot.sh v0.90.0

Replay mode, `v0.89.4 -> v0.90.0`. Generated 2026-10-06 from
`chore/v0.90.0-upgrade-snapshot`.

- Tag: `v0.90.0`
- Squash commit: `c77f392c` ("feat: v0.90.0 — the model's first harness
  tools, scheduled-run containment, and bundle signing readiness",
  PR #384)
- Predecessor snapshot: `v0.89.4` (whose dump.sql was backfilled in the
  same PR — see that directory's PROVENANCE)

## What changed on the upgrade path

**Zero-delta**: byte-identical to v0.89.4's dump (`cmp`-verified at
generation time). No migrations on this path — the v0.90.0 release
review's claim that `git diff v0.89.4..v0.90.0` touches no
migration/schema files was re-verified independently against the dump
diff while producing this snapshot. The snapshot keeps the chain level
with the newest tag.
