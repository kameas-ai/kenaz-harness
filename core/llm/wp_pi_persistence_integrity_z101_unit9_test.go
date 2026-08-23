package llm_test

// WP-PI — persistence integrity for model-settings-reach-the-model-01PMZ101,
// scoped to THIS landing: UNIT-9 (WP13 — real "ollama" adapter kind;
// WP14 — capability probe, SQLite cache, CapabilityHints overlay).
// core/llm/wp_pi_persistence_integrity_z101_test.go covers an EARLIER
// session's landing (WP04/WP05/WP15) and explicitly left UNIT-9 in its
// "not landed" list — this file is the promised follow-up, not a
// duplicate.
//
// Per kitty-specs/_templates/WP-persistence-integrity.md (mandatory in
// every mission) and this mission's tasks.md UNIT-PI ("merge gate ...
// lands with or before the last of UNIT-4 / UNIT-6 / UNIT-9"). This
// landing is UNIT-9 itself; UNIT-PI (WP12's gates + the mission-closing
// report) is a separate WP this landing does not claim to be.
//
// Register G-6 (docs/escalation-register-2026-08-19.md Part 10,
// 2026-08-22) ruled E-005 the same day this landing started: "register
// the new kind, leave existing custom-openai profiles alone. NO
// migration." That ruling is why this WP-PI is straightforward —
// register G-6 says so explicitly: "Z101 UNIT-9 ... is NOT a
// destructive migration — it takes no profile rows, needs no
// populated-table test beyond the ordinary upgrade-path assertion, and
// cannot strand an existing install."
//
// # AC-PI-4 — per-WP enumeration
//
//   - WP13 (fix(llm): register a real ollama adapter kind and correct
//     ollama.yaml) — NO NEW table/migration/setting/FTS index.
//     core/llm/ollama/ is a new package (Go code only — the HTTP
//     adapter, its SSE stream consumer, and the /api/show probe
//     client); core/llm/registry/registry.go gains one conditional
//     registration line, the same env-flag-gated shape as
//     custom-openai's; core/llm/capabilities/data/ollama.yaml is a
//     data-file edit (embedded via embed.FS, compiled into the binary
//     — not a database artifact). It does not write, migrate, or read
//     any ProviderProfile row — existing "custom-openai" profiles are
//     untouched (register G-6's own words: "leave existing
//     custom-openai profiles alone").
//   - WP14 (feat(llm): probe custom endpoints, cache the result, and
//     let CapabilityHints override the catalog) — PERSISTENCE-BEARING,
//     but adds no migration. core/llm/capabilities/cache_sqlite.go is
//     the FIRST production code to write to provider_capabilities
//     (migration sessions/0329-provider-capabilities, shipped in
//     v0.63.0 per spec §11's correction to register A-5 — "the table
//     and the migration already shipped in v0.63.0"). D-5 survives:
//     WP14 adds code only. gate.go's CapabilityHints overlay
//     (applyCapabilityHints / applyVisionHintToAttachments) reads an
//     in-memory ProviderProfile field already declared before this
//     mission — no new persisted setting.
//   - WP-PI (this file) — none; adds only this enumeration and the
//     falsifiability re-run below.
//
// # AC-PI-1 — tests boot from a previous-release database
//
// provider_capabilities is present (empty) in every committed
// snapshot. The newest snapshot in this tree as of this landing is
// v0.70.0 (core/storage/sqlite/testdata/upgrade/v0.70.0/) — NOT
// v0.71.0, even though v0.71.0 is the newest git tag (`git tag
// --sort=-v:refname | head -1` = v0.71.0; `ls
// core/storage/sqlite/testdata/upgrade/` tops out at v0.70.0). This is
// the SAME pre-existing gap the earlier z101 WP-PI file flagged at
// v0.66.0/v0.65.1 (`check-upgrade-snapshot-present.sh` would fail on
// this tree today, RUN and confirmed) — not caused by this landing,
// and this landing is not "the last mission before a release tag" (see
// AC-PI-5), so it does not attempt the release ritual for v0.71.0.
//
// core/llm/capabilities/cache_sqlite_upgrade_test.go materialises
// core/storage/sqlite/testdata/upgrade/v0.70.0/dump.sql through
// upgradesnap.Materialize + storagesqlite.Open (the same helpers
// core/storage/sqlite/upgrade_path_test.go uses, not a hand-rolled
// second materialiser) and asserts, via a query against the SAME
// on-disk file made through a SEPARATE connection than the one
// SQLiteCache used to Put, that a capability record actually lands in
// provider_capabilities. RUN, not predicted:
//
//	$ go test ./core/llm/capabilities/... -run TestSQLiteCache -race -count=1 -v
//	=== RUN   TestSQLiteCache_WritesToRealProviderCapabilitiesTable
//	--- PASS: TestSQLiteCache_WritesToRealProviderCapabilitiesTable
//	=== RUN   TestSQLiteCache_GetRoundTripsThroughRealSQLite
//	--- PASS: TestSQLiteCache_GetRoundTripsThroughRealSQLite
//	=== RUN   TestSQLiteCache_InvalidateOnRealSQLite
//	--- PASS: TestSQLiteCache_InvalidateOnRealSQLite
//	PASS
//
// TestSQLiteCache_GetRoundTripsThroughRealSQLite specifically closes
// the storage.DB and reopens a SECOND one against the same on-disk
// file before calling Get — a record that only survived in an
// in-driver statement cache (not on disk) would fail that assertion,
// which is the same class of check AC-PI-1 exists for applied to a
// cache rather than a session store.
//
// Falsifiability, RUN not predicted: reverted the "case \"sqlite\":"
// arm in core/llm/capabilities/cache.go's DefaultCache back to falling
// through to MemoryCache (the pre-WP14 shape). The registry-level
// pipeline tests (below) still passed — because they construct
// SQLiteCache directly via Options.Cache, which is the point: the
// mutation that matters for the "was SQLite actually wired" question
// is at the DefaultCache selection, and TestSQLiteCache_
// WritesToRealProviderCapabilitiesTable does not go through
// DefaultCache at all (it calls NewSQLiteCache directly, by design —
// AC-017(a)'s stated failure mode is exactly "DefaultCache() returns a
// MemoryCache for 'sqlite' today", so the test that catches THAT
// mutation must exercise DefaultCache itself). Re-ran with
// HARNESS_LLM_CAPABILITY_CACHE=sqlite and a nil db (the "no DB handle
// supplied" degrade path) — confirmed DefaultCache(nil) returns a
// *MemoryCache (via a short throwaway assertion, not committed) and
// logs llm.capability_cache.sqlite_unavailable, matching the documented
// degrade-not-panic contract. Reverted after confirming both directions.
//
// # AC-PI-2 — this landing's own fixtures, audited for the SQL/file bypass
//
// Changed to drive real sqlite:
//   - core/llm/capabilities/cache_sqlite_upgrade_test.go (new) — every
//     test in this file materialises the v0.70.0 snapshot and opens a
//     real storagesqlite.DB; none uses an in-memory stand-in.
//
// Examined and deliberately NOT changed, with reason:
//   - core/llm/capabilities/cache_test.go (pre-existing,
//     TestMemoryCache_*, TestNullCache_*, TestRefresher_MaybeRefresh) —
//     these assert MemoryCache/NullCache/Refresher behaviour
//     specifically, which is legitimately in-process-only by design
//     (MemoryCache's own doc: "no persistence across restarts"; the
//     harness falls back to it when no DB handle is available). Testing
//     them against sqlite would test the wrong subject.
//   - core/llm/capabilities/gate_capability_hints_test.go (new) — pure
//     in-memory Gate/CapabilityDescriptor/AttachmentDescriptor
//     assertions. CapabilityHints is a ProviderProfile field held in
//     memory for the duration of one Stream call; it is not itself
//     persisted by this mission (WP10's session-knobs work is a
//     DIFFERENT persisted field, unrelated to this one). Nothing here
//     is supposed to survive a storage round trip.
//   - core/llm/registry/wp14_capability_probe_test.go (new) — drives
//     Registry.Stream with an in-memory MemoryCache (via
//     Options.Cache) deliberately, because these tests are about the
//     REGISTRY-LEVEL WIRING (does Stream call Get/MaybeRefresh/Put in
//     the right order, does Evict call Invalidate) rather than about
//     SQLite's durability, which cache_sqlite_upgrade_test.go already
//     covers directly. Using SQLiteCache here would not change what
//     these tests can see (the CapabilityCache interface is identical
//     either way) and would make them slower for no coverage gain —
//     this is the same reasoning CLAUDE.md's blind spot #2 exempts for
//     "legitimately in-memory behaviour," documented per its own rule
//     ("add a one-line comment saying so").
//   - core/llm/ollama/adapter_test.go (new) — httptest-server-backed
//     HTTP/SSE round trips and a direct call to ProviderCapabilities;
//     no storage layer in scope at all (the adapter itself never
//     touches a database — only Registry's cache wiring does).
//
// # AC-PI-3 — destructive migrations
//
// None. This landing adds no migration at all (D-5 survives — spec
// §11's correction: migration 0329 shipped in v0.63.0).
// check-destructive-migration-coverage.sh is not engaged.
//
// # AC-PI-5 — release-ritual hook
//
// This landing is UNIT-9 of a 17-unit mission with UNIT-11 (WP12, the
// gates) and the mission-closing report still open — it is not "the
// last mission to land before a release tag." The v0.70.0-vs-v0.71.0
// snapshot gap recorded under AC-PI-1 above is real and is flagged
// here (as the earlier landing flagged its own version of the same
// gap) for whoever cuts the v0.71.x-or-later release: `bash
// scripts/ci/upgrade-snapshot.sh v0.71.0` plus a hand-written
// PROVENANCE.md, per CLAUDE.md's release-ritual corollary.
