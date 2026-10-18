# v0.85.2 upgrade snapshot — provenance

Hand-written per the release ritual (CLAUDE.md, blind spot #3 →
release-ritual corollary). `scripts/ci/upgrade-snapshot.sh` writes only
`dump.sql`; this file is the part a human has to author.

## How this file was produced

    bash scripts/ci/upgrade-snapshot.sh v0.85.2

Replay mode, `v0.85.1 -> v0.85.2`. Generated 2026-10-04 from
`chore/v0.85.2-upgrade-snapshot`, branched off the squash commit.

- Tag: `v0.85.2`
- Squash commit: `4a429485` ("fix(policy): graph file nodes go through
  fs.Gate; corrupt policy fails closed", PR #364)
- Predecessor snapshot: `v0.85.1`

## What changed on the upgrade path

**Zero-delta**: `dump.sql` is byte-identical to v0.85.1's (verified
with `diff`). v0.85.2 ships no migrations — the patch is Cedar policy
enforcement (fail-closed on corrupt user policy, graph file nodes
through fs.Gate, snippet-preserving Reload), none of which touches the
harness database schema. The snapshot keeps the chain level with the
newest tag so `check-upgrade-snapshot-present.sh` stays green.
