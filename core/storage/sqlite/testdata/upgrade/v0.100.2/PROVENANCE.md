# v0.100.2 upgrade snapshot — provenance

Hand-written per the release ritual. `scripts/ci/upgrade-snapshot.sh`
writes only `dump.sql`; this file is the human-authored part.

## How this file was produced

    HOME=$(mktemp -d) KENAZ_HARNESS_ENV=test bash scripts/ci/upgrade-snapshot.sh v0.100.2

Replay mode, `v0.100.1 -> v0.100.2`. Generated 2026-10-26 from
`chore/upgrade-snapshot-v0.100.2`.

- Tag: `v0.100.2`, cut by a manual `tag-on-merge` dispatch (bump=auto) at
  `ddbf1a7d` ("fix(release): pin Wails CLI v2.17.0 so wails build loads
  go1.27.3 packages", PR #462). The v0.100.1 release build failed on the
  old Wails CLI; v0.100.2 is the first published go1.27.3 harness.
- Releases since v0.100.1: #461 (v0.100.1 snapshot), #459, #462.
- Predecessor snapshot: `v0.100.1`
- dump.sql md5: `64273635a86a6166c6ec15f6213c1e07` (byte-identical to
  v0.100.1's)

### Toolchain note

Generated online with Go 1.27.3 and the real `modernc.org/sqlite` driver.

## What changed on the upgrade path

Nothing. No `data.db` migration landed between v0.100.1 and v0.100.2 (the
only change under `core/storage` is the v0.100.1 snapshot itself), so the
replay applies no pending migrations and reproduces the predecessor
byte-for-byte. The snapshot keeps the chain current with the newest
release tag (`check-upgrade-snapshot-present.sh`).

## Checks run on this snapshot

- `go test -run TestUpgradePath ./core/storage/sqlite/`
- `check-upgrade-snapshot-present.sh`
- `check-upgrade-snapshots-locked.sh`
