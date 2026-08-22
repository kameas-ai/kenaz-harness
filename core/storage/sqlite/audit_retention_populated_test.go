// TestAuditRetention_DeleteAfterWindow_AgainstPopulatedUpgradedDatabase
// is AC-011 / AC-PI-3's concrete evidence for
// audit-that-tells-the-truth-01PMZA10 UNIT-8: DeleteRows is a RUNTIME
// path (not a migration) that check-destructive-migration-coverage.sh
// cannot see by construction (spec §7 G-4), so it gets its own
// populated-table test here, in the same file family as
// TestUpgradePath and for the same reason — CLAUDE.md blind spot #3:
// "a migration that has never run against populated tables has never
// been tested" extends to any destructive runtime path built on top of
// one.
//
// Boots from testdata/upgrade/v0.65.0/dump.sql — a database a PREVIOUS
// RELEASE produced, populated in every unrelated table (sessions,
// session_messages, units, workflows, ...) via seed.sql — not an empty
// Open(). Migration 106 (this mission's own) applies as part of the
// real Open() path exercised here, exactly like every other pending
// migration a real upgrade would apply. Then this test populates events
// itself (the v0.65.0 snapshot's events table is empty — event-log had
// no production writer until this mission), runs the real
// LocalRetentionScheduler, and asserts:
//
//  1. aged rows are gone from events;
//  2. SearchFTS on an aged (purged) term returns SUCCESSFULLY with zero
//     rows — not the pre-106 "fts5: missing row N from content table"
//     error ([RAN] §1.6, reproduced by TestMigration0106Up_FixesTheFTSDeleteLeak
//     at the unit level; this is the same defect proven end-to-end
//     through production Open() against a real upgraded install);
//  3. a fresh (inside-window) row survives, and its term is still
//     searchable;
//  4. every unrelated, pre-existing table's row count is UNCHANGED —
//     the destructive path touched only events/events_fts.
package sqlite_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	eventlog "github.com/kameas-ai/kenaz-harness/core/event/log"
	storagesqlite "github.com/kameas-ai/kenaz-harness/core/storage/sqlite"
	"github.com/kameas-ai/kenaz-harness/core/storage/sqlite/upgradesnap"
)

func TestAuditRetention_DeleteAfterWindow_AgainstPopulatedUpgradedDatabase(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	dumpText, err := os.ReadFile(filepath.Join("testdata", "upgrade", "v0.65.0", "dump.sql"))
	if err != nil {
		t.Fatalf("read v0.65.0 dump.sql: %v", err)
	}
	rawPath := filepath.Join(dir, "data.db")
	raw := openRawSQLiteAt(t, rawPath)
	if err := upgradesnap.Materialize(ctx, raw, string(dumpText)); err != nil {
		t.Fatalf("materialise v0.65.0 snapshot: %v", err)
	}
	preSnap, err := upgradesnap.SnapshotAll(ctx, raw)
	if err != nil {
		t.Fatalf("pre-Open snapshot: %v", err)
	}
	if err := raw.Close(); err != nil {
		t.Fatalf("close raw after materialise: %v", err)
	}
	// Sanity: the fixture really is populated in unrelated tables, not
	// an empty database — the fails-if this whole test exists to guard
	// against.
	for _, tbl := range []string{"sessions", "session_messages", "units", "workflows"} {
		snap, ok := preSnap[tbl]
		if !ok || snap.RowCount == 0 {
			t.Fatalf("fixture sanity: table %q has %d rows before Open — the v0.65.0 snapshot must be populated, not empty", tbl, snap.RowCount)
		}
	}

	// Real production Open() path: applies migration 106 (this
	// mission's) on top of the v0.65.0 ledger, exactly as a real
	// upgraded install's would.
	db, err := storagesqlite.Open(newConfig(dir))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close(context.Background()) })

	backend := eventlog.NewSQLBackend(db)
	store := eventlog.NewStore(backend)

	agedRows := []eventlog.Row{
		{EventID: "pop-aged-1", Kind: "k.a", EmittedAt: time.Now().Add(-400 * 24 * time.Hour),
			Payload: []byte(`{"term":"ZORKAGEDAAA"}`), RedactionSummary: "none"},
		{EventID: "pop-aged-2", Kind: "k.b", EmittedAt: time.Now().Add(-370 * 24 * time.Hour),
			Payload: []byte(`{"term":"ZORKAGEDBBB"}`), RedactionSummary: "none"},
	}
	freshRows := []eventlog.Row{
		{EventID: "pop-fresh-1", Kind: "k.a", EmittedAt: time.Now(),
			Payload: []byte(`{"term":"ZORKFRESHCCC"}`), RedactionSummary: "none"},
	}
	for _, r := range append(append([]eventlog.Row{}, agedRows...), freshRows...) {
		if err := store.AppendComputed(ctx, r); err != nil {
			t.Fatalf("append %s: %v", r.EventID, err)
		}
	}

	// Confirm the purged term IS matchable before the sweep (otherwise
	// the "returns zero rows after" assertion below would be vacuous).
	before, err := backend.SearchFTS(ctx, "ZORKAGEDAAA", "", nil, 10)
	if err != nil {
		t.Fatalf("pre-sweep SearchFTS: %v", err)
	}
	if len(before) != 1 {
		t.Fatalf("pre-sweep SearchFTS(ZORKAGEDAAA) = %d rows, want 1", len(before))
	}

	if err := eventlog.WriteRetentionPolicy(ctx, db, eventlog.PersistedPolicy{
		Kind: eventlog.RetentionDeleteAfterWindow, WindowDays: 30,
	}); err != nil {
		t.Fatalf("WriteRetentionPolicy: %v", err)
	}
	sched := eventlog.NewLocalRetentionScheduler(backend, db, dir)
	res, err := sched.SweepOnce(ctx)
	if err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}
	if res.Purged != len(agedRows) {
		t.Fatalf("Purged = %d, want %d (exactly the aged rows)", res.Purged, len(agedRows))
	}

	// (1) Aged rows gone from events.
	for _, r := range agedRows {
		if _, err := backend.GetRow(ctx, r.EventID); err == nil {
			t.Errorf("%s still present after sweep", r.EventID)
		}
	}

	// (2) THE defect line: SearchFTS on a purged term must succeed with
	// zero rows, not error. This is the assertion the mission spec
	// explicitly warns a weaker test would skip: "Fails if: the FTS
	// assertion checks only the result set and not the error."
	afterAged, err := backend.SearchFTS(ctx, "ZORKAGEDAAA", "", nil, 10)
	if err != nil {
		t.Fatalf("post-sweep SearchFTS(ZORKAGEDAAA) errored — this is the exact pre-106 defect "+
			"('fts5: missing row N from content table'), reproduced end-to-end through production Open() "+
			"against a real upgraded install: %v", err)
	}
	if len(afterAged) != 0 {
		t.Errorf("post-sweep SearchFTS(ZORKAGEDAAA) = %d rows, want 0 (term must be gone from the index)", len(afterAged))
	}

	// (2b) The PRIVACY half of the defect, specifically — and the half
	// that actually distinguishes red/green here. SQLBackend.SearchFTS
	// joins events_fts to events (`... JOIN events e ON e.rowid =
	// events_fts.rowid ...`), so a MATCH on a rowid missing from events
	// silently drops out of the join with no error — meaning (2) above,
	// unlike the mission spec's [RAN] transcript (a direct
	// `SELECT rowid, quote(payload) FROM events_fts WHERE ... MATCH`,
	// no join), does not actually distinguish the fixed tree from the
	// broken one. THIS query is the one that does: a raw MATCH count
	// directly against events_fts, bypassing the join entirely, is
	// exactly the "existence oracle" F-2's privacy half describes —
	// "counting matches on a term confirms it appeared in a payload the
	// panel calls 'permanently deleted'". Verified by mutation: with
	// migration 106 removed from the registered set, this assertion
	// goes red (count=1) while (1)-(3) above stay green.
	var rawMatchCount int
	if err := db.Reader().QueryRow(ctx,
		"SELECT count(*) FROM events_fts WHERE events_fts MATCH ?", "ZORKAGEDAAA").Scan(&rawMatchCount); err != nil {
		t.Fatalf("raw events_fts MATCH count query: %v", err)
	}
	if rawMatchCount != 0 {
		t.Errorf("raw events_fts MATCH count for a purged term = %d, want 0 — the term must not remain an "+
			"existence oracle over data the panel calls \"permanently deleted\" (spec §1.6 F-2 privacy half)",
			rawMatchCount)
	}

	// (3) Fresh row survives, still searchable.
	if _, err := backend.GetRow(ctx, "pop-fresh-1"); err != nil {
		t.Errorf("pop-fresh-1 missing after sweep: %v", err)
	}
	afterFresh, err := backend.SearchFTS(ctx, "ZORKFRESHCCC", "", nil, 10)
	if err != nil {
		t.Fatalf("post-sweep SearchFTS(ZORKFRESHCCC): %v", err)
	}
	if len(afterFresh) != 1 {
		t.Errorf("post-sweep SearchFTS(ZORKFRESHCCC) = %d rows, want 1 (fresh row must remain searchable)", len(afterFresh))
	}

	// (4) Every unrelated, pre-existing table is untouched — the
	// destructive path touched only events/events_fts. events itself
	// is excluded from this loop (its count is asserted directly above:
	// 2 aged rows in, minus 2 deleted, plus 1 fresh = 1 row net; the
	// PRE-open snapshot has 0 events rows since the v0.65.0 fixture
	// never wrote any).
	postRaw := openRawSQLiteAt(t, rawPath)
	postSnap, err := upgradesnap.SnapshotAll(ctx, postRaw)
	if err != nil {
		t.Fatalf("post-sweep snapshot: %v", err)
	}
	if err := postRaw.Close(); err != nil {
		t.Fatalf("close postRaw: %v", err)
	}
	for table, before := range preSnap {
		switch table {
		case "events", "retention_config", "harness_migrations", "event_chain_heads":
			// events: this test's own subject. retention_config: this
			// test wrote the policy. harness_migrations: migration 106
			// applying is expected. event_chain_heads: AppendComputed
			// writes a chain-head row per session_id (including the
			// empty session_id these synthetic rows use) — a normal
			// side effect of appending, not of the sweep.
			continue
		case "scheduled_chat_runs":
			// model-scheduled-jobs-01PMSJ01 WP09's sessions/0340 migration
			// ADD COLUMNs created_by + tool_allowlist onto this table,
			// which the v0.65.0 fixture predates — the real Open() path
			// this test drives applies 0340 same as any other pending
			// migration, changing the table's digest (not its row
			// count) as a normal side effect of schema evolution, not of
			// the retention sweep. Row count is still checked below
			// (only Digest is skipped) so a real regression in this
			// table would still be caught. Mirrors upgrade_path_test.go's
			// expectedChangedTables entries for the same migration.
			after, ok := postSnap[table]
			if !ok {
				t.Errorf("table %s present before sweep, missing after", table)
				continue
			}
			if before.RowCount != after.RowCount {
				t.Errorf("unrelated table %s row count changed: %d -> %d (retention sweep must not touch it)",
					table, before.RowCount, after.RowCount)
			}
			continue
		}
		after, ok := postSnap[table]
		if !ok {
			t.Errorf("table %s present before sweep, missing after", table)
			continue
		}
		if before.RowCount != after.RowCount {
			t.Errorf("unrelated table %s row count changed: %d -> %d (retention sweep must not touch it)",
				table, before.RowCount, after.RowCount)
		}
		if before.Digest != after.Digest {
			t.Errorf("unrelated table %s content digest changed (retention sweep must not touch it)", table)
		}
	}
	var eventsCount int
	if err := db.Reader().QueryRow(ctx, "SELECT COUNT(*) FROM events").Scan(&eventsCount); err != nil {
		t.Fatalf("count events: %v", err)
	}
	if eventsCount != 1 {
		t.Errorf("events row count after sweep = %d, want 1 (only pop-fresh-1 survives)", eventsCount)
	}
}
