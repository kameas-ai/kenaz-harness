# v0.100.0 upgrade snapshot — provenance

Hand-written per the release ritual. `scripts/ci/upgrade-snapshot.sh`
writes only `dump.sql`; this file is the human-authored part.

## How this file was produced

    HOME=$(mktemp -d) KENAZ_HARNESS_ENV=test bash scripts/ci/upgrade-snapshot.sh v0.100.0

Replay mode, `v0.99.0 -> v0.100.0`. Generated 2026-10-22 from
`chore/upgrade-snapshot-v0.100.0`.

- Tag: `v0.100.0`, cut by a manual `tag-on-merge` dispatch (bump=auto) at
  `25d4a76d` ("fix(served-ui): one served capability map, honest session
  chrome, audit lines from served turns", PR #452). The minor bump comes
  from #448 (`feat:`).
- Releases since v0.99.0: #445, #444, #446, #447, #448, #451, #450, #453,
  #452.
- Predecessor snapshot: `v0.99.0`
- dump.sql md5: `64273635a86a6166c6ec15f6213c1e07`

### Toolchain note

Generated offline with Go 1.25.1 (`GOTOOLCHAIN=local`) and a local module
workspace in which `modernc.org/sqlite` was replaced by a shim registering
`github.com/mattn/go-sqlite3` (SQLite 3.53.4, built `-tags fts5`) under the
`sqlite` driver name, because the module proxy was unreachable. The same
setup was first used to regenerate the committed `v0.97.0` snapshot
(`v0.96.0 -> v0.97.0`, a non-zero-delta step); it reproduced that file
byte-for-byte. CI's `upgrade-path` job replays this file under the real
`modernc.org/sqlite` driver.

## What changed on the upgrade path

Two additive `data.db` migrations, both `ALTER TABLE … ADD COLUMN` with
no foreign keys and no row rewrites:

- `sessions/0348-scheduled-chat-runs-session-id` (#444) —
  `scheduled_chat_runs.session_id TEXT`.
- `sessions/0349-scheduled-chat-context-budget` (#448) —
  `scheduled_chat_runs.context_budget_tokens` (default 20000),
  `keep_recent_runs` (default 2), `memory_focus` (default `''`);
  `scheduled_chat_run_history.prompt_message_id TEXT`,
  `compaction_cost_usd REAL`.

The seed rows in `scheduled_chat_runs` and `scheduled_chat_run_history`
carry the new columns at their defaults / NULL; every other table and row
is byte-identical to v0.99.0's dump.

## Checks run on this snapshot

- `go test -run TestUpgradePath ./core/storage/sqlite/`
- `check-upgrade-snapshot-present.sh`
- `check-upgrade-snapshots-locked.sh`
