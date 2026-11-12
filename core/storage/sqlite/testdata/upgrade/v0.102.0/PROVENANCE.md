# v0.102.0 upgrade snapshot — provenance

Hand-written per the release ritual. `scripts/ci/upgrade-snapshot.sh`
writes only `dump.sql`; this file is the human-authored part.

## How this file was produced

    HOME=$(mktemp -d) KENAZ_HARNESS_ENV=test bash scripts/ci/upgrade-snapshot.sh v0.102.0

Replay mode, `v0.101.0 -> v0.102.0`. Generated 2026-11-10 from
`chore/upgrade-snapshot-v0.102.0` (branched at `8fe605c3`, main's tip and
the tagged commit), with `GOMODCACHE` / `GOCACHE` / `GOPATH` pointed at the
real caches so the temporary `HOME` didn't re-download modules.

- Tag: `v0.102.0`, cut by a manual `tag-on-merge` dispatch on 2026-11-10
  (run 42808267843) at `8fe605c3` ("feat(fleet): report workbench identity
  and honour check-in requests", PR #474).
- Releases since v0.101.0: #474 only.
- Predecessor snapshot: `v0.101.0`
- dump.sql md5: `64273635a86a6166c6ec15f6213c1e07` (byte-identical to
  v0.101.0's, and to v0.100.3's)

## What changed on the upgrade path

Nothing in `data.db`. No migration landed between v0.101.0 and v0.102.0
(the only change under `core/storage` in that range is the v0.101.0
snapshot itself), so the replay applies no pending migrations and
reproduces the predecessor.

#474 changed `core/fleet` only: the enroll body gained `arch`,
`os_version`, `client_kind`, `in_vm`, `vm_id`, `host_node_id`
(`node_facts.go`), and the config poller re-ACKs on
`X-Fleet-Checkin-Requested`. Neither touches on-disk state the snapshot
covers; both are exercised by `enroll_node_facts_test.go` and
`config_pull_test.go`.
