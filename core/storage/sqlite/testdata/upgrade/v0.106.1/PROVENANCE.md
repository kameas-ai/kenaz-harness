# v0.106.1 upgrade snapshot — provenance

Hand-written per the release ritual. `scripts/ci/upgrade-snapshot.sh`
writes only `dump.sql`; this file is the human-authored part.

## How this file was produced

    HOME=$(mktemp -d) KENAZ_HARNESS_ENV=test bash scripts/ci/upgrade-snapshot.sh v0.106.1

Replay mode, `v0.106.0 -> v0.106.1`. Generated 2026-11-14 from
`chore/upgrade-snapshot-v0.106.1` (branched at `95ec725e`, the tagged
commit), with `GOMODCACHE` / `GOCACHE` / `GOPATH` pointed at the real caches.

- Tag: `v0.106.1`, cut by a manual `tag-on-merge` dispatch on 2026-11-14
  (run 43630746773) at `95ec725e` (PR #500).
- Releases since v0.106.0: #499, #498, #500 (#497 is no-bump).
- Predecessor snapshot: `v0.106.0`
- dump.sql md5: `ea4d6478c3071bca355928ff19cbf216` (byte-identical to
  v0.106.0's)

## What changed on the upgrade path

Nothing in `data.db`. No migration landed between v0.106.0 and v0.106.1.

New state in this range lives outside `data.db`, all 0600 JSON under
`<dataDir>/fleet/` and removed on sign-out:
- `key_reauth_refused.json` (#499)
- `context_pulled_locations.json` (#499)

#500 adds a Cedar `resolved_path` context attribute to newly written
filesystem grants. Grants written by older releases keep matching, and the
gate additionally requires the resolved location to be allowed. No stored
policy is rewritten.
