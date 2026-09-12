package llm_test

// WP-PI — persistence integrity for model-settings-reach-the-model-01PMZ101,
// scoped to THIS landing: UNIT-6 (WP10 — sessions.knobs_default gets its
// first production writer + reader, merged onto every
// GenerationRequest.Knobs the session issues; WP11 — /effort and
// SessionTunePanel stop reporting success on an unpersisted change, plus
// the TestProviderKey doc disposition).
//
// core/llm/wp_pi_persistence_integrity_z101_test.go (an earlier landing)
// and core/llm/wp_pi_persistence_integrity_z101_unit9_test.go (UNIT-9)
// both explicitly left UNIT-6 in their "not landed" lists — this file is
// the promised follow-up, not a duplicate.
//
// Per kitty-specs/_templates/WP-persistence-integrity.md (mandatory in
// every mission) and this mission's tasks.md UNIT-PI ("P0 · gate ...
// merge gate. Lands with or before the last of UNIT-4 / UNIT-6 / UNIT-9.
// The release PR cannot open without it."). This landing is UNIT-6
// itself; UNIT-PI's own WP12-gates half and the mission-closing report
// are separate WPs this file does not claim to be.
//
// # AC-PI-4 — per-WP enumeration
//
//   - WP10 (feat(sessions): store knobs_default and merge it into
//     GenerationRequest.Knobs) — PERSISTENCE-BEARING, adds NO new
//     migration. Migration sessions/0330-knobs (adding
//     sessions.knobs_default and session_messages.knobs_override)
//     shipped long before this landing and is already registered
//     (core/session/migrations.go) and present in every committed
//     snapshot through v0.78.1. This WP is the FIRST production code
//     that reads or writes sessions.knobs_default — the column has sat
//     empty (NULL) on every install since it was added. Everything
//     else this WP touches is in-memory: the KnobsDefaultResolver
//     interface + WithKnobsDefault option on LLMProviderAdapter, the
//     Generate() merge onto GenerationRequest.Knobs (an in-flight
//     struct, never itself persisted), and the four
//     core/llm/openaiwire/knob_coverage.go RegisterDeferred blocker-text
//     corrections (comment-only). session_messages.knobs_override is
//     DELIBERATELY left unwired this landing — see
//     docs/unwired-ledger.md's dated 2026-09-12 entry and
//     core/session/migrations_knobs.go's doc comment for the named
//     blocker and owner; it is not silently dropped.
//   - WP11 (fix(sessions): /effort and SessionTunePanel report success
//     only when the setting was stored) — NO persistence surface at
//     all. cmd_effort.go's fix changes what shape a Result.Metadata map
//     marshals to (an in-flight value returned over the wire, never
//     written to disk); SessionTunePanel.vue's fix makes the frontend
//     AWAIT the WP10 persist call rather than adding a new one; the
//     TestProviderKey doc correction touches only a comment.
//   - WP-PI (this file) — none; adds only this enumeration and the
//     falsifiability re-run below.
//
// # AC-PI-1 — tests boot from a previous-release database
//
// Verified 2026-09-12: `bash scripts/ci/check-upgrade-snapshot-present.sh`
// RUN, exit 0 — "snapshot chain reaches v0.78.1 (newest release tag
// v0.78.1)". The committed snapshot chain is current; no gap to flag for
// this landing (contrast the two earlier z101 WP-PI files, which both
// found and flagged a real gap at the time).
//
// core/storage/sqlite/upgrade_knobs_default_test.go materialises
// core/storage/sqlite/testdata/upgrade/v0.78.1/dump.sql through
// upgradesnap.Materialize + storagesqlite.Open (the SAME helpers
// core/storage/sqlite/upgrade_path_test.go and the earlier z101 WP-PI
// files' tests use — no hand-rolled second materialiser) and, against
// "seed-session-1" (baked into the snapshot with knobs_default = NULL —
// a session that existed before this feature and never opened the tune
// panel), asserts:
//
//  1. GetKnobsDefault on the untouched seed row returns nil, not an
//     error and not a zero-value struct that would look like a real
//     (if empty) override.
//  2. SetKnobsDefault + GetKnobsDefault round-trips a
//     {Reasoning: {OpenAIEffort: "high"}} value through the REAL
//     upgraded schema.
//  3. SetKnobsDefault(nil) clears the override back to nil — matching
//     SessionTunePanel's "Reset" contract — proving the NULL-write path
//     works against an upgraded column too, not only a freshly-created
//     one.
//
// RUN, not predicted:
//
//	$ go test ./core/storage/sqlite/... -run TestKnobsDefault_RoundTrips_AcrossUpgrade -race -count=1 -v
//	=== RUN   TestKnobsDefault_RoundTrips_AcrossUpgrade
//	--- PASS: TestKnobsDefault_RoundTrips_AcrossUpgrade
//	PASS
//
// # AC-PI-1 (send-path half) — the store round trip is not consumption
//
// A real-sqlite round trip proves the column works; it does not prove
// the stored value reaches a live GenerationRequest (CLAUDE.md sweep
// pass 4: "a read that only copies the value into another struct is not
// consumption"). core/rpc/views/agentgraph/chat/knobs_default_test.go
// covers that half with a pure in-memory fake (capturingRegistry +
// fakeKnobsDefaultResolver) — no SQL in scope here by design (spec §8
// rule 3: a seam-boundary unit test with a fake resolver has no SQL to
// bypass; the resolver's OWN persistence is what the sqlite test above
// covers).
//
// Falsifiability, RUN not predicted (mutation on the PRODUCTION code,
// not the test): guarded the Generate() merge in
// core/rpc/views/agentgraph/chat/llm_provider_adapter.go with `if false
// && a.knobsDefault != nil {`, leaving the store methods and the
// resolver wiring completely untouched:
//
//	$ go test ./core/rpc/views/agentgraph/chat/... -run TestGenerate_MergesSessionKnobsDefault -v
//	--- FAIL: TestGenerate_MergesSessionKnobsDefault
//	    knobs_default_test.go:58: GenerationRequest.Knobs is nil; want the
//	    session default merged onto the wire request — this is the WP10
//	    defect: the column round-trips through the store but nothing
//	    downstream reads it
//
// Reverted immediately; re-ran the same command — PASS. This is exactly
// the mutation AC-009's own text calls for ("drop the send-path merge,
// keep the store methods. AC-009 must go red — this is the mutation that
// separates 'stored' from 'reached the model'").
//
// A second mutation on cmd_effort.go (marshal the Go struct directly
// into Metadata again, undoing reasoningKnobMetadata) and a third on
// SessionTunePanel.vue (fire-and-forget the persist call instead of
// awaiting it) were also run and reverted for WP11's own tests — see
// those tests' doc comments for the observed failure text; not repeated
// here since neither touches a persistence surface (AC-PI-4 above).
//
// # AC-PI-2 — this landing's own fixtures, audited for the SQL/file bypass
//
// Changed to drive real sqlite:
//   - core/storage/sqlite/upgrade_knobs_default_test.go (new) — every
//     test in this file materialises the v0.78.1 snapshot and opens a
//     real storagesqlite.DB via session.NewSQLStore(session.NewStorageDB(db));
//     no in-memory stand-in anywhere in the file.
//
// Examined and deliberately NOT changed, with reason:
//   - core/rpc/views/sessions/impl_last_usage_read_test.go /
//     upgrade_last_usage_read_test.go (pre-existing, UNIT-4/WP06's own
//     WP-PI territory) — these assert session.LastUsage persistence, a
//     DIFFERENT column (last_usage_json) this landing does not touch.
//     Confirmed by reading: neither file references knobs_default,
//     KnobsDefault, or RequestKnobs.
//   - core/rpc/views/agentgraph/chat/knobs_default_test.go (new) —
//     deliberately in-memory (capturingRegistry fakes corellm.Registry;
//     fakeKnobsDefaultResolver fakes the persistence layer). This is
//     the send-path/seam-boundary half of AC-009, not the persistence
//     half — testing the adapter against real sqlite would mean
//     constructing a full *session.Manager just to exercise a merge
//     the fake resolver already exercises identically, for zero
//     additional coverage (same reasoning the UNIT-9 WP-PI file used to
//     exempt its own registry-level wiring tests from SQLiteCache).
//   - core/slashcmd/cmd_effort_metadata_test.go (new) — no storage
//     layer in scope at all; asserts on an in-memory Result.Metadata
//     map's JSON marshalling.
//   - frontend/src/views/sessions/__tests__/SessionTunePanel.test.ts
//     (new) and the two pre-existing frontend fixtures extended to
//     satisfy the widened SessionsClient interface
//     (AutonomyChip.test.ts, useSession.test.ts) — frontend unit tests
//     against a fake harnessClient; there is no SQL layer to bypass on
//     this side of the RPC boundary at all.
//
// # AC-PI-3 — destructive migrations
//
// None. This landing adds no migration (migration sessions/0330-knobs
// shipped and was registered long before this landing).
// check-destructive-migration-coverage.sh is not engaged.
//
// # AC-PI-5 — release-ritual hook
//
// This landing is UNIT-6 of a multi-unit mission with UNIT-11 (WP12,
// the gates) and the mission-closing report still open — it is not "the
// last mission to land before a release tag." No snapshot gap exists to
// flag (see AC-PI-1 above): the chain already reaches v0.78.1, the
// newest tag as of this landing.
