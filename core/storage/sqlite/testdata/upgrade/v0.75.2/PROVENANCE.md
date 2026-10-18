# Provenance: `testdata/upgrade/v0.75.2/dump.sql`

**Tag**: `v0.75.2`, cut by `tag-on-merge.yml` from **#320,
`fix(fleet): stop the unbacked-off enroll poll and stop leaking raw JSON on
sign-in`**.

## How this snapshot was produced

```bash
bash scripts/ci/upgrade-snapshot.sh v0.75.2
```

`replay mode: v0.75.1 -> v0.75.2` — detached worktree at the tag, `v0.75.1`'s
dump materialised into a fresh `data.db` under `$TMPDIR`, that tag's
`storagesqlite.Open` run against it, dumped through `upgradesnap`.

## Schema review

**Byte-identical to `v0.75.1/dump.sql`**, verified rather than assumed:

```
$ cmp v0.75.1/dump.sql v0.75.2/dump.sql          -> 0
$ git diff --stat v0.75.1..v0.75.2 -- '*migration*'  -> (empty)
```

A fix to a frontend polling interval and a Go error envelope registers no
migration.

## The sixth, and why this PR stopped growing

`v0.75.2` was cut while this PR was in review — the sixth such tag. The five
before it were `v0.73.2`, `v0.73.3`, `v0.74.0`, `v0.75.0` and `v0.75.1`.

**What made it stop:** the merge automation was running several sweeps
concurrently, one of which merged approved PRs without waiting for the
snapshot chain to catch up. Each of those merges cut a tag, which re-failed
`check-upgrade-snapshot-present.sh` on every other open PR, which is why this
PR kept needing another directory. Stopping the out-of-order sweep is what
broke the loop — not adding snapshots faster.

**The terminating procedure**, for whoever hits a burst of merges next:

1. Fold **every** outstanding tag into one snapshot PR.
2. Stop anything that merges tag-cutting PRs until that snapshot PR lands.
3. Land it — `chore:` cuts no tag, so the chain stays current.
4. Re-run the remaining PRs so they go green **simultaneously**, then merge
   back-to-back. The branch ruleset has
   `strict_required_status_checks_policy: false`, so a recorded green result
   stays valid for its SHA even as `main` moves beneath it.
5. Pay the resulting debt once, in a final snapshot PR.

Chasing each tag with a fresh snapshot PR does not terminate while merges
continue. That is the whole lesson, and it cost six directories to learn.
