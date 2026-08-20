# Upgrade-path snapshots

How the harness proves a previously-shipped database still opens under
HEAD, and what a release owner has to do to keep that true. Background:
`kitty-specs/upgrade-path-coverage-01PMUG01/spec.md`, prompted by the
v0.63.0 P0 — every test in the suite starts from an empty database, which
structurally cannot exercise a migration-selection defect that only exists
on the path from a previously-shipped schema to this one.

## What exists

- `core/storage/sqlite/testdata/upgrade/seed.sql` — a synthetic, fixed-ID
  seed corpus, applied once at the genesis tag and carried forward by every
  later snapshot.
- `core/storage/sqlite/testdata/upgrade/<tag>/dump.sql` — a normalised text
  dump (schema DDL + ledger + the seed rows in whatever shape that tag's
  migrations left them) of the database that tag's code produces when it
  replays the previous tag's snapshot. `<tag>/PROVENANCE.md` next to it
  records how it was built and, for the genesis, how it was cross-checked
  against a real upgraded install — **hand-written**; the generator does
  not produce it (see *Cutting a release* below).
- `core/storage/sqlite/upgrade_path_test.go` — table-driven over every
  directory under `testdata/upgrade/`. Adding a snapshot adds a test case
  with no code change.
- The `upgrade-path` CI job (`.github/workflows/pr.yml`) runs that test on
  every PR. `check-upgrade-snapshots-locked.sh` (a `lint-go` step) fails if
  any file under an already-released tag's snapshot directory changes.

## Cutting a release: add a snapshot

**Every release must add one**, and CI now enforces it.
`check-upgrade-snapshots-locked.sh` catches *modification* of a committed
snapshot; `check-upgrade-snapshot-present.sh` catches *absence*, failing
when the newest snapshot is older than the newest stable `vX.Y.Z` tag.

Before that gate existed, skipping this step left the `upgrade-path` job
passing while it silently stopped covering anything the new release changed
— which is exactly what happened on `v0.63.2`, `v0.64.0` and `v0.64.1`. Each
of those releases' PROVENANCE.md files correctly named the missing gate and
stopped there.

```bash
bash scripts/ci/upgrade-snapshot.sh v<X.Y.Z>
```

This replays the previous committed snapshot through a real `git worktree
add` checkout of the new tag's code — not a same-tree simulation — and
writes `core/storage/sqlite/testdata/upgrade/v<X.Y.Z>/dump.sql`.

Then review the diff. **Use `--no-index`:**

```bash
git diff --no-index \
  core/storage/sqlite/testdata/upgrade/<prev>/dump.sql \
  core/storage/sqlite/testdata/upgrade/v<X.Y.Z>/dump.sql
```

That diff *is* the release's schema review — nobody had that before this
mission. Without `--no-index`, git reads two tracked paths as *pathspecs*
and diffs the worktree against the index, which for a freshly committed
snapshot is empty: the command exits 0 and prints nothing, which is the
worst possible failure mode for a review step.

An empty diff is a legitimate outcome — a release with no schema change
produces a byte-identical dump (`v0.63.2` is exactly that: identical to
`v0.63.1`). Commit it anyway; the chain reaching the latest release tag is
what makes "the previous release's database still opens" a true statement
rather than a statement about some older release.

**Then hand-write `<tag>/PROVENANCE.md`.** The generator writes only
`dump.sql`. Copy the shape of `v0.63.1/PROVENANCE.md`: which tag, which
snapshot it was replayed from, which migrations that tag newly applied,
and anything you had to work around. A snapshot directory without a
PROVENANCE.md is an unexplained fixture, and the next person to see a diff
in it has nothing to check it against.

Requires the new tag to already exist (`git tag` it, or run against `HEAD`
first with `bash scripts/ci/upgrade-snapshot.sh HEAD` to preview before
tagging). **Delete `testdata/upgrade/HEAD/` when you are done with it** —
`upgrade_path_test.go` skips any directory that is not a `vX.Y.Z` name
(`upgradesnap.IsSnapshotTag`), so a leftover preview cannot silently become
a chain entry, but it should not be committed either.

## Why a migration needs a populated-table test, separately

Repairing *selection* (the v0.63.1 `Pending()` fix) is what aims a dormant
migration at real rows for the first time. `I14`
(`scripts/ci/check-destructive-migration-coverage.sh`) enforces that every
migration whose `Up()` runs a destructive statement (`DROP TABLE`,
`DROP INDEX`, `DROP TRIGGER`, `DROP COLUMN`, `ALTER TABLE ... RENAME`, or a
non-scratch `DELETE FROM`) has its own populated-table test — see
`core/storage/sqlite/migration_0327_test.go` for the pattern: rewind the
migration's own ledger row, seed the target table (and every table with an
FK into it), reopen, assert content survives.

## Troubleshooting

- **"no committed snapshot older than `<tag>` found"** — the chain
  bootstraps at the genesis tag (`v0.63.0`); build/commit that first.
- **Regeneration doesn't match the committed byte-for-byte** — a finding,
  not something to silence. The normalisation rules
  (`core/storage/sqlite/upgradesnap`) are meant to be deterministic; a
  divergence means either the generator changed (review it) or the
  committed snapshot was hand-edited (don't do that — regenerate instead).
- **Snapshot growth.** Roughly 42 KB of `dump.sql` per release. Spec §10
  escalation 4 proposes "keep the current minor plus the last three minors,
  prune older ones in a dated commit" — **this has not been ratified**, and
  until it is, the directory grows without bound. Note that a release with
  no schema change contributes a byte-identical duplicate, which is the
  cheapest possible candidate for pruning first.
- **Building at an old tag fails** — a fresh worktree needs
  `mkdir -p frontend/dist && touch frontend/dist/.gitkeep` before any `go
  build`/`go test` (the binary embeds `frontend/dist`); the script does
  this automatically for tag-replay mode.
