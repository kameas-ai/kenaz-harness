# v0.100.1 upgrade snapshot — provenance

Hand-written per the release ritual. `scripts/ci/upgrade-snapshot.sh`
writes only `dump.sql`; this file is the human-authored part.

## How this file was produced

    HOME=$(mktemp -d) KENAZ_HARNESS_ENV=test bash scripts/ci/upgrade-snapshot.sh v0.100.1

Replay mode, `v0.100.0 -> v0.100.1`. Generated 2026-10-26 from
`chore/upgrade-snapshot-v0.100.1`.

- Tag: `v0.100.1`, cut by a manual `tag-on-merge` dispatch (bump=auto) at
  `13fcbce3` ("fix(security): rebuild on go1.27.3 for CVE-2026-111943
  (eMudhra CA denylist)", PR #460). Patch bump: everything since v0.100.0
  is `fix:` / `deps:` / `ci:` / `chore:`.
- Releases since v0.100.0: #455 (v0.100.0 snapshot), #454, #456, #457,
  #458, #460.
- Predecessor snapshot: `v0.100.0`
- dump.sql md5: `64273635a86a6166c6ec15f6213c1e07` (byte-identical to
  v0.100.0's)

### Toolchain note

Generated online with Go 1.27.3 (the module's `toolchain` line) and the
real `modernc.org/sqlite` driver — no offline shim this time.

## What changed on the upgrade path

Nothing. No `data.db` migration was added between v0.100.0 and v0.100.1
(no change under `core/storage` besides the v0.100.0 snapshot itself), so
replaying v0.100.0's dump under v0.100.1's `Open()` applies no pending
migrations and reproduces the predecessor byte-for-byte. The snapshot
exists to keep the chain current with the newest release tag
(`check-upgrade-snapshot-present.sh`).

## Checks run on this snapshot

- `go test -run TestUpgradePath ./core/storage/sqlite/`
- `check-upgrade-snapshot-present.sh`
- `check-upgrade-snapshots-locked.sh`
