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
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/session"
	"github.com/kameas-ai/kenaz-harness/core/storage"
	storagesqlite "github.com/kameas-ai/kenaz-harness/core/storage/sqlite"
	"github.com/kameas-ai/kenaz-harness/core/storage/sqlite/upgradesnap"
	coretasks "github.com/kameas-ai/kenaz-harness/core/tasks"

	_ "modernc.org/sqlite"
)

// expectedChangedTables lists, per snapshot tag, the tables a migration
// registered ABOVE that snapshot's ledger is declared to alter — so
// their pre/post-Open digest is allowed to differ. Every entry names
// the migration responsible. A table not listed here that WOULD change
// shape (e.g. gains/loses a column) fails the test, which is exactly
// the point: an undeclared shape change is a regression.
var expectedChangedTables = map[string][]string{
	"v0.63.0": {
		// sessions/0333-transcript-moves adds nullable columns to
		// session_messages (kind, move_index, turn_span_id,
		// model_tool_args).
		"session_messages",
		// sessions/0334-move-fidelity-columns adds sessions.move_history_mode.
		"sessions",
	},
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
	},
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
	changed := map[string]bool{"harness_migrations": true, "sessions": true}
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
