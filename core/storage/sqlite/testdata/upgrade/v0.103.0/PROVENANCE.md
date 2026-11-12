# v0.103.0 upgrade snapshot — provenance

Hand-written per the release ritual. `scripts/ci/upgrade-snapshot.sh`
writes only `dump.sql`; this file is the human-authored part.

## How this file was produced

    HOME=$(mktemp -d) KENAZ_HARNESS_ENV=test bash scripts/ci/upgrade-snapshot.sh v0.103.0

Replay mode, `v0.102.0 -> v0.103.0`. Generated 2026-11-10 from
`chore/v0.103.0-upgrade-snapshot` (branched at `be84b240`, main's tip and
the tagged commit), with `GOMODCACHE` / `GOCACHE` / `GOPATH` pointed at the
real caches so the temporary `HOME` didn't re-download modules.

- Tag: `v0.103.0`, cut by a manual `tag-on-merge` dispatch on 2026-11-11
  (run 42935814148) at `be84b240` ("feat(served): bash in workbenches
  gated by fleet Cedar policy", PR #477).
- Releases since v0.102.0: #477 only (#476 was the v0.102.0 snapshot
  itself and shipped in the same tag range without bumping a version).
- Predecessor snapshot: `v0.102.0`
- dump.sql md5: `64273635a86a6166c6ec15f6213c1e07` (byte-identical to
  v0.102.0's, v0.101.0's and v0.100.3's — verified with `md5sum` against
  the committed predecessor after generation)

## What changed on the upgrade path

Nothing in `data.db`. No migration landed between v0.102.0 and v0.103.0
(the only change under `core/storage` in that range is the v0.102.0
snapshot itself), so the replay applies no pending migrations and
reproduces the predecessor.

#477 touched `core/policy/cedar` (`PermittedBySnippet` helper),
`core/fleet/cedar_apply.go`, `core/rpc` (served-mode bash gate wiring in
`builtins_wiring.go` / `api.go`), `cmd/harness-served/main.go`, `main.go`
and docs. None of it reads or writes on-disk state the snapshot covers;
the behaviour is exercised by `served_bash_fleet_policy_test.go`,
`permitted_by_snippet_test.go` and `bash_prompt_roundtrip_test.go`.
