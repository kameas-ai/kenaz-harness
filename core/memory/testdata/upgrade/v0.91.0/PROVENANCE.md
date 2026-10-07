# v0.91.0 memory.gob — provenance

Written by the **v0.91.0 release's own `core/memory` code** (a detached
worktree at tag `v0.91.0`, a throwaway `go run` program that was never
committed) for memory-sync-01MEMSY01 WP-PI: the pre-mission gob shape, with
none of the HLC / recall-split / sync fields. Three chunks:

- `mem-v091-global` — global, titled, pinned, `files_read` set, recalled 3x
  via `MarkAccessed` (legacy `RecallCount=3`), session `seed-session-1`.
- `mem-v091-session` — session-scoped to `seed-session-1` (the session the
  v0.91.0 sqlite snapshot seeds), for the WP09 delete cascade.
- `mem-v091-longterm` — long_term, `narrative_synthesised`, `turn_id`
  `turn-42`, retrieval weight 1.5.

Consumers: core/memory TestUpgrade_V091MemoryGob, core/fleet
TestMemorySync_V091GobFullSyncCycle, core/rpc
TestMemorySync_UpgradedProfileBoot. Do not regenerate with HEAD code — that
would stop testing the upgrade path.
