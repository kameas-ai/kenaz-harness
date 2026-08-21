package sqlite_test

// upgrade_path_approval_test.go — approval-node-01PMZC12 UNIT-PI
// (AC-PI-1).
//
// This mission's spec (C-3) establishes that a run paused at ANY node
// — `ask` today, `approval` since UNIT-2 — is durable in its EVENT
// TRAIL (agent_graph_events, migration sessions/0309) but NOT in the
// in-memory run registry (Manager.runs): a process restart loses the
// registry, so the run becomes permanently unresumable while its last
// durable word stays "run_paused" forever — a record saying a human
// was asked and never answered.
//
// UNIT-6 (durable pause: persist enough of the pending decision + Env
// to rehydrate it at boot) is the fix, and is EXPLICITLY GATED ON
// ESCALATION E-002 in this mission's spec (spec.md §13, plan.md's
// sequencing rule 7: "if the mission is cut short, cut at UNIT-4 —
// never at UNIT-2"; UNIT-6 is P2 and may be replaced by an "abandoned"
// alternative — spec.md §5.5). Neither UNIT-6 nor the abandoned
// alternative was implemented in this pass; E-002 is left OPEN and
// unresolved rather than answered under time pressure — see this
// mission's UNIT-PI report for the full disclosure.
//
// Per this template's AC-PI-1, a test asserting anything about
// persistence, migration selection or schema evolution must boot from
// a database a PREVIOUS RELEASE produced, not from Open on an empty
// directory (CLAUDE.md blind spot #3 / the v0.63.0 P0). This test
// does exactly that: it materialises the newest COMMITTED upgrade
// snapshot (v0.64.0 as of this mission — the chain has not been
// extended past it for five releases, v0.65.0..v0.69.0, a pre-existing
// release-ritual gap this mission did not create and is out of scope
// to backfill here), appends a real run_paused + approval_pending
// event pair through the production coreag.SQLEventLog exactly as the
// approval executor would on a genuine pause, closes the database
// (simulating the process exit), reopens a FRESH connection under HEAD
// (simulating the restart), and asserts what actually happens today:
// the run is unresumable and GetRunStatus-equivalent reports "not
// found" — the trace's last row still claims run_paused.
//
// THIS TEST DOCUMENTS A KNOWN GAP, NOT A FIX. It exists so the gap is
// pinned against a real upgraded database rather than merely asserted
// in prose, and so the day UNIT-6 or the abandoned alternative lands,
// this test's own assertion flips from "still broken" to a positive
// resumable/abandoned check — the mechanical signal that G7 has
// actually been met, rather than another paragraph claiming it.

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	coreag "github.com/kameas-ai/kenaz-harness/core/agentgraph"
	graphview "github.com/kameas-ai/kenaz-harness/core/rpc/views/agentgraph"
	storagesqlite "github.com/kameas-ai/kenaz-harness/core/storage/sqlite"
	"github.com/kameas-ai/kenaz-harness/core/storage/sqlite/upgradesnap"

	_ "modernc.org/sqlite"
)

// newestCommittedUpgradeSnapshotTag returns the highest vX.Y.Z tag
// under testdata/upgrade with a dump.sql — i.e. the actual latest
// database a previous release produced, whatever that is today. Not
// hardcoded to "v0.64.0": if a future commit extends the chain, this
// test picks up the newer snapshot automatically rather than silently
// testing an increasingly stale one.
func newestCommittedUpgradeSnapshotTag(t *testing.T) string {
	t.Helper()
	entries, err := os.ReadDir("testdata/upgrade")
	if err != nil {
		t.Fatalf("read testdata/upgrade: %v", err)
	}
	var tags []string
	for _, e := range entries {
		if !e.IsDir() || !upgradesnap.IsSnapshotTag(e.Name()) {
			continue
		}
		if _, err := os.Stat(filepath.Join("testdata/upgrade", e.Name(), "dump.sql")); err != nil {
			continue
		}
		tags = append(tags, e.Name())
	}
	if len(tags) == 0 {
		t.Fatal("no committed upgrade snapshots found under testdata/upgrade")
	}
	sorted := upgradesnap.SortedSnapshotTags(tags)
	return sorted[len(sorted)-1]
}

// sqlHandle mirrors core/rpc/api.go's buildAgentGraphEventLog helper:
// the narrow surface needed to hand a *sql.DB to coreag.NewSQLEventLog.
type sqlHandle interface{ SQL() *sql.DB }

func TestUpgradePath_PausedApprovalIsNotResumableAcrossRestart(t *testing.T) {
	tag := newestCommittedUpgradeSnapshotTag(t)
	ctx := context.Background()
	dir := t.TempDir()

	dumpText, err := os.ReadFile(filepath.Join("testdata", "upgrade", tag, "dump.sql"))
	if err != nil {
		t.Fatalf("read %s dump.sql: %v", tag, err)
	}
	rawPath := filepath.Join(dir, "data.db")
	raw := openRawSQLiteAt(t, rawPath)
	if err := upgradesnap.Materialize(ctx, raw, string(dumpText)); err != nil {
		t.Fatalf("materialise %s snapshot: %v", tag, err)
	}
	if err := raw.Close(); err != nil {
		t.Fatalf("close raw after materialise: %v", err)
	}

	// "Boot 1": open under HEAD (applies whatever migrations are newer
	// than the snapshot, including 0309 if the snapshot predates it —
	// it does not, 0309 landed well before v0.64.0, but the point of
	// booting through storagesqlite.Open rather than assuming the
	// table exists is that this test does not get to assume anything
	// about the snapshot's schema state).
	db1, err := storagesqlite.Open(newConfig(dir))
	if err != nil {
		t.Fatalf("Open on the %s snapshot failed: %v", tag, err)
	}
	h1, ok := db1.(sqlHandle)
	if !ok {
		t.Fatalf("%T does not expose SQL() *sql.DB", db1)
	}
	rawDB1 := h1.SQL()
	if rawDB1 == nil {
		t.Fatal("SQL() returned nil")
	}

	// A run pauses at an approval node — the exact event pair
	// approvalExecutor.Execute writes on its first fire (UNIT-2):
	// approval_pending, then the kernel's kind-agnostic pause path
	// appends run_paused.
	const runID = "run-upgrade-path-approval-probe"
	const nodeID = "a"
	log1 := coreag.NewSQLEventLog(rawDB1)
	var batch coreag.EventBatch
	if err := batch.AppendKind(runID, nodeID, coreag.EventApprovalPending, map[string]any{
		"prompt":        "Ship it?",
		"approver_role": "user",
	}); err != nil {
		t.Fatalf("AppendKind approval_pending: %v", err)
	}
	if err := batch.AppendKind(runID, "", coreag.EventRunPaused, map[string]any{
		"reason": "approval: Ship it?",
	}); err != nil {
		t.Fatalf("AppendKind run_paused: %v", err)
	}
	if _, err := log1.Append(batch); err != nil {
		t.Fatalf("Append pause events: %v", err)
	}

	// Confirm the durable half really is durable: replay the log back
	// and see both events, BEFORE simulating the restart. If this
	// fails, the rest of the test proves nothing about the gap — it
	// would just mean the write itself didn't land.
	var replayed []coreag.Event
	if err := log1.Replay(runID, func(e coreag.Event) error {
		replayed = append(replayed, e)
		return nil
	}); err != nil {
		t.Fatalf("Replay before restart: %v", err)
	}
	if len(replayed) != 2 {
		t.Fatalf("expected 2 events written before the simulated restart, got %d: %+v", len(replayed), replayed)
	}

	// Simulate the process exit: close every handle. Nothing about the
	// in-memory Manager (which does not exist in this test at all) is
	// carried across this line.
	if err := db1.Close(ctx); err != nil {
		t.Fatalf("close db1 (simulated process exit): %v", err)
	}

	// "Boot 2": a FRESH Manager, FRESH database connection, backed by
	// the SAME on-disk data.db. This is the shape a real restart takes
	// today — buildAgentGraphEventLog reconstructs a SQLEventLog over
	// whatever *sql.DB core.New() opens, and NewManager's runs map
	// starts empty every time (manager.go: `runs: map[string]*runEntry{}`,
	// loaded from nowhere).
	db2, err := storagesqlite.Open(newConfig(dir))
	if err != nil {
		t.Fatalf("re-Open (simulated restart) failed: %v", err)
	}
	t.Cleanup(func() { _ = db2.Close(context.Background()) })
	h2, ok := db2.(sqlHandle)
	if !ok {
		t.Fatalf("%T does not expose SQL() *sql.DB", db2)
	}
	log2 := coreag.NewSQLEventLog(h2.SQL())

	// The event trail survived the restart — this half of C-3 is true
	// and this mission did not need to fix it.
	var replayedAfterRestart []coreag.Event
	if err := log2.Replay(runID, func(e coreag.Event) error {
		replayedAfterRestart = append(replayedAfterRestart, e)
		return nil
	}); err != nil {
		t.Fatalf("Replay after restart: %v", err)
	}
	if len(replayedAfterRestart) != 2 {
		t.Fatalf("event trail did not survive the restart: got %d events, want 2", len(replayedAfterRestart))
	}
	lastKind := replayedAfterRestart[len(replayedAfterRestart)-1].Kind
	if lastKind != coreag.EventRunPaused {
		t.Fatalf("last durable event is %q, want %q", lastKind, coreag.EventRunPaused)
	}

	// The run registry did NOT survive — a fresh Manager over the same
	// event log has never heard of this run. This is C-3's documented
	// gap, unchanged by this mission (UNIT-6 not built; E-002 open).
	mgr, err := graphview.NewManager(
		graphview.WithDataDir(dir),
		graphview.WithEventLog(log2),
	)
	if err != nil {
		t.Fatalf("NewManager (boot 2): %v", err)
	}
	impl := graphview.New(mgr)
	_, err = impl.GetRunStatus(ctx, runID)
	if err == nil {
		t.Fatalf("GetRunStatus unexpectedly succeeded after a simulated restart — " +
			"if this is because UNIT-6 (or the abandoned-run alternative) has landed, " +
			"update this test to assert the NEW positive behaviour (resumable, or " +
			"explicitly reported abandoned with an event) instead of deleting it; " +
			"G7 / E-002's disposition should be recorded in the commit that flips this.")
	}

	// The trace itself still asserts a pending human decision that no
	// surface will ever offer again — the exact "silence" spec.md §5.5
	// calls the one unacceptable outcome if UNIT-6 is cut. This
	// assertion is what should start failing, loudly, the moment
	// someone builds the abandoned-run alternative without also
	// updating this test — a stale test here is worse than no test.
	var traceAfterFailedLookup []coreag.Event
	if err := log2.Replay(runID, func(e coreag.Event) error {
		traceAfterFailedLookup = append(traceAfterFailedLookup, e)
		return nil
	}); err != nil {
		t.Fatalf("Replay after failed GetRunStatus: %v", err)
	}
	finalKind := traceAfterFailedLookup[len(traceAfterFailedLookup)-1].Kind
	if finalKind != coreag.EventRunPaused {
		t.Fatalf("trace's last word is %q, want %q (run_paused, forever, per C-3) — "+
			"if an abandonment event now gets appended lazily on lookup, that is progress; "+
			"update this test to assert it explicitly rather than leaving this check stale",
			finalKind, coreag.EventRunPaused)
	}
}
