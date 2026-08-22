package sqlite_test

// TestUpgradePath is the mission's headline test
// (upgrade-path-coverage-01PMUG01, spec.md FR-1 / §6.1-6.2).
//
// IT MUST CATCH THE v0.63.0 BUG WITHOUT KNOWING ABOUT IT. This test is
// table-driven over every directory under testdata/upgrade/ — adding a
// snapshot adds a case with no code change here. It carries no
// knowledge of migration 334, the units block, or move_history_mode;
// it only knows "here is a database a previous release produced; the
// app must be able to read AND WRITE it."
//
// FALSIFIABILITY (spec §6.1). Revert core/storage/migrations/registry.go
// Pending() to `if m.Version > maxApplied`, delete
// core/storage/migrations/pending_setmembership_test.go and
// core/storage/sqlite/repair_upgrade_test.go (the two tests written
// because someone already knew about the bug), and this test — the
// v0.63.0 case specifically — must still fail. It was performed; see
// the WP02 mission report for the pasted failure output.
//
// Every real table's row COUNT and per-PK CONTENT DIGEST is snapshotted
// before Open and compared after (spec FR-1 item 6), except tables a
// migration in that snapshot's tag range is declared to change — see
// expectedChangedTables below, one entry per reason, not a blanket
// exemption.

import (
	"context"
	"database/sql"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/session"
	"github.com/kameas-ai/kenaz-harness/core/storage"
	storagesqlite "github.com/kameas-ai/kenaz-harness/core/storage/sqlite"
	"github.com/kameas-ai/kenaz-harness/core/storage/sqlite/upgradesnap"
	coretasks "github.com/kameas-ai/kenaz-harness/core/tasks"
	corewf "github.com/kameas-ai/kenaz-harness/core/workflows"

	_ "modernc.org/sqlite"
)

// expectedChangedTables lists, per snapshot tag, the tables a migration
// registered ABOVE that snapshot's ledger is declared to alter — so
// their pre/post-Open digest is allowed to differ. Every entry names
// the migration responsible. A table not listed here that WOULD change
// shape (e.g. gains/loses a column) fails the test, which is exactly
// the point: an undeclared shape change is a regression.
// model-scheduled-jobs-01PMSJ01 WP09's sessions/0340 migration ADD
// COLUMNs created_by + tool_allowlist onto scheduled_chat_runs, which
// was created by migration 0325 (v0.10.0, long before any committed
// snapshot) and has not changed shape since. 0340 is newer than every
// migration reflected in every currently-committed snapshot, so EVERY
// tag below sees this table's shape change on Open — this is not a
// per-tag judgement call, it's the same fact repeated for each one.
// scheduledChatRunsProvenanceNote is the shared reason for the
// scheduled_chat_runs entry that appears under EVERY tag below.
//
// COST OF THAT ENTRY, stated here because it is stated for `tasks` and
// was not stated for this table (finding N6, re-review of PR #307):
// scheduled_chat_runs is now allowlisted on all twelve tags, so the
// comparison loop skips both its row-count and digest checks
// permanently. A REAL future migration that writes rows to this table
// will be masked. If one lands, assert the row count exactly rather
// than allowlisting the table, or move the provenance assertion onto a
// table no migration touches.
//
// The const is referenced from the per-tag comments rather than from
// code, which is why a compiler cannot see it go stale — blind spot #1
// in CLAUDE.md, inside the upgrade-path test of all places.
const scheduledChatRunsProvenanceNote = "sessions/0340-scheduled-chat-runs-created-by " +
	"(model-scheduled-jobs-01PMSJ01 WP09) adds created_by + tool_allowlist " +
	"to scheduled_chat_runs (created by 0325, unchanged since)."

var expectedChangedTables = map[string][]string{
	"v0.70.0": {
		// sessions/0340 (model-scheduled-jobs-01PMSJ01 WP09) ALTERs
		// scheduled_chat_runs, adding created_by NOT NULL DEFAULT 'user'
		// and tool_allowlist NOT NULL DEFAULT ''. Every pre-existing row
		// in the v0.70.0 snapshot gains both columns, so the content
		// digest legitimately changes.
		//
		// This surfaced only AFTER the merge: WP09's agent ran
		// TestUpgradePath against v0.63.0-v0.69.0 and was green, because
		// its worktree branched before the v0.70.0 snapshot existed. The
		// defect is real and belongs to the integration, not to that
		// agent — which is the class of failure a shared release branch
		// exists to catch.
		//
		// The provenance columns are the POINT of WP09: an upgraded
		// install must have created_by on rows written before the column
		// existed, defaulted to 'user' so a pre-existing schedule is
		// never mistaken for a model-created one. That default is
		// load-bearing, not cosmetic — GateScheduledChatExecute fails
		// closed only for created_by == "model".
		"scheduled_chat_runs",
		// NOT a migration writing rows — this one is the TEST's own
		// probe. tasks/1200-tasks-init (subagent-control-and-background-
		// tasks-01PMZB11 UNIT-2) creates the table empty, and then
		// assertTasksTableMigrated inserts through the production writer
		// coretasks.NewSQLiteStore(...) to prove the table is actually
		// usable and not merely present.
		//
		// For v0.63.0..v0.69.0 that insert is invisible here: `tasks`
		// does not exist in those dumps, so there is no before-state to
		// diff. v0.70.0 is the first snapshot containing the table, so
		// the probe's row shows up as 0 -> 1 and the test correctly
		// refuses it until declared.
		//
		// Declaring it is right, but note what it costs: a real
		// migration that writes to `tasks` in a future release will now
		// be masked for THIS tag. If one lands, split the probe onto a
		// table nobody migrates, or assert the row count exactly rather
		// than allowlisting the table.
		"tasks",
	},
	"v0.63.0": {
		// sessions/0333-transcript-moves adds nullable columns to
		// session_messages (kind, move_index, turn_span_id,
		// model_tool_args).
		"session_messages",
		// sessions/0334-move-fidelity-columns adds sessions.move_history_mode.
		"sessions",
		// See scheduledChatRunsProvenanceNote above.
		"scheduled_chat_runs",
	},
	"v0.63.1": {"scheduled_chat_runs"}, // see scheduledChatRunsProvenanceNote
	"v0.63.2": {"scheduled_chat_runs"}, // see scheduledChatRunsProvenanceNote
	"v0.64.0": {"scheduled_chat_runs"}, // see scheduledChatRunsProvenanceNote
	"v0.64.1": {"scheduled_chat_runs"}, // see scheduledChatRunsProvenanceNote
	"v0.65.0": {
		// event-log/0106-events-fts-sync (audit-that-tells-the-truth-
		// 01PMZA10 UNIT-8) UPDATEs the single retention_config row,
		// correcting event-log/0103's shipped seed
		// ('{"kind":"keep_all"}', not a valid RetentionStrategy value)
		// to '{"kind":"keep_forever"}'. A deliberate DATA change, not a
		// shape change — TestReadRetentionPolicy_AfterMigration106_ReadsTheFixedSeed
		// (core/event/log/retention_config_test.go) asserts the
		// corrected value is actually readable afterwards.
		"retention_config",
		// See scheduledChatRunsProvenanceNote above.
		"scheduled_chat_runs",
	},
	"v0.65.1": {
		// Same migration, same reason as v0.65.0 above: v0.65.1 is a
		// CI-only patch release (PR #300) whose dump is byte-identical
		// to v0.65.0's, so event-log/0106 corrects the same seeded row
		// when it boots under HEAD.
		//
		// This entry exists because the tag exists: `fix(ci):` is a
		// patch prefix, so a CI-hygiene PR minted a real release tag,
		// and check-upgrade-snapshot-present.sh then required a snapshot
		// for it. Every release tag owes one, including the ones nobody
		// set out to cut.
		"retention_config",
		// See scheduledChatRunsProvenanceNote above.
		"scheduled_chat_runs",
	},
	"v0.66.0": {"scheduled_chat_runs"}, // see scheduledChatRunsProvenanceNote
	"v0.67.0": {"scheduled_chat_runs"}, // see scheduledChatRunsProvenanceNote
	"v0.68.0": {"scheduled_chat_runs"}, // see scheduledChatRunsProvenanceNote
	"v0.69.0": {"scheduled_chat_runs"}, // see scheduledChatRunsProvenanceNote
}

// fixedProbeTime is used for the item-4 session INSERT probe so the
// test's own write doesn't introduce nondeterminism into anything that
// might hash session content later.
var fixedProbeTime = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func TestUpgradePath(t *testing.T) {
	root := "testdata/upgrade"
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("read %s: %v", root, err)
	}
	var tags []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(root, e.Name(), "dump.sql")); err != nil {
			continue // e.g. a tag directory recorded as unreplayable, no dump.sql
		}
		// Only vX.Y.Z directories are snapshots. scripts/ci/upgrade-
		// snapshot.sh also accepts the literal "HEAD" to preview an
		// unreleased tree (docs/upgrade-snapshots.md); that output is a
		// scratch artefact and must never silently become a chain entry
		// just because someone forgot to delete it.
		if !upgradesnap.IsSnapshotTag(e.Name()) {
			continue
		}
		tags = append(tags, e.Name())
	}
	sort.Strings(tags)
	if len(tags) == 0 {
		t.Fatal("no snapshot directories with a dump.sql found under testdata/upgrade — the chain is empty")
	}

	for _, tag := range tags {
		tag := tag
		t.Run(tag, func(t *testing.T) {
			t.Parallel()
			testUpgradeSnapshot(t, tag)
		})
	}
}

func testUpgradeSnapshot(t *testing.T, tag string) {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()

	dumpText, err := os.ReadFile(filepath.Join("testdata", "upgrade", tag, "dump.sql"))
	if err != nil {
		t.Fatalf("read dump.sql: %v", err)
	}

	// Materialise the historical snapshot into data.db, behind the
	// harness's back — this is "a database a previous release
	// produced," not a hand-assembled registry (spec §4).
	rawPath := filepath.Join(dir, "data.db")
	raw := openRawSQLiteAt(t, rawPath)
	if err := upgradesnap.Materialize(ctx, raw, string(dumpText)); err != nil {
		t.Fatalf("materialise snapshot: %v", err)
	}
	preOpen, err := upgradesnap.SnapshotAll(ctx, raw)
	if err != nil {
		t.Fatalf("pre-Open snapshot: %v", err)
	}
	// UpSource immutability (spec §4 / §6.3). The snapshot's frozen
	// content_hash values are what a PREVIOUS release wrote; they must
	// still equal HEAD's registered hashes. Nothing in production
	// enforces this — migrations.VerifyLedger, which returns
	// ErrLedgerHashMismatch, has no production caller — so the
	// snapshot chain is where the constraint becomes decidable. See
	// assertLedgerHashesUnchanged for the full reasoning.
	assertLedgerHashesUnchanged(t, ctx, raw, tag)
	if err := raw.Close(); err != nil {
		t.Fatalf("close raw after materialise: %v", err)
	}

	// ---- FR-1 item 1: Open succeeds. ----
	db, err := storagesqlite.Open(newConfig(dir))
	if err != nil {
		t.Fatalf("Open on the %s snapshot failed: %v\n\nThis is the mission's headline assertion. If you are seeing "+
			"this because Pending() was reverted to a max-based selection, that is the design working as intended.", tag, err)
	}
	t.Cleanup(func() { _ = db.Close(context.Background()) })

	// ---- item 2: nothing pending. ----
	pending, err := db.Migrations().Pending()
	if err != nil {
		t.Fatalf("Pending: %v", err)
	}
	if len(pending) != 0 {
		var ids []string
		for _, m := range pending {
			ids = append(ids, m.ID)
		}
		t.Fatalf("Pending() after Open = %v, want none", ids)
	}

	// ---- item 3: session list, through the production store. ----
	store := session.NewSQLStore(session.NewStorageDB(db))
	sessions, err := store.List(ctx)
	if err != nil {
		t.Fatalf("session list: %v", err)
	}
	if len(sessions) < 2 {
		t.Fatalf("session list returned %d sessions, want at least the 2 seeded ones", len(sessions))
	}
	wantNames := map[string]bool{"Seed Session One": false, "Seed Session Two (branch child)": false}
	for _, s := range sessions {
		if _, ok := wantNames[s.Name]; ok {
			wantNames[s.Name] = true
		}
	}
	for name, found := range wantNames {
		if !found {
			t.Errorf("session list missing seeded session %q", name)
		}
	}

	// ---- item 4: session INSERT, through the production store. This
	// is THE assertion that fails with "no such column:
	// move_history_mode" when the selection bug is present and
	// verifyFullyApplied is also reverted (spec §6.1's second
	// independent witness). ----
	if err := store.Create(ctx, session.Record{
		ID: "upgrade-path-" + tag, Name: "post-open probe", CreatedAt: fixedProbeTime, UpdatedAt: fixedProbeTime,
		LastActiveAt: fixedProbeTime, Position: 999, ContextKind: session.ContextKindSystem,
	}); err != nil {
		t.Fatalf("session INSERT after Open on the %s snapshot failed: %v", tag, err)
	}

	// ---- item 5: a representative read on every major surface. ----
	assertSurfaceReads(t, ctx, db)

	// ---- AC-001 (audit-that-tells-the-truth-01PMZA10 UNIT-2): the six
	// event-log migrations (versions 100-105) actually applied on THIS
	// upgraded install, not just on a fresh database. Every tag under
	// testdata/upgrade/ predates event-log's registration, so on every
	// one of them these six ledger rows are new — this is exactly the
	// "first migrations to land below an install's high-water mark"
	// case spec §1.3 calls out, and Pending()==0 above (item 2) already
	// proves Registry.Pending()'s set-membership selection picked them
	// up; this asserts the concrete evidence a reviewer would ask for
	// (the ledger rows exist AND the table they create is queryable). ----
	assertEventLogMigrated(t, ctx, db)

	// ---- AC-01/AC-02 (subagent-control-and-background-tasks-01PMZB11
	// UNIT-2): the tasks table exists and accepts a write on THIS
	// upgraded install, through the exact production wiring shape
	// (coretasks.NewSQLiteStore against the *sql.DB storage.Open hands
	// out) — not a hand-rolled fixture. Every tag under testdata/upgrade/
	// predates the tasks/1200-tasks-init migration's registration, so on
	// every one of them this is a "first migration to land below an
	// install's high-water mark" case, same shape as event-log above. ----
	assertTasksTableMigrated(t, ctx, db)

	// ---- automation-actually-runs-01PMZ404 UNIT-13 (owner ruling
	// A-10): rerun_policy is refused on save but tolerated on load. A
	// row this tag's own release could have written via the old,
	// lenient validator (rerun_policy: skip) must still Load and still
	// appear in List on THIS build, which narrows the same field to
	// save-only rejection — the read-compat hazard the mission's PI
	// table flags for yaml_source (X-11). ----
	assertRerunPolicyToleratedOnLoad(t, ctx, db, tag)

	// ---- item 6: no seeded row disappeared, except declared changes.
	// "sessions" always gained exactly the one row from the item-4
	// probe insert above, on every tag — that is expected regardless
	// of whether a migration also changed the table's shape, so it is
	// unconditionally in the changed set here (its ROW COUNT changing
	// by exactly 1 is asserted below; digest is not re-checked).
	postRaw := openRawSQLiteAt(t, rawPath)
	postOpen, err := upgradesnap.SnapshotAll(ctx, postRaw)
	if err != nil {
		t.Fatalf("post-Open snapshot: %v", err)
	}
	if err := postRaw.Close(); err != nil {
		t.Fatalf("close raw after post-Open snapshot: %v", err)
	}
	// "workflows" gained exactly two rows from
	// assertRerunPolicyToleratedOnLoad above (the direct-SQL legacy
	// probe row plus the one successful Save of a fresh, empty-policy
	// workflow — the rejected non-empty-policy Save writes nothing),
	// and "workflow_versions" gained one row (Save's version-history
	// append for that same successful save; the direct-SQL legacy
	// insert bypasses Store.Save entirely, so it does not touch
	// workflow_versions), unconditionally on every tag for the same
	// reason "sessions" is: this test itself is the writer, not a
	// migration.
	changed := map[string]bool{"harness_migrations": true, "sessions": true, "workflows": true, "workflow_versions": true}
	for _, tbl := range expectedChangedTables[tag] {
		changed[tbl] = true
	}
	for table, before := range preOpen {
		if changed[table] {
			continue
		}
		after, ok := postOpen[table]
		if !ok {
			t.Errorf("table %s present before Open, missing after", table)
			continue
		}
		if before.RowCount != after.RowCount {
			t.Errorf("table %s row count changed: %d -> %d (not in expectedChangedTables[%q])", table, before.RowCount, after.RowCount, tag)
		}
		if before.Digest != after.Digest {
			t.Errorf("table %s content digest changed (not in expectedChangedTables[%q]): a seeded row was altered", table, tag)
		}
	}
	if before, ok := preOpen["sessions"]; ok {
		after := postOpen["sessions"]
		if after.RowCount != before.RowCount+1 {
			t.Errorf("sessions row count = %d after Open+probe-insert, want exactly %d (before + the one item-4 probe row)",
				after.RowCount, before.RowCount+1)
		}
	}
	if before, ok := preOpen["workflows"]; ok {
		after := postOpen["workflows"]
		if after.RowCount != before.RowCount+2 {
			t.Errorf("workflows row count = %d after Open+UNIT-13 probes, want exactly %d (before + legacy-load row + fresh-valid-save row)",
				after.RowCount, before.RowCount+2)
		}
	}

	// ---- item 7 (secondary — spec §1.3 proves these stay clean
	// through a silent cascade, so they are necessary but not
	// sufficient; asserted anyway). ----
	assertPragmaClean(t, ctx, db)

	// ---- item 8: idempotence. A second Open on the same directory
	// changes no row and leaves Pending() empty. ----
	if err := db.Close(context.Background()); err != nil {
		t.Fatalf("close before second Open: %v", err)
	}
	db2, err := storagesqlite.Open(newConfig(dir))
	if err != nil {
		t.Fatalf("second Open: %v", err)
	}
	t.Cleanup(func() { _ = db2.Close(context.Background()) })
	pending2, err := db2.Migrations().Pending()
	if err != nil {
		t.Fatalf("Pending after second Open: %v", err)
	}
	if len(pending2) != 0 {
		t.Fatalf("Pending() after SECOND Open = %d entries, want 0 (idempotence)", len(pending2))
	}
	raw2 := openRawSQLiteAt(t, rawPath)
	secondSnap, err := upgradesnap.SnapshotAll(ctx, raw2)
	if err != nil {
		t.Fatalf("snapshot after second Open: %v", err)
	}
	if err := raw2.Close(); err != nil {
		t.Fatalf("close raw2: %v", err)
	}
	for table, first := range postOpen {
		second, ok := secondSnap[table]
		if !ok {
			t.Errorf("idempotence: table %s present after first Open, missing after second", table)
			continue
		}
		if first.RowCount != second.RowCount || first.Digest != second.Digest {
			t.Errorf("idempotence: table %s changed between the first and second Open (count %d->%d) — "+
				"a migration that mutates data on every apply, not just the first, is not idempotent "+
				"(see docs/unwired-ledger.md re: migration 0335)", table, first.RowCount, second.RowCount)
		}
	}
}

// assertSurfaceReads drives a representative read on every major
// surface the seed corpus (testdata/upgrade/seed.sql) covers, through
// the production storage.DB.Reader() surface (spec FR-1 item 5).
func assertSurfaceReads(t *testing.T, ctx context.Context, db storage.DB) {
	t.Helper()
	r := db.Reader()

	count := func(label, query string, args ...any) int {
		var n int
		if err := r.QueryRow(ctx, query, args...).Scan(&n); err != nil {
			t.Errorf("%s: %v", label, err)
			return 0
		}
		return n
	}

	if n := count("messages", "SELECT COUNT(*) FROM session_messages WHERE session_id='seed-session-1'"); n < 3 {
		t.Errorf("session_messages for seed-session-1 = %d, want >= 3", n)
	}
	if n := count("artifacts", "SELECT COUNT(*) FROM artifacts WHERE id='seed-artifact-1'"); n != 1 {
		t.Errorf("artifacts row for seed-artifact-1 = %d, want 1", n)
	}
	if n := count("artifact_versions", "SELECT COUNT(*) FROM artifact_versions WHERE artifact_id='seed-artifact-1'"); n != 2 {
		t.Errorf("artifact_versions for seed-artifact-1 = %d, want 2 (the cascade canary)", n)
	}
	if n := count("branches", "SELECT COUNT(*) FROM branches WHERE id='seed-branch-1'"); n != 1 {
		t.Errorf("branches row for seed-branch-1 = %d, want 1", n)
	}
	if n := count("workflows", "SELECT COUNT(*) FROM workflows WHERE id='seed-workflow-1'"); n != 1 {
		t.Errorf("workflows row for seed-workflow-1 = %d, want 1", n)
	}
	if n := count("workflow_versions", "SELECT COUNT(*) FROM workflow_versions WHERE workflow_id='seed-workflow-1'"); n != 1 {
		t.Errorf("workflow_versions for seed-workflow-1 = %d, want 1", n)
	}
	if n := count("workflow_runs_cache", "SELECT COUNT(*) FROM workflow_runs_cache WHERE workflow_id='seed-workflow-1'"); n != 1 {
		t.Errorf("workflow_runs_cache for seed-workflow-1 = %d, want 1", n)
	}
	if n := count("scheduled_chat_runs", "SELECT COUNT(*) FROM scheduled_chat_runs WHERE id='seed-schedrun-1'"); n != 1 {
		t.Errorf("scheduled_chat_runs row for seed-schedrun-1 = %d, want 1", n)
	}
	if n := count("scheduled_chat_run_history", "SELECT COUNT(*) FROM scheduled_chat_run_history WHERE chat_run_id='seed-schedrun-1'"); n != 1 {
		t.Errorf("scheduled_chat_run_history for seed-schedrun-1 = %d, want 1", n)
	}
	if n := count("context_attachments", "SELECT COUNT(*) FROM context_attachments WHERE id='seed-attach-1'"); n != 1 {
		t.Errorf("context_attachments row for seed-attach-1 = %d, want 1", n)
	}
	if n := count("slash_commands_user", "SELECT COUNT(*) FROM slash_commands_user WHERE name='seed-cmd'"); n != 1 {
		t.Errorf("slash_commands_user row for seed-cmd = %d, want 1", n)
	}
	if n := count("units", "SELECT COUNT(*) FROM units WHERE id IN ('seed-unit-1','seed-unit-2')"); n != 2 {
		t.Errorf("units rows for seed-unit-{1,2} = %d, want 2", n)
	}
	if n := count("unit_edges", "SELECT COUNT(*) FROM unit_edges WHERE id='seed-edge-1'"); n != 1 {
		t.Errorf("unit_edges row for seed-edge-1 = %d, want 1", n)
	}

	// FTS search: the seed message text is real prose (not a role='tool'
	// row, so migration 0335's purge must not have removed it).
	if n := count("fts search", `SELECT COUNT(*) FROM messages_fts WHERE messages_fts MATCH 'kameas'`); n < 1 {
		t.Errorf("FTS search for 'kameas' = %d hits, want >= 1 (seed-msg-1's content)", n)
	}

	// chat-turn-integrity-01PMZ606 WP02 (AC-004, partial): migration
	// sessions/0336 must have created stream_checkpoints on every
	// snapshot in the chain, including v0.64.0 (which predates it). The
	// table is empty here — nothing in this mission's WPs writes a row
	// during Open — so this asserts the table EXISTS and is queryable,
	// not any row content; the write path is covered by AC-001/AC-002
	// in core/rpc/views/agentgraph/chat.
	if n := count("stream_checkpoints", "SELECT COUNT(*) FROM stream_checkpoints"); n != 0 {
		t.Errorf("stream_checkpoints row count = %d, want 0 (migration 0336 creates an empty table; nothing writes to it during Open)", n)
	}
}

// assertEventLogMigrated is AC-001's concrete evidence: the six
// event-log/010x ledger rows exist with action='applied' after Open on
// an upgraded install, and the events table migration 0100 creates is
// actually queryable (not merely "registered" — see the distinction
// spec §1.3 draws between those two things).
func assertEventLogMigrated(t *testing.T, ctx context.Context, db storage.DB) {
	t.Helper()
	r := db.Reader()

	wantIDs := []string{
		"event-log/0100-events",
		"event-log/0101-event-chain-heads",
		"event-log/0102-redaction-rules",
		"event-log/0103-retention-config",
		"event-log/0104-schema-version",
		"event-log/0105-saved-audit-queries",
	}
	for _, id := range wantIDs {
		var n int
		err := r.QueryRow(ctx,
			"SELECT COUNT(*) FROM harness_migrations WHERE id = ? AND action = 'applied' AND owning_mission = 'event-log'",
			id).Scan(&n)
		if err != nil {
			t.Fatalf("query ledger for %s: %v", id, err)
		}
		if n != 1 {
			t.Errorf("harness_migrations row for %s (action=applied, owning_mission=event-log) = %d, want 1", id, n)
		}
	}

	// The table 0100 creates, and the schema_version column 0104 adds,
	// must both be queryable — proves the DDL actually ran, not just
	// that a ledger row got written for it.
	var n int
	if err := r.QueryRow(ctx, "SELECT COUNT(*) FROM events").Scan(&n); err != nil {
		t.Errorf("events table not queryable after Open: %v", err)
	}
	if err := r.QueryRow(ctx, "SELECT COUNT(*) FROM events WHERE schema_version >= 0").Scan(&n); err != nil {
		t.Errorf("events.schema_version column not queryable after Open (migration 0104 did not apply its ALTER TABLE): %v", err)
	}
	if err := r.QueryRow(ctx, "SELECT COUNT(*) FROM retention_config").Scan(&n); err != nil {
		t.Errorf("retention_config table not queryable after Open: %v", err)
	}
	if err := r.QueryRow(ctx, "SELECT COUNT(*) FROM saved_audit_queries").Scan(&n); err != nil {
		t.Errorf("saved_audit_queries table not queryable after Open: %v", err)
	}
}

// assertTasksTableMigrated is AC-01/AC-02's concrete evidence, run
// against every snapshot in the chain: the tasks table exists, is
// queryable, and accepts a write through the exact production writer
// (coretasks.NewSQLiteStore(rawDB), the same call core/rpc/api.go makes)
// — not a memory-store fixture or a hand-assembled insert (CLAUDE.md
// blind spot #2). A pre-migration snapshot has no `tasks` table at all,
// so this is the "first migration to land below an install's high-water
// mark" case: Pending()==0 above (item 2) already proved selection
// picked it up, and this asserts the table it creates is actually
// there and writable, not merely ledgered.
func assertTasksTableMigrated(t *testing.T, ctx context.Context, db storage.DB) {
	t.Helper()
	r := db.Reader()

	var n int
	if err := r.QueryRow(ctx, "SELECT COUNT(*) FROM tasks").Scan(&n); err != nil {
		t.Fatalf("tasks table not queryable after Open (tasks/1200-tasks-init did not apply): %v", err)
	}
	if n != 0 {
		t.Errorf("tasks row count on a fresh upgrade = %d, want 0 (no snapshot seeds a task row)", n)
	}

	type sqlHandle interface{ SQL() *sql.DB }
	h, ok := db.(sqlHandle)
	if !ok {
		t.Fatalf("storage.DB does not expose SQL() *sql.DB — cannot drive the production tasks writer")
	}
	rawDB := h.SQL()
	if rawDB == nil {
		t.Fatalf("db.SQL() returned nil")
	}
	store := coretasks.NewSQLiteStore(rawDB)
	probe := coretasks.Task{
		ID:        "upgrade-path-tasks-probe",
		Kind:      coretasks.KindBash,
		Status:    coretasks.StatusRunning,
		StartedAt: fixedProbeTime,
	}
	if err := store.Insert(ctx, probe); err != nil {
		t.Fatalf("tasks store insert after Open on an upgraded install failed: %v — "+
			"this is the exact failure mode of a table that does not exist (spec.md §1.4)", err)
	}
	if err := r.QueryRow(ctx, "SELECT COUNT(*) FROM tasks WHERE id = ?", probe.ID).Scan(&n); err != nil {
		t.Fatalf("re-query tasks after insert: %v", err)
	}
	if n != 1 {
		t.Errorf("tasks row for %s after insert = %d, want 1", probe.ID, n)
	}
}

// assertRerunPolicyToleratedOnLoad is automation-actually-runs-01PMZ404
// UNIT-13's persistence-integrity assertion (owner ruling A-10).
//
// core/workflows/schema.go's ValidateForSave now refuses a non-empty
// rerun_policy outright — none of the six historically-accepted values
// ("fresh", "continue", "ask", "always", "skip", "prompt") ever did
// anything, because Engine.Cache has no production assignment. But a
// row THIS SNAPSHOT'S OWN RELEASE could have written under the old,
// lenient validator may still carry one of those values on disk, and
// core/workflows/storage.go's sqliteStore.Load re-validates the stored
// yaml_source on every single read (LoadYAML -> ValidateForLoad). If
// the load path used the same strict gate as save, that row would
// fail to parse and the workflow would vanish from the user's list —
// the X-11 hazard the mission's own spec calls out as "a worse lie
// than the dial it replaces."
//
// The legacy row below is inserted directly via SQL rather than
// through workflows.Store.Save, because Save now rejects exactly this
// value — that rejection is this same function's other half, asserted
// against a SEPARATE, freshly-constructed workflow so the two
// assertions don't entangle with each other's yaml_source caching (a
// workflow already Loaded — and therefore already scrubbed by
// storage.go's Load — carries its OLD raw text in its yaml_source
// cache even after RerunPolicy is cleared in memory; reusing that
// same struct for the save-side assertion would test the cache's
// no-op-on-equal-hash path instead of ValidateForSave).
func assertRerunPolicyToleratedOnLoad(t *testing.T, ctx context.Context, db storage.DB, tag string) {
	t.Helper()
	store := corewf.NewSQLiteStore(db)

	// isKebab (core/workflows/refs.go) rejects '.', so a tag like
	// "v0.69.0" cannot appear verbatim in a workflow id.
	tagSlug := strings.ReplaceAll(tag, ".", "-")

	// ---- Load side: a value this snapshot's release could have
	// written must still load, and must come back cleared. ----
	legacyID := "upgrade-path-rerun-policy-legacy-" + tagSlug
	legacyYAML := "id: " + legacyID + "\n" +
		"name: \"UNIT-13 legacy rerun_policy probe (" + tag + ")\"\n" +
		"version: 1\n" +
		"rerun_policy: skip\n" +
		"steps:\n" +
		"  - name: a\n" +
		"    kind: model_turn\n" +
		"    user_prompt: probe\n"
	now := fixedProbeTime.UnixNano()
	if err := db.WriteTx(ctx, func(tx storage.WriteTx) error {
		_, err := tx.Exec(ctx,
			`INSERT INTO workflows (id, name, description, yaml_source, version, hash, created_at, updated_at)
			 VALUES (?, ?, '', ?, 1, 'upgrade-path-unit13-probe-hash', ?, ?)`,
			legacyID, "UNIT-13 legacy rerun_policy probe", legacyYAML, now, now,
		)
		return err
	}); err != nil {
		t.Fatalf("insert legacy rerun_policy=skip workflow row directly (simulating what a previous release's Store.Save would have written): %v", err)
	}

	loaded, err := store.Load(ctx, legacyID)
	if err != nil {
		t.Fatalf("Load on a %s-snapshot-shaped workflow row with a legacy rerun_policy=skip failed — this is exactly the vanish-on-open regression X-11 warns about: %v", tag, err)
	}
	if loaded.RerunPolicy != "" {
		t.Errorf("Load(%s).RerunPolicy = %q, want \"\" — storage.go's Load must drop a stale value, not merely tolerate it", legacyID, loaded.RerunPolicy)
	}
	summaries, err := store.List(ctx)
	if err != nil {
		t.Fatalf("List after inserting a legacy-rerun_policy row: %v", err)
	}
	found := false
	for _, s := range summaries {
		if s.ID == legacyID {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("List does not include %q — a stored workflow with a legacy rerun_policy vanished after upgrade (X-11)", legacyID)
	}

	// ---- Save side: a NEW, freshly-authored workflow with a
	// non-empty rerun_policy is refused outright, and the error names
	// the field. ----
	badID := "upgrade-path-rerun-policy-bad-" + tagSlug
	bad := corewf.Workflow{
		ID:          badID,
		Name:        "UNIT-13 save-rejection probe",
		Version:     1,
		RerunPolicy: "skip",
		Steps: []corewf.Step{
			{Name: "a", Kind: corewf.StepKindModelTurn, UserPrompt: "probe"},
		},
	}
	if _, err := store.Save(ctx, bad); err == nil {
		t.Errorf("Save(%s) with rerun_policy=%q succeeded, want refusal (UNIT-13 save-side narrowing, A-10)", badID, bad.RerunPolicy)
	} else if !strings.Contains(err.Error(), "rerun_policy") {
		t.Errorf("Save(%s) rejection error = %q, want it to name rerun_policy", badID, err.Error())
	}
	if _, lerr := store.Load(ctx, badID); lerr == nil {
		t.Errorf("Load(%s) succeeded after its Save was refused — a rejected save must not have persisted a row", badID)
	}

	// ---- Round trip: a fresh, valid (empty) rerun_policy saves and
	// loads back unchanged. ----
	goodID := "upgrade-path-rerun-policy-good-" + tagSlug
	good := corewf.Workflow{
		ID:      goodID,
		Name:    "UNIT-13 round-trip probe",
		Version: 1,
		Steps: []corewf.Step{
			{Name: "a", Kind: corewf.StepKindModelTurn, UserPrompt: "probe"},
		},
	}
	if _, err := store.Save(ctx, good); err != nil {
		t.Fatalf("Save(%s) with empty rerun_policy failed: %v", goodID, err)
	}
	reloaded, err := store.Load(ctx, goodID)
	if err != nil {
		t.Fatalf("Load(%s) after a clean save failed: %v", goodID, err)
	}
	if reloaded.RerunPolicy != "" {
		t.Errorf("Load(%s).RerunPolicy = %q after a round trip of the empty value, want \"\"", goodID, reloaded.RerunPolicy)
	}
}

// assertPragmaClean asserts the two secondary integrity pragmas spec
// §1.3 proves are NOT sufficient on their own (they stay clean through
// a silent cascade that empties a whole table) — asserted anyway
// because a dirty result IS meaningful, even though a clean one isn't
// proof of correctness.
func assertPragmaClean(t *testing.T, ctx context.Context, db storage.DB) {
	t.Helper()
	r := db.Reader()

	rows, err := r.Query(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		t.Fatalf("foreign_key_check: %v", err)
	}
	var violations int
	for rows.Next() {
		violations++
	}
	_ = rows.Close()
	if violations > 0 {
		t.Errorf("PRAGMA foreign_key_check reported %d violation(s) — secondary check, but a nonzero result here is real", violations)
	}

	var integrity string
	if err := r.QueryRow(ctx, "PRAGMA integrity_check").Scan(&integrity); err != nil {
		t.Fatalf("integrity_check: %v", err)
	}
	if integrity != "ok" {
		t.Errorf("PRAGMA integrity_check = %q, want \"ok\"", integrity)
	}
}

func openRawSQLiteAt(t *testing.T, path string) *sql.DB {
	t.Helper()
	dsn := "file:" + url.PathEscape(path) + "?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("open raw sqlite at %s: %v", path, err)
	}
	db.SetMaxOpenConns(1)
	return db
}
