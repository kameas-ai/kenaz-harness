# v0.80.1 upgrade snapshot — provenance

Hand-written per the release ritual. Generated 2026-09-15 via
`bash scripts/ci/upgrade-snapshot.sh v0.80.1` (replay `v0.80.0 -> v0.80.1`)
from `chore/v0.80.1-snapshot`, branched off the squash commit.

- Tag: `v0.80.1` (cut by PR #348, "fix(catalog): stop tests from hitting
  the developer's real macOS keychain")
- Predecessor snapshot: `v0.80.0`

## What the replay applied: nothing — verified, and correct

`diff v0.80.0/dump.sql v0.80.1/dump.sql` is empty. v0.80.1 is a TEST-ONLY
release: two `main_test.go` files adding `keyring.MockInit()` guards.
No production code, no migrations, no schema. The byte-identical dump is
the proof. The snapshot exists solely to keep the chain contiguous with
`max(git tag)` so `check-upgrade-snapshot-present.sh` keeps covering the
next real release — the same gate that (correctly) blocked PR #348 itself
when its branch briefly trailed the v0.80.0 snapshot.

## Caveats

- Immutable from here (`check-upgrade-snapshots-locked.sh`).
- Generation appended to `~/.kenaz/harness.log` (finding #56).
