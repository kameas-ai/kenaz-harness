package sqlite_test

// upgrade_path_approval_test.go — approval-node-01PMZC12 UNIT-PI
// (AC-PI-1, AC-08, G7).
//
// This mission's spec (C-3) establishes that a run paused at ANY node
// — `ask` today, `approval` since UNIT-2 — is durable in its EVENT
// TRAIL (agent_graph_events, migration sessions/0309) but NOT in the
// in-memory run registry (Manager.runs): a process restart loses the
// registry, so a naive re-lookup would report "not found" while the
// trace's last durable word still says a human was asked and never
// answered.
//
// UNIT-6 (durable pause: persist enough of the pending decision + Env
// to rehydrate a run BACK TO RUNNING at boot) is EXPLICITLY GATED ON
// ESCALATION E-002 in this mission's spec (spec.md §13, plan.md's
// sequencing rule 7: "if the mission is cut short, cut at UNIT-4 —
// never at UNIT-2"; UNIT-6 is P2 and may be replaced by an "abandoned"
// alternative — spec.md §5.5) and remains CUT: no code rebuilds a
// resumable *coreag.Env from the log. What landed instead is the
// alternative spec.md §5.5 sanctions — Manager.rehydrateAbandonedRuns,
// called from NewManager, marks an orphaned paused run
// RunStateAbandoned with a recorded reason and an EventRunAbandoned
// row, rather than answering "not found" (silence) forever. This test
// now asserts THAT behaviour, updated from the "known gap, not a fix"
// version this mission originally shipped with UNIT-6/E-002 left open.
//
// Per this template's AC-PI-1, a test asserting anything about
// persistence, migration selection or schema evolution must boot from
// a database a PREVIOUS RELEASE produced, not from Open on an empty
// directory (CLAUDE.md blind spot #3 / the v0.63.0 P0). This test
// does exactly that: it materialises the newest COMMITTED upgrade
// snapshot, appends a real run_paused + approval_pending event pair
// through the production coreag.SQLEventLog exactly as the approval
// executor would on a genuine pause, closes the database (simulating
// the process exit), reopens a FRESH connection under HEAD (simulating
// the restart) and constructs a FRESH Manager over it, and asserts the
// run is reported RunStateAbandoned — with a non-generic reason and an
// EventRunAbandoned row appended to the durable trail — not "not
// found".
//
// AC-PI-1 falsifiability: reverting rehydrateAbandonedRuns's wiring in
// NewManager (core/rpc/views/agentgraph/manager.go) makes this test
// fail with "run ... not found" again — proved directly in this
// mission's abandoned_run_test.go, which disables the same call site
// and asserts the resulting failure names the run as not-found.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
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
// sqlHandle is declared once for this package in wp21_test.go — both
// missions introduced an identical structural interface independently
// and the duplicate would not compile. Reusing the existing one rather
// than renaming: two names for one shape is how the next merge gets a
// third.

func TestUpgradePath_PausedApprovalIsAbandonedAcrossRestart(t *testing.T) {
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

	// The run registry did NOT survive the restart — a fresh Manager's
	// runs map starts empty regardless of what the log holds. What
	// happens next is the property under test: NewManager's boot-time
	// rehydrateAbandonedRuns pass (E-002's "abandoned" fallback) must
	// find this orphaned pause and register it truthfully instead of
	// leaving it to answer "not found".
	mgr, err := graphview.NewManager(
		graphview.WithDataDir(dir),
		graphview.WithEventLog(log2),
	)
	if err != nil {
		t.Fatalf("NewManager (boot 2): %v", err)
	}
	impl := graphview.New(mgr)
	st, err := impl.GetRunStatus(ctx, runID)
	if err != nil {
		t.Fatalf("GetRunStatus after a simulated restart: %v — "+
			"an orphaned paused run must be reported RunStateAbandoned, not "+
			"fail as not-found (spec.md §5.5: silence is the one unacceptable outcome)", err)
	}
	if st.State != graphview.RunStateAbandoned {
		t.Fatalf("state after restart = %q, want %q", st.State, graphview.RunStateAbandoned)
	}
	if strings.TrimSpace(st.Error) == "" {
		t.Fatalf("abandoned run must record a non-empty reason")
	}
	if st.PendingApproval != nil {
		t.Fatalf("an abandoned run must not still advertise a resolvable pending approval; got %+v", st.PendingApproval)
	}

	// The event stream, not just the status struct (spec.md §9 rule 1):
	// the durable trail must end on an EventRunAbandoned row recording
	// why — the mechanical signal that G7's fallback actually landed,
	// not just a paragraph claiming it. This is what should start
	// failing, loudly, the moment someone reverts the rehydration wiring
	// — proved directly by this mission's abandoned_run_test.go, which
	// disables the same call site and asserts the resulting
	// "not found" failure.
	var traceAfterLookup []coreag.Event
	if err := log2.Replay(runID, func(e coreag.Event) error {
		traceAfterLookup = append(traceAfterLookup, e)
		return nil
	}); err != nil {
		t.Fatalf("Replay after GetRunStatus: %v", err)
	}
	finalKind := traceAfterLookup[len(traceAfterLookup)-1].Kind
	if finalKind != coreag.EventRunAbandoned {
		t.Fatalf("trace's last word is %q, want %q — a paused run's trail must not still "+
			"read as an unanswered human decision once it has been reconciled as abandoned",
			finalKind, coreag.EventRunAbandoned)
	}

	// Resolving an abandoned run must be refused — the pending decision
	// no longer exists to resolve.
	if err := impl.ResolveApproval(ctx, runID, nodeID, true, "too late"); err == nil {
		t.Fatalf("ResolveApproval on an abandoned run must be refused")
	}
}
