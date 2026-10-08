# v0.93.1 upgrade snapshot — provenance

Hand-written per the release ritual. `scripts/ci/upgrade-snapshot.sh`
writes only `dump.sql`; this file is the human-authored part.

## How this file was produced

    HOME=$(mktemp -d) KENAZ_HARNESS_ENV=test bash scripts/ci/upgrade-snapshot.sh v0.93.1

Replay mode, `v0.93.0 -> v0.93.1`. Generated 2026-10-07 from
`chore/v0.93.1-upgrade-snapshot`.

- Tag: `v0.93.1`
- Squash commit: `34138c5b` ("fix: ledger follow-ups — chat-runner
  shutdown drain, memory-sync stop, served node_removed clear,
  prerelease updater host, stray binary", PR #394)
- Predecessor snapshot: `v0.93.0`

## What changed on the upgrade path

**Zero-delta**: byte-identical to v0.93.0's dump (`cmp`-verified at
generation time; both files hash `3ae89985bd55a3699bcfea0c3ed08f8c`).
No migrations on this path.

`git diff --name-only v0.93.0..v0.93.1 -- '*migration*' core/storage
core/session` lists exactly two files, both of them the **v0.93.0
snapshot itself** (`testdata/upgrade/v0.93.0/{PROVENANCE.md,dump.sql}`,
landed by #391 after the v0.93.0 tag was cut — so they fall inside this
tag range without being a schema change). Nothing under `core/session`
changes. No `core/session/migrations*.go` file is touched; the ledger
stays at 66 rows / sessions block at 45 (last entry
`sessions/0344-turn-run-outcome`, see v0.93.0's provenance).

The patch itself is runtime-lifecycle and release-infra work, none of
which reaches sqlite:

- `core/rpc/views/agentgraph/chat/{chat_runner,run_drain}.go` — drain
  in-flight chat runs on shutdown (writes through the *existing* 0344
  outcome columns; no new columns).
- `core/fleet/memory_sync.go`, `core/rpc/views/settings/{api,fleet}.go`
  — memory-sync lane stop + served-mode `node_removed` clear. Fleet
  state lives in fleet / profile-directory JSON, not `data.db`.
- `core/memory/store.go` — `chromemStore` gains an injectable
  `capture *CaptureRateTracker` field (process-scoped counter; the
  chromem store is a JSON-on-disk document store, not sqlite).
- `core/update/{manifest,doc}.go`, `core/mlsidecar/{release,release_key,
  demand}.go` — prerelease updater host + engine-release 0.1.1 fixture.
  Network/filesystem only.
- `.github/workflows/*`, `scripts/ci/{check-release-integrity,
  check-semver-lib,lib/semver}.sh`, `.github/release-integrity-ignore.txt`
  — CI only.

## Non-storage change worth recording

`kenaz-harness` (a 58,084,818-byte `go build` output at the repo root,
blob `39e6a47c`, mode `100755`) was committed by accident in #390 and
shipped inside the v0.93.0 tag. #394 deletes it (`D kenaz-harness`) and
adds `/kenaz-harness` to `.gitignore`. It is a tree-content change
only: the binary never participated in the build, in `Open()`, or in
any migration, so it has no bearing on the snapshot — noted here so
nobody reading `git diff --stat v0.93.0..v0.93.1` mistakes a 55 MB
deletion for a storage event.

## State outside sqlite's purview

Nothing new. The memory capture-rate tracker is in-process and
unpersisted; memory-sync lane state remains in the fleet data dir.
Do not hunt for missing migrations.
