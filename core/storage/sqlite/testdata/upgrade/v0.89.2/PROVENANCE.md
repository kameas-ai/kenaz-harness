# v0.89.2 upgrade snapshot — provenance

Hand-written per the release ritual. `scripts/ci/upgrade-snapshot.sh`
writes only `dump.sql`; this file is the human-authored part.

## How this file was produced

    HOME=$(mktemp -d) KENAZ_HARNESS_ENV=test bash scripts/ci/upgrade-snapshot.sh v0.89.2

Replay mode, `v0.89.1 -> v0.89.2`. Generated 2026-10-05 from
`chore/v0.89.2-upgrade-snapshot`.

- Tag: `v0.89.2`
- Squash commit: PR #376 ("fix(mlsidecar): per-env engine port lanes
  with engine.port discovery and verified-port pinning (A5.3)")
- Predecessor snapshot: `v0.89.1`

## What changed on the upgrade path

**Zero-delta**: byte-identical to v0.89.1's dump (diff-verified).
v0.89.2 ships no migrations — the fix is the ML-engine port lane scheme
(prod 7774 / dev 7785 / test 7786 + engine.port discovery + verified-
port pinning), all runtime/filesystem state under ~/.kenaz/ml, outside
the harness database. The snapshot keeps the chain level with the
newest tag.
