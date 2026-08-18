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
  against a real upgraded install.
- `core/storage/sqlite/upgrade_path_test.go` — table-driven over every
  directory under `testdata/upgrade/`. Adding a snapshot adds a test case
  with no code change.
- The `upgrade-path` CI job (`.github/workflows/pr.yml`) runs that test on
  every PR. `check-upgrade-snapshots-locked.sh` (a `lint-go` step) fails if
  any file under an already-released tag's snapshot directory changes.

## Cutting a release: add a snapshot

**Every release must add one.** The lock gate catches *modification*;
nothing catches *absence* — skipping this step leaves the `upgrade-path`
job passing while it silently stops covering anything the new release
changed.

```bash
bash scripts/ci/upgrade-snapshot.sh v<X.Y.Z>
```

This replays the previous committed snapshot through a real `git worktree
add` checkout of the new tag's code — not a same-tree simulation — and
writes `core/storage/sqlite/testdata/upgrade/v<X.Y.Z>/dump.sql`. Review the
diff (`git diff testdata/upgrade/<prev>/dump.sql testdata/upgrade/v<X.Y.Z>/dump.sql`
is the release's schema review — nobody had that before this mission),
commit it, and it becomes locked once the release ships.

Requires the new tag to already exist (`git tag` it, or run against `HEAD`
first with `bash scripts/ci/upgrade-snapshot.sh HEAD` to preview before
tagging — that output is NOT committed automatically).

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
- **Building at an old tag fails** — a fresh worktree needs
  `mkdir -p frontend/dist && touch frontend/dist/.gitkeep` before any `go
  build`/`go test` (the binary embeds `frontend/dist`); the script does
  this automatically for tag-replay mode.
