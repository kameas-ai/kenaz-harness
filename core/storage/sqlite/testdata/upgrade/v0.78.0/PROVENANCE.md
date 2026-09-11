# Provenance: `testdata/upgrade/v0.78.0/dump.sql`

**Tag**: `v0.78.0`, cut by `tag-on-merge.yml` from **#340,
`feat: v0.78.0 — surfaces that tell the truth (permission gates, connector
health, hook fan-out)`** — a release roll-up of nine missions.

## How this snapshot was produced

```bash
bash scripts/ci/upgrade-snapshot.sh v0.78.0
```

`replay mode: v0.77.1 -> v0.78.0` — detached worktree at the tag, `v0.77.1`'s
dump materialised into a fresh `data.db` under `$TMPDIR`, that tag's
`storagesqlite.Open` run against it, dumped through `upgradesnap`.

## Schema review

**Byte-identical to `v0.77.1/dump.sql`**, verified rather than assumed:

```
$ cmp v0.77.1/dump.sql v0.78.0/dump.sql            -> 0
$ git diff --stat v0.77.1..v0.78.0 -- '*migration*' -> (empty)
```

Nine missions and no schema change is worth a sentence, because it looks
wrong at first glance. This release was about **removing lies**, not adding
storage: a permission gate that never ran (#339), connector health that was
a hardcoded `StateRunning` literal (#336), a `confirm_each` ladder with no
writer (#333), reasoning tokens billed and never rendered (#329), a hook
surface advertising 18 events and delivering six (#338), a redaction catalog
consulted for 2 of ~21 patterns (#337), and two sub-agent control verbs
(#331, #334). Every one of those is wiring, policy or presentation. The only
new persistence — `confirm_each`'s tool-policy rules — lives in
`<DataDir>/mcp_servers.json`, a plain file, deliberately not a table.

So the empty migration diff is a claim about this release's *shape*, not a
gap in the snapshot.

## Replay verified

Not name-checked — replayed, with the real exit code read directly rather
than from printed output (`go test` in this environment prints canned
success strings that do not track `$?`):

```
$ rtk proxy go test ./core/storage/sqlite/ -run TestUpgradePath -count=1 -v
REAL_EXIT=0
--- PASS: TestUpgradePath/v0.78.0 (2.01s)
ok   .../core/storage/sqlite   2.937s
```

27 per-tag subtests, the whole committed chain, green.

## Why one snapshot for nine missions

The nine PRs were merged into `release/v0.78.0` and landed as a single
squash, so `tag-on-merge.yml` cut exactly one tag. Merging them individually
would have cut nine — and since `check-upgrade-snapshot-present.sh` compares
`max(testdata/upgrade/v*)` to `max(git tag v*)` and fails **every** open PR
until the chain catches up, that path costs a snapshot per merge and starves
whichever PR is trying to pay the debt. `v0.75.2/PROVENANCE.md` records the
six-directory version of that lesson; this release is the shape that avoids
it.
