# Provenance: `testdata/upgrade/v0.69.0/dump.sql`

**Tag**: `v0.69.0` — cut by `tag-on-merge.yml` when PR #304 squash-merged
("model-authored graphs complete, and the serve drift gate starts gating").

## How this snapshot was produced

```bash
bash scripts/ci/upgrade-snapshot.sh v0.69.0
```

Replay from `v0.68.0`, using HEAD's generator: the tag supplies the
database STATE, HEAD supplies the dump FORMAT.

## The first snapshot the gate did not have to catch

`check-upgrade-snapshot-present.sh` had fired on **five consecutive
releases** — v0.65.0, v0.65.1, v0.66.0, v0.67.0, v0.68.0 — every one since
it shipped. This is the first release where the snapshot was cut as part
of the merge rather than after a red gate demanded it.

That is worth exactly as much as it sounds like, and no more: one
release. The convention it replaces failed on eight consecutive releases,
each of which documented the gap and left it open. The gate's value was
never that it would teach anyone to remember — it was that forgetting
became cheap and loud instead of silent and compounding. If the next
release is caught by the gate again, nothing has regressed; the system is
working as designed.

## Schema review for v0.69.0

744 lines, 52 ledger rows, high-water `event-log/0106-events-fts-sync` —
**byte-identical to `v0.68.0/dump.sql`**, which is in turn identical to
`v0.67.0` and `v0.66.0`.

Correct and expected. v0.69.0 shipped two missions — GA01 UNIT-7
(model-authored graph drafting) and Z707 WP02/WP03/WP05 (the serve drift
gate promotion plus two served-mode truthfulness fixes) — and registered
no migration. Agent graphs persist as YAML files under the profile's
`agent_graph/library/`, not as rows, so a release that adds a whole
graph-authoring path can legitimately leave the schema untouched.

Four identical snapshots in a row is itself a finding worth stating
plainly: the storage layer has been stable since v0.66.0, and the chain
is carrying four directories that differ only in name. The growth policy
escalation in `docs/upgrade-snapshots.md` is still unresolved; these are
the cheapest candidates under whatever it eventually ratifies. They are
kept because the property that matters — *the chain reaches the latest
release tag* — is not expressible without the newest one, and that
property is now gated.

## Verification

`TestUpgradePath` must show `v0.69.0` as its own `RUN`/`PASS` pair opening
a real database. A green suite is not evidence that a new snapshot is
covered.
