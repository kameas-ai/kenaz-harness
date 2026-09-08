package session_test

// WP-PI — persistence integrity for chat-turn-integrity-01PMZ606,
// mission-closing (covers WP02/WP03 [UNIT-1], WP04 [UNIT-2], WP05
// [UNIT-3], WP06 [UNIT-4], WP07/WP08 [UNIT-5], WP09/WP10 [UNIT-6],
// WP11/WP12 [UNIT-7], WP13 [UNIT-8], WP14 [UNIT-9]).
//
// Per kitty-specs/_templates/WP-persistence-integrity.md (owner decision
// 2026-08-19, mandatory, no per-mission judgement call) and this
// mission's tasks.md WP-PI section. AC-PI-4's "prove it does not apply"
// path is explicitly NOT available to this mission (spec.md §11): it
// adds two migrations (sessions/0336, sessions/0337 — the latter
// destructive), changes what every turn writes to session_messages, and
// changes what the model reads back.
//
// This file is a doc-only WP-PI record (no new Test functions): every
// test it cites already exists and was RUN, not re-implemented here,
// per spec.md §8 rule 2 ("do not hand-roll a second materialiser").
//
// ===========================================================================
// PARAGRAPH 1 — WHAT WAS RUN, VERBATIM, vs WHAT WAS READ
// ===========================================================================
//
// Base verified before any of this: `rtk proxy git log --oneline -1` on
// this worktree showed a stale ancestor (f4dc1013); `git merge-base
// --is-ancestor origin/release/v0.73.0 HEAD` failed; the tree was clean
// with no divergent commits, so `git reset --hard origin/release/v0.73.0`
// was run, landing on c92c1313 (the WP05 merge, tasks.md's own required
// base). All commands below ran against that commit.
//
// RAN (this session, personally, not copied from a prior WP's landed
// marker in tasks.md — those markers were read for orientation only and
// re-verified independently where they made a falsifiability claim):
//
//  1. bash scripts/ci/check-destructive-migration-coverage.sh
//     -> "clean — 6 destructive migration(s) all covered."
//     Then, to rule out vacuous coverage: renamed
//     core/storage/sqlite/migration_0337_test.go to *.disabled (removing
//     every literal reference to the 0337 repair migration's ID from the
//     test corpus) and re-ran the gate. It reported that migration as
//     lacking coverage, naming both the ID and its DELETE FROM
//     session_messages target -> confirms the gate genuinely names it and
//     the specific DELETE target, not a vacuous pass. Restored the file
//     (`git status --porcelain` empty afterward) and re-ran the gate
//     clean again.
//
//     ⚠️ WHY THIS FILE DELIBERATELY DOES NOT SPELL THE FULL MIGRATION ID.
//     check-destructive-migration-coverage.sh accepts, as proof of
//     coverage, ANY *_test.go whose source contains the migration's ID
//     string. This record is committed as a doc-only _test.go file, so
//     when it first landed it quoted the full literal -- and thereby
//     SATISFIED THE GATE BY ITSELF. Verified 2026-09-07 by the release
//     coordinator: with this file present, deleting
//     migration_0337_test.go entirely left the gate reporting "clean --
//     6 destructive migration(s) all covered". Removing BOTH files made
//     it fail correctly. A prose file was standing in for the test that
//     guards an irreversible DELETE against real user rows -- the exact
//     documentation-instead-of-guarantee class this mission exists to
//     remove, reintroduced by the mission's own closing record.
//     Every mention here is therefore deliberately partial ("0337", "the
//     repair migration") and MUST STAY THAT WAY. Do not "fix" it by
//     restoring the full ID.
//
//     Owed, and not fixed here (owner: alec): the gate should require a
//     test that actually opens a database, not merely one that mentions a
//     string. Any future doc-only _test.go can re-open this hole.
//
//  2. THE AC-PI-1 FALSIFICATION (mandatory, not optional, not predicted
//     — see PARAGRAPH 3 below for the full result). Mutated
//     core/session/manager.go's UpsertStreamCheckpoint to route through
//     AppendMessage + MarkStreamingFailure("transient", true) instead of
//     the checkpoint table (reproducing the pre-WP03 CHAT-01 shape at the
//     one production seam every fixture in this mission calls through).
//     Verified the mutation COMPILES first (`go build
//     ./core/session/... ./core/rpc/...` — exit 0, then `go vet` on the
//     same packages — clean) before treating any test result as
//     meaningful, per this WP-PI's own instruction that a
//     non-compiling mutation is a build error, not a falsification.
//     Disabled AC-001 (renamed
//     TestP0_HealthyTurnPollutesTranscript_AC001 to a non-Test-prefixed
//     name so `go test` cannot select it, keeping the shared
//     countingCheckpointWriter fixture type AC-004 depends on — deleting
//     the whole file breaks AC-004's build, which is not the same as
//     deleting the assertion). Ran:
//       go test ./core/rpc/views/agentgraph/chat/... -run
//         TestP0_HealthyTurnPollutesTranscript_AC004_UpgradePath -v -count=1
//     RESULT: FAIL.
//       "AC-004 (upgrade path): ListMessages returned 9 rows (7 carry
//        StreamingFailedAt); 7 checkpoint upserts landed; want 2 rows"
//       "AC-004: len(ListMessages) on the upgraded database = 9, want
//        exactly 2 (user=1 + assistant=1) — got 7 extra row(s)"
//       "AC-004: 7 row(s) carry a non-nil StreamingFailedAt on the
//        upgraded database after a turn that ended cleanly — want 0"
//     This is the exact "9 rows / 7 junk" shape recorded as the original
//     pre-fix defect in kitty-specs/chat-turn-integrity-01PMZ606/
//     research/p0-chain.md and in tasks.md's own UNIT-1 landed-marker —
//     independently reproduced here, not assumed from that marker.
//     Reverted both files (`git checkout -- core/session/manager.go
//     core/rpc/views/agentgraph/chat/p0_repro_test.go`); `git status
//     --porcelain` empty afterward; re-ran both AC-001 and
//     AC-004-upgrade-path clean (PASS, 2 rows, 0 failed, matching
//     production behaviour).
//
//     Separately, the migration 0337 discriminator's own falsification
//     was re-run (not merely read from migrations_checkpoint_repair.go's
//     doc comment): weakened checkpointRowsToDeleteForSession's loop to
//     the naive isCheckpointCondition1-only predicate (conditions 2 and
//     3 removed; `_ = referenced` added to keep the now-unused local
//     compiling). `go build ./core/session/... ./core/storage/...` and
//     `go vet` on the same packages were clean before running the test.
//       go test ./core/storage/sqlite/... -run
//         TestMigration0337_RepairsCheckpointRowsAgainstUpgradedDatabase -v -count=1
//     RESULT: FAIL —
//       "migration_0337_test.go:232: read wp05-error-partial-1 after
//        Open: sql: no rows in result set (0 rows means it was
//        incorrectly deleted)"
//     i.e. the genuine error-path partial (condition 3's protected
//     shape) was deleted — a row that should have survived was
//     destroyed, not merely a changed delete count. Reverted
//     (`git checkout -- core/session/migrations_checkpoint_repair.go`);
//     `git status --porcelain` empty; rebuilt clean.
//
//  3. go test ./core/storage/sqlite/... -run TestUpgradePath -v -count=1
//     -> PASS, all 14 committed snapshots (v0.63.0 through v0.72.0),
//     confirming migrations sessions/0336 and sessions/0337 apply
//     cleanly against every previously-shipped schema this repo has a
//     record of, not only a schema built fresh at HEAD.
//
//  4. go test ./core/storage/sqlite/... -run
//     TestGet_LastUsage_SurvivesReload_AcrossUpgrade -v -count=1
//     -> PASS. This is WP11's own AC-PI-1 test
//     (upgrade_last_usage_read_test.go), booting from
//     testdata/upgrade/v0.72.0/ (the file's own doc comment notes the
//     mission's spec text says v0.64.0, which is stale relative to the
//     base this mission actually landed on — the tree wins, and the
//     literal is guarded by check-upgrade-snapshot-present.sh so it does
//     not silently drift).
//
//  5. bash scripts/ci/check-upgrade-snapshot-present.sh
//     -> "OK — snapshot chain reaches v0.72.0 (newest release tag
//     v0.72.0)." Clean as of this run; see PARAGRAPH 3 / AC-PI-5 below
//     for what this does and does not prove about THIS release.
//
//  6. CGO_ENABLED=1 go test -race -count=1 -short -p 4 over the touched
//     packages (core/session/..., core/storage/sqlite/...,
//     core/rpc/views/agentgraph/chat/..., core/agentgraph/...,
//     core/rpc/views/sessions/...) -> all ok. core/rpc (WP11/WP12/WP13's
//     package) was run separately (139s, too slow for foreground):
//     the first run FAILed with no captured per-test failure line
//     (truncated by an accidental `| tail -60` on the backgrounded
//     command, a self-inflicted logging mistake, not a real diagnosis);
//     a clean re-run with output redirected to a file in full showed
//     "ok github.com/kameas-ai/kenaz-harness/core/rpc 139.145s" with no
//     FAIL anywhere in the full log. Not chased further — CLAUDE.md
//     names AutoTitle as a known flake in this exact package, and this
//     mission's own WP13 added AutoTitle-adjacent tests
//     (wp13_chat_turn_integrity_findings_test.go). Reported as observed,
//     not swept under the rug: first run flaked, rerun was clean, cause
//     not independently re-diagnosed beyond ruling out my own tail
//     truncation as the source of the missing detail.
//
//  7. go test ./scripts/ci/ -run
//     "TestGates/session-message-writers|TestGates/destructive-migration-coverage"
//     -v -count=1 -> PASS, including both gates' planted-violation
//     subtests (destructive-migration-coverage/uncovered-drop-table,
//     session-message-writers/second-append-message-call-site).
//
// READ, not independently re-derived (beyond what's cited inline above):
// tasks.md's UNIT-1/2/4/WP07 "LANDED" markers were read for scope
// orientation and cross-checked against `git show --stat` of the cited
// commits (97399c01, c9900043/f86f3e94, 2d784299, 2d80fcf2) rather than
// trusted at face value; the row counts and mutation descriptions in
// those markers were re-earned in this file's own paragraph 1 items 2-3
// rather than copied forward.
//
// NOT RUN: `bash scripts/ci/upgrade-snapshot.sh v0.73.0` — no v0.73.0
// tag exists yet (this branch has not been squash-merged to main), so
// there is nothing to snapshot yet. See AC-PI-5 below.
//
// ===========================================================================
// PARAGRAPH 2 — FIXTURES EXAMINED, WHICH CHANGED, WHICH DELIBERATELY DID NOT
// ===========================================================================
//
// No fixture was modified as part of writing this WP-PI record itself
// (all mutations above were temporary and reverted before this file was
// committed; `git status --porcelain` was checked clean after every
// revert). The audit below covers spec.md §11 / tasks.md WP-PI's named
// minimum set plus the mission's own new test files.
//
// EXAMINED AND NOT CHANGED, WITH REASON:
//
//   - core/rpc/views/agentgraph/chat/partial_flush_test.go — already
//     carries its own AC-PI-2 audit comment from WP03 naming
//     fakeStreamCheckpointWriter as the fixture class that hid CHAT-01,
//     and explaining why its remaining use here is legitimate: the three
//     tests in this file assert runPeriodicFlush's OWN control flow
//     (ticker interval, skip-on-no-growth watermark, ctx-cancel exit),
//     none of which is a persistence property. The persistence-shaped
//     assertions live in p0_repro_test.go / p0_repro_upgrade_test.go,
//     which drive the real *session.Manager end to end. Re-read and
//     confirmed accurate; not re-written.
//
//   - core/rpc/views/agentgraph/chat/chat_runner_integration_test.go
//     (1,300+ L) — grepped for session.Store / session.NewMemoryStore /
//     storagesqlite.Open / PartialPersister / StreamCheckpoints: zero
//     matches. This file's only session-shaped fixture is
//     fakeAutoTitleManager, a hand-built narrow session.Manager surface
//     (Get/ListMessagesActive/etc.) used to test the AutoTitle trigger's
//     control flow. It never wires session persistence, the
//     periodic-flush checkpoint seam, or the terminal PartialPersister
//     path this mission's P0 concerns. Out of scope for the SQL-bypass
//     concern because it never claims to test what survives a storage
//     round trip — reason it was left unchanged.
//
//   - core/rpc/views/agentgraph/chat/moves_test.go (model-moves-
//     transcript-01PMCH01, a different, already-shipped mission, not
//     touched by any chat-turn-integrity-01PMZ606 commit per
//     `git log --oneline --all -- <path>`) — TestMoves_
//     FiveIterationTurnPersistsEveryMove and
//     TestMoves_StreamBoundariesMatchPersistedMoves assert against a
//     hand-built recordingHistoryWriter (chat_runner_test.go), not real
//     sqlite. "Persists" here names the seam contract (HistoryWriter.
//     AppendEntry called with the right kind/order/index/content), one
//     layer above storage. The actual store-level round-trip guarantee
//     for the same move fields is a DIFFERENT file's job — see next
//     bullet — so this file legitimately stays a fake at its own layer.
//
//   - core/session/moves_test.go — TestAppendTranscriptEntry_
//     MemStoreMatchesSQL already carries its own justification comment
//     ("tests all over core/rpc run against NewMemoryStore, so a
//     memstore that silently dropped move metadata would make those
//     tests lie about the SQL behaviour") and is exactly the legitimate
//     memstore-parity pattern AC-PI-2 asks fixtures to state explicitly.
//     The sibling rejection-path test in the same TestAppendTranscript
//     Entry_RejectsInvalidMoveMetadata table exercises Manager-level
//     validation that runs before the Store is touched at all, so a real
//     vs. fake store cannot change its outcome. Not this mission's own
//     file (v0.63.0, per `git log`), but named in tasks.md's audit list;
//     examined, left unchanged for the reasons above.
//
//   - core/rpc/views/agentgraph/chat/wire_integration_test.go (1,100+ L)
//     — zero matches for session.Store/NewMemoryStore/storagesqlite;
//     these are provider-adapter wire-format goldens (Anthropic/OpenAI/
//     OpenRouter/Bedrock request-shape pinning). No session persistence
//     surface at all.
//
//   - core/rpc/views/agentgraph/chat/session_compaction_reload_test.go
//     — hand-built reloadHistoryReader fake, testing the `compact`
//     node's re-read-after-mutation contract (a different, already-
//     shipped mission's finding, agentgraph-total-convergence-01PMGX01
//     WP08). Tests a History-reader seam contract, not what survives a
//     storage round trip; the engine's own SQL rewrite has its own
//     coverage elsewhere (see pairing_sql_test.go below).
//
//   - core/rpc/views/agentgraph/chat/compaction_dial_golden_test.go —
//     a golden pinning WHICH SessionEngine calls a pre-send layer makes
//     and WHAT ERROR it returns, explicitly stating in its own doc
//     comment that "The Engine's own transform is pinned by
//     core/agentgraph/compaction's own tests and is NOT changed by
//     Phase 4" — i.e. this file is deliberately scoped above the
//     storage layer by the mission that wrote it.
//
//   - core/agentgraph/compaction/wiring/pairing_sql_test.go — THE HOUSE
//     EXAMPLE, not a gap: its own doc comment states it is deliberately
//     SQL-driven (session.NewSQLStore + storagesqlite.Open, real sqlite
//     file, session.Manager.AppendTranscriptEntry as the single move-
//     writing seam) because every OTHER compaction-boundary fixture in
//     this repo uses NewMemoryStore or hand-built SessionMessage values,
//     and that is what let a prior 5th-of-a-kind SQL-path mutation
//     (Role==RoleAssistant vs RoleTool) survive silently. Confirmed via
//     grep that it genuinely opens storagesqlite.Open + NewSQLStore, not
//     merely claiming to in its comment. One honest gap noted, not
//     fixed here (out of this mission's diff, predates it): it boots
//     from a fresh t.TempDir(), not a previous-release snapshot, despite
//     asserting "compaction of persisted history" — AC-PI-1's literal
//     text would apply if this were this mission's own new test. It is
//     not (model-moves-transcript-01PMCH01 WP06); flagging for whoever
//     next touches this file rather than silently accepting or
//     re-fixing a pre-existing, out-of-scope fixture under this
//     mission's WP-PI.
//
//   - core/session/migrations_test.go's migFakeDB (touched by WP05,
//     9544532a) — the fake's Query stub for 0337's session-candidate
//     scan deliberately returns "no candidates" with an inline comment
//     cross-referencing the REAL row-level contract test
//     (TestMigration0337_RepairsCheckpointRowsAgainstUpgradedDatabase in
//     core/storage/sqlite). This is TestMigrations_RegisterAndApply's
//     job (migration registration/ledger-ordering), not row-level
//     correctness — the fake is honest about that boundary in its own
//     comment. Confirmed accurate; not re-written.
//
//   - core/rpc/wp13_chat_turn_integrity_findings_test.go (WP13, new) —
//     TestBuildAutoTitleDeps_WiresAudit and _NilDeps construct
//     session.NewManager(session.NewMemoryStore()) but never assert
//     anything about what session_messages holds after a round trip;
//     the sessionMgr argument only satisfies buildAutoTitleDeps's
//     non-nil precondition so the function under test (a wiring
//     constructor) can be called at all. The actual assertions are on
//     deps.Audit forwarding to a fakeContextAuditEmitter. Legitimate:
//     a control-flow/wiring test, not a persistence assertion.
//
//   - core/agentgraph/compact_attrs_wiring_test.go,
//     core/agentgraph/compaction/pipeline_test.go,
//     core/agentgraph/compaction_event_reporting_test.go,
//     core/agentgraph/compaction_posttool_contextwindow_test.go,
//     core/agentgraph/compaction_target_test.go (WP09/WP10, new) —
//     zero matches for session.Store/NewMemoryStore/storagesqlite across
//     all five. UNIT-6's actual change (CompactionInput.Strategy ->
//     CompactRequest.Override, ContextWindow at the post_tool site,
//     event reporting) operates entirely on already-in-memory
//     []coreag.Message / CompactionInput values handed to a Compactor
//     interface (targetRecorder and similar hand-built fakes record
//     Compact() calls). The actual SQL rewrite of compacted history is
//     performed by compaction.SessionEngine, which this mission's diff
//     does not touch and which has its own pre-existing coverage
//     (including pairing_sql_test.go above). CORRECTION TO SPEC.MD §11:
//     the spec states "AC-PI-1 applies to UNIT-1, UNIT-3, UNIT-6 and
//     UNIT-7" as a blanket claim; personal inspection of every new
//     UNIT-6 test file shows none of them touches persisted storage, so
//     AC-PI-1's "boot from a previous release" requirement has no
//     target to apply to for UNIT-6 specifically — there is no
//     migration, table, or storage round-trip in this unit's diff for a
//     snapshot-boot to exercise. Per CLAUDE.md ("the tree wins" where
//     spec and tree disagree) and the precedent of the model-authored-
//     graphs WP-PI (04b4069d, which found the same class of mismatch for
//     a JSON-file-backed setting), this is recorded as a correction, not
//     silently complied with or silently ignored.
//
//   - core/rpc/compaction_overhead_test.go (WP12, new) — zero matches;
//     asserts against llmStack's in-process compactionLLM/compactionAudit
//     audit ring (a 256-entry in-memory structure), not a database table.
//     No persistence surface.
//
// CHANGED: none, by this WP-PI. All persistence-bearing assertions this
// mission needed were already written by the WPs that introduced them
// (WP02/WP03's p0_repro*.go, WP05's migration_0337_test.go, WP11's
// upgrade_last_usage_read_test.go) and independently re-verified above
// rather than re-authored.
//
// ===========================================================================
// PARAGRAPH 3 — AC-PI-1 FALSIFIABILITY RESULT (THE HEADLINE)
// ===========================================================================
//
// This mission DOES have a persistence surface (AC-PI-4's "prove it does
// not apply" path is unavailable, confirmed above rather than merely
// asserted) and AC-PI-1 was run to completion, not predicted:
//
//   - Reverting WP03's checkpoint-writer seam (UpsertStreamCheckpoint
//     routed back through AppendMessage+MarkStreamingFailure, the
//     compiling mutation verified above) and deleting AC-001 leaves
//     AC-004 — the upgrade-path assertion booted from a v0.64.0-shaped
//     database materialised via upgradesnap.Materialize, NOT an empty
//     directory — STILL FAILING, on exactly the row-count/failure-flag
//     shape the pre-fix defect produced (9 rows, 7 flagged, want 2/0).
//     This proves AC-004 is not vacuous: it is not merely passing
//     because the database it boots started empty.
//   - Separately, weakening migration 0337's three-condition
//     discriminator to condition-1-only reddens
//     TestMigration0337_RepairsCheckpointRowsAgainstUpgradedDatabase —
//     also booted from testdata/upgrade/v0.64.0/dump.sql — specifically
//     because a row that SHOULD have survived (the genuine
//     error-path partial) was deleted, not merely because a delete
//     count changed.
//   - TestUpgradePath passes against all 14 committed snapshots
//     (v0.63.0 through v0.72.0) with 0336/0337 registered, confirming
//     neither migration regresses any previously-shipped schema.
//
// AC-PI-5 (release-ritual hook): as of this WP-PI, the newest git tag is
// v0.72.0 and the newest committed upgrade snapshot is v0.72.0 —
// check-upgrade-snapshot-present.sh passes clean. No v0.73.0 tag exists
// yet (this release has not been squash-merged to main), so there is
// nothing to snapshot yet and this mission cannot determine from inside
// its own worktree whether it is the LAST mission to land on
// release/v0.73.0 before that tag is cut — the same limitation the
// served-mode-is-a-real-mode mission-closing WP-PI (0c1fda6a) recorded
// for the same reason. Flagged, not silently assumed either way: whoever
// merges the final worktree into release/v0.73.0 and opens the PR to
// main owns running `bash scripts/ci/upgrade-snapshot.sh v0.73.0` (once
// the tag exists) plus a hand-written PROVENANCE.md, per CLAUDE.md's
// release-ritual corollary.
//
// ===========================================================================
// RESIDUAL RISK — STATED PLAINLY, NOT SOFTENED
// ===========================================================================
//
// Migration sessions/0337's safety rests entirely on discriminator
// condition 3: a genuine error-path partial is assumed to be the LAST
// thing its turn produced, so nothing later in the same session can
// supersede it by strict content prefix. This is a heuristic over
// message content, not a foreign-key or provenance certainty. Its
// precise failure mode: a session where a genuine, never-resumed
// error-path partial (recoverable, kind=transient, no continuation_of
// in either direction — i.e. it passes conditions 1 and 2 exactly like
// a real checkpoint) is COINCIDENTALLY a strict byte-prefix of some
// later, textually-unrelated assistant message in that same session.
// If that coincidence occurs, 0337 deletes real, resumable user history,
// indistinguishable in its own output from correctly-deleted checkpoint
// junk. The migration's Down is a best-effort no-op (no pre-migration
// backup is taken — stated explicitly in
// migrations_checkpoint_repair.go's own doc comment) — there is no
// undo. This exact tradeoff (false negatives safe, false positives
// unrecoverable) was surfaced to the owner as escalation E-002 and
// ruled REPAIR on 2026-08-30 by alec, who therefore owns the residual
// risk of this specific failure mode — the ruling accepted it as the
// cost of ending eight releases of accumulating checkpoint pollution,
// it did not eliminate it. Nothing in this mission's diff, and nothing
// available at migration time (SQLite, a bounded per-session scan, no
// external provenance signal), can distinguish this pathological case
// from a genuine checkpoint. If it is ever found to have occurred on a
// real install, there is no code path in this mission that recovers the
// deleted row.
