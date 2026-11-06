# v0.100.3 upgrade snapshot — provenance

Hand-written per the release ritual. `scripts/ci/upgrade-snapshot.sh`
writes only `dump.sql`; this file is the human-authored part.

## How this file was produced

    HOME=$(mktemp -d) KENAZ_HARNESS_ENV=test bash scripts/ci/upgrade-snapshot.sh v0.100.3

Replay mode, `v0.100.2 -> v0.100.3`. Generated 2026-11-04 from
`chore/upgrade-snapshot-v0.100.3`, with the Go caches pointed at the real
ones so the temporary `HOME` didn't re-download modules.

- Tag: `v0.100.3`, cut by a manual `tag-on-merge` dispatch (bump=auto) at
  `f19e90b9` ("fix(memory): stop memory sync crossing accounts on a shared
  device (D164)", PR #469).
- Releases since v0.100.2: #463 (v0.100.2 snapshot), #468, #466, #467, #470,
  #469.
- Predecessor snapshot: `v0.100.2`
- dump.sql md5: `64273635a86a6166c6ec15f6213c1e07` (byte-identical to
  v0.100.2's)

## What changed on the upgrade path

Nothing in `data.db`. No migration landed between v0.100.2 and v0.100.3 (the
only change under `core/storage` is the v0.100.2 snapshot itself), so the
replay applies no pending migrations and reproduces the predecessor.

#469 did change other profile files, which this snapshot doesn't cover: chunks
in `memory.gob` gain `Account`, and `fleet/memory_sync.json` and
`fleet/memory_outbox.json` gain account fields. All are additive. Those files
are covered by `core/fleet/testdata/memory/upgrade/v0.100.2/` and
`memory_sync_account_test.go`.
