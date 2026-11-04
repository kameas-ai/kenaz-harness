# Memory sync profile written by v0.100.2

Written on 2026-11-04 by that release's own code, at tag `v0.100.2` (`ddbf1a7d`),
through `memory.NewChromemStore`, `memory.NewHLC`, `memory.OpenForgetOutbox` and
`memSyncState`, the way an installed profile is written. No account field exists
in any of it.

- `memory.gob`: `v0100-unsynced` (long-term, never synced), `v0100-synced`
  (global, accepted by Fleet), `v0100-session` (session).
- `fleet/memory_outbox.json`: one queued forget, `v0100-forgotten`.
- `fleet/memory_sync.json`: sync on, cursor `42`.
- `fleet/memory_hlc.json`: the clock state.

Do not regenerate these with newer code. `memory_sync_account_test.go` reads them.
