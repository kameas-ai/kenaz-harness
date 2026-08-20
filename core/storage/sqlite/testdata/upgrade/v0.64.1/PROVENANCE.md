# Provenance: `testdata/upgrade/v0.64.1/dump.sql`

**Tag**: `v0.64.1` (commit `88b7a75e`, PR #295, *"fix(fleet): bake prod realm
defaults into source so prod builds are Configured()"*) — the latest
release tag at the time `entry-points-and-crash-reporting-01PMZD13` UNIT-3
was implemented. `docs/roadmap.md` and the mission spec both cite `v0.64.0`
as latest; `v0.64.1` landed after the spec was written and before this unit
ran (`git tag --sort=-v:refname` re-checked per `plan.md`'s out-of-band
check #1, which anticipated exactly this). This snapshot — not `v0.64.0`'s —
is what `check-upgrade-snapshots-locked.sh`'s new missing-snapshot check
(added in this same commit) actually requires, since that check computes
`LATEST_TAG` dynamically rather than trusting the spec's citation.

## How this snapshot was produced

```bash
bash scripts/ci/upgrade-snapshot.sh v0.64.1
```

which resolved `PREV_TAG=v0.64.0` (committed in this same commit — see its
own `PROVENANCE.md`), created a detached `git worktree` at
`refs/tags/v0.64.1` (`HEAD is now at 88b7a75e`), materialised
`v0.64.0/dump.sql` into a fresh `data.db`, ran that tag's
`storagesqlite.Open` against it, and dumped the result. Temporary worktree
removed by the script's own cleanup trap.

## Schema review for v0.64.1

**`dump.sql` is byte-identical to `v0.64.0/dump.sql`** (and therefore to
`v0.63.2/dump.sql` as well — three consecutive releases with no schema
change):

```
$ diff core/storage/sqlite/testdata/upgrade/v0.64.0/dump.sql \
       core/storage/sqlite/testdata/upgrade/v0.64.1/dump.sql
(no output)
```

Consistent with the tag's own subject — a Go-source default-value fix in
`core/fleet`, not a storage change. No new `harness_migrations` row.
