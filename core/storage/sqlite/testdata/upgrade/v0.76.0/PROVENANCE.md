# Provenance: `testdata/upgrade/v0.76.0/dump.sql`

**Tag**: `v0.76.0`, cut by `tag-on-merge.yml` from **#321,
`feat(fleet): WP01 — sign ProvisionedMCP + ProviderSetups in the ConfigBundle
wire contract`** — a `feat:` prefix, hence the minor bump.

## How this snapshot was produced

```bash
bash scripts/ci/upgrade-snapshot.sh v0.76.0
```

`replay mode: v0.75.2 -> v0.76.0` — detached worktree at the tag, `v0.75.2`'s
dump materialised into a fresh `data.db` under `$TMPDIR`, that tag's
`storagesqlite.Open` run against it, dumped through `upgradesnap`.

## Schema review

**Byte-identical to `v0.75.2/dump.sql`**, verified rather than assumed:

```
$ cmp v0.75.2/dump.sql v0.76.0/dump.sql          -> 0
$ git diff --stat v0.75.2..v0.76.0 -- '*migration*'  -> (empty)
```

#321 added two fields to the fleet `Bundle` **wire contract** and to
`bundleSigningPayload`. Bundles are signed JSON on the wire, not rows in this
database, so the change registers no migration and replaying `v0.75.2`'s
database under `v0.76.0`'s code alters nothing. Worth stating explicitly for a
*minor* bump: a reader seeing `v0.75.x -> v0.76.0` would reasonably expect
schema movement, and there is none.

## A verification step that was skipped for v0.74.0 through v0.75.2

For those four snapshots the author ran only
`check-upgrade-snapshot-present.sh`, which compares
`max(testdata/upgrade/v*)` against `max(git tag v*)` — a **filename**
comparison. It says nothing about whether the dumps replay.

For this one, CI's actual command was run first, before commit:

```
$ go test ./core/storage/sqlite/... -run TestUpgradePath -count=1 -short -race
ok  github.com/kameas-ai/kenaz-harness/core/storage/sqlite  11.336s
```

Note `-race` and the `/...` — the earlier local checks used neither. Those
four snapshots did turn out to replay cleanly (verified retroactively on
`main`), so nothing was shipped broken. But "the presence gate passed" is not
"the snapshots work," and conflating them is precisely the class of error this
whole directory exists to catch.
