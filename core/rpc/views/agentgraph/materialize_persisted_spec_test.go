package agentgraph_test

// feat/graph-resolved-spec WP02: run views read the persisted spec; the
// reconstruction is labelled and reachable only for runs that have none.
//
// Both behaviour pins run on real sqlite through the production SQL
// event log, with a FRESH manager after the run — "after a restart" is
// the case the in-memory registries could never answer:
//
//   - TestMaterializeRun_EditedGraphStillShowsWhatRan: a run executes,
//     its graph is then EDITED (a node added, a node retitled), and the
//     run's materialized graph still shows the topology that executed,
//     unmarked. Before WP02 the restart pushed this run onto the
//     library-file reconstruction, which loaded the edited graph — the
//     view showed a node that never existed when the run happened.
//   - TestMaterializeRun_PreSnapshotRunOnUpgradedInstall_IsLabelledReconstruction:
//     a run recorded before the store existed (no agent_graph_run_specs
//     row) on a database a previous release produced (v0.86.0 snapshot)
//     still materializes, and says it is a reconstruction — even when
//     its run_start's spec_digest matches the library file, the case the
//     deleted digest-verified upgrade used to serve as exact.

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	coreag "github.com/kameas-ai/kenaz-harness/core/agentgraph"
	graphview "github.com/kameas-ai/kenaz-harness/core/rpc/views/agentgraph"
	"github.com/kameas-ai/kenaz-harness/core/session"
	"github.com/kameas-ai/kenaz-harness/core/storage"
	storagesqlite "github.com/kameas-ai/kenaz-harness/core/storage/sqlite"
	"github.com/kameas-ai/kenaz-harness/core/storage/sqlite/upgradesnap"

	_ "modernc.org/sqlite"
)

// openSQLEventLog returns the production SQL event log over a real
// sqlite file carrying the production DDL of the two tables it touches:
// sessions/0309 (agent_graph_events) and sessions/0343
// (agent_graph_run_specs).
func openSQLEventLog(t *testing.T) *coreag.SQLEventLog {
	t.Helper()
	log, _ := openSQLEventLogDB(t)
	return log
}

// openSQLEventLogDB is openSQLEventLog plus the raw handle, for tests
// that corrupt a stored row behind the log's back.
func openSQLEventLogDB(t *testing.T) (*coreag.SQLEventLog, *sql.DB) {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "events.db"))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	applied := 0
	for _, m := range session.Migrations() {
		if m.ID != "sessions/0309-agent-graph-events" && m.ID != "sessions/0343-agent-graph-run-specs" {
			continue
		}
		for _, stmt := range strings.Split(m.UpSource, ";") {
			if strings.TrimSpace(stmt) == "" {
				continue
			}
			if _, err := db.Exec(stmt); err != nil {
				t.Fatalf("apply %s: %v", m.ID, err)
			}
		}
		applied++
	}
	if applied != 2 {
		t.Fatalf("applied %d of the 2 migrations the event log needs", applied)
	}
	return coreag.NewSQLEventLog(db), db
}

const editedGraphV1 = `spec_version: "1"
id: edited_later
entrypoints: [first]
nodes:
  - id: first
    kind: transform
    title: First
    attrs:
      name: concat
  - id: second
    kind: transform
    title: Second as it ran
    attrs:
      name: concat
edges:
  - from: {node: first, port: out}
    to: {node: second, port: in}
`

const editedGraphV2 = `spec_version: "1"
id: edited_later
entrypoints: [first]
nodes:
  - id: first
    kind: transform
    title: First
    attrs:
      name: concat
  - id: second
    kind: transform
    title: Second after the edit
    attrs:
      name: concat
  - id: added_later
    kind: transform
    title: Added after the run
    attrs:
      name: concat
edges:
  - from: {node: first, port: out}
    to: {node: second, port: in}
  - from: {node: second, port: out}
    to: {node: added_later, port: in}
`

func TestMaterializeRun_EditedGraphStillShowsWhatRan(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := t.TempDir()
	log := openSQLEventLog(t)

	before, err := graphview.NewManager(graphview.WithDataDir(dir), graphview.WithEventLog(log))
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	a := graphview.New(before)
	if err := a.SaveGraph(ctx, graphview.GraphSpec{ID: "edited_later", YAML: editedGraphV1}, "user"); err != nil {
		t.Fatalf("SaveGraph v1: %v", err)
	}
	resp, err := a.StartRun(ctx, graphview.StartRunRequest{GraphID: "edited_later"})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	waitState(t, a, resp.RunID, graphview.RunStateCompleted)

	// The user edits the graph after the run.
	if err := a.SaveGraph(ctx, graphview.GraphSpec{ID: "edited_later", YAML: editedGraphV2}, "user"); err != nil {
		t.Fatalf("SaveGraph v2: %v", err)
	}

	// Restart: nothing in memory, same database, same library dir.
	after, err := graphview.NewManager(graphview.WithDataDir(dir), graphview.WithEventLog(log))
	if err != nil {
		t.Fatalf("NewManager (after restart): %v", err)
	}
	spec, err := graphview.New(after).MaterializeRun(ctx, resp.RunID)
	if err != nil {
		t.Fatalf("MaterializeRun: %v", err)
	}
	if spec.SpecProvenance != "" {
		t.Errorf("SpecProvenance = %q, want \"\" — the persisted spec is exact", spec.SpecProvenance)
	}
	mg, err := coreag.LoadYAML([]byte(spec.YAML))
	if err != nil {
		t.Fatalf("materialized YAML does not parse: %v", err)
	}
	if strings.Contains(mg.Description, "DEGRADED") {
		t.Errorf("a run with a persisted spec is described as degraded:\n%s", mg.Description)
	}
	titles := map[string]string{}
	for _, n := range mg.Nodes {
		titles[n.ID] = n.Title
		if strings.HasPrefix(n.ID, "added_later") {
			t.Errorf("materialized run shows %q — a node added AFTER the run", n.ID)
		}
	}
	for _, want := range []string{"first@1", "second@1"} {
		if _, ok := titles[want]; !ok {
			t.Errorf("materialized run is missing %q (nodes: %v)", want, titles)
		}
	}
	if got := titles["second@1"]; !strings.Contains(got, "Second as it ran") {
		t.Errorf("second@1 title = %q, want the title it ran under, not the edit", got)
	}
}

// The ledger's closing condition for the 2026-10-04 entry, verbatim: "a
// test materializes a 65th-turn run with exact provenance after an edit
// to its library file". A chat-shaped run (TrackExternalRun + the shared
// kernel, as the chat runner does) is evicted from the 64-entry registry
// by 64 later turns, its graph is edited, and it still materializes the
// spec that ran.
func TestMaterializeRun_65thTurnAfterLibraryEdit_IsExact(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := t.TempDir()
	log := openSQLEventLog(t)
	mgr, err := graphview.NewManager(graphview.WithDataDir(dir), graphview.WithEventLog(log))
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	a := graphview.New(mgr)
	if err := a.SaveGraph(ctx, graphview.GraphSpec{ID: "edited_later", YAML: editedGraphV1}, "user"); err != nil {
		t.Fatalf("SaveGraph v1: %v", err)
	}
	ran, err := mgr.LoadGraphSpec("edited_later")
	if err != nil {
		t.Fatalf("LoadGraphSpec: %v", err)
	}
	const runID = "chat-01J0000000000000000000TURN1"
	mgr.TrackExternalRun(runID, ran)
	if err := mgr.Kernel().Run(ctx, &coreag.Env{RunID: runID, Graph: &ran}); err != nil {
		t.Fatalf("kernel run: %v", err)
	}
	for i := 0; i < 64; i++ {
		mgr.TrackExternalRun(fmt.Sprintf("chat-filler-%02d", i), ran)
	}
	if err := a.SaveGraph(ctx, graphview.GraphSpec{ID: "edited_later", YAML: editedGraphV2}, "user"); err != nil {
		t.Fatalf("SaveGraph v2: %v", err)
	}
	spec, err := a.MaterializeRun(ctx, runID)
	if err != nil {
		t.Fatalf("MaterializeRun: %v", err)
	}
	if spec.SpecProvenance != "" {
		t.Errorf("evicted, edited 65th-turn run: SpecProvenance = %q, want exact", spec.SpecProvenance)
	}
	if strings.Contains(spec.YAML, "Second after the edit") || !strings.Contains(spec.YAML, "Second as it ran") {
		t.Errorf("materialized run does not show the spec that ran:\n%s", spec.YAML)
	}
}

const v0860DumpRelPath = "../../../storage/sqlite/testdata/upgrade/v0.86.0/dump.sql"

func TestMaterializeRun_PreSnapshotRunOnUpgradedInstall_IsLabelledReconstruction(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	dumpText, err := os.ReadFile(v0860DumpRelPath)
	if err != nil {
		t.Fatalf("read v0.86.0 dump.sql: %v", err)
	}
	raw, err := sql.Open("sqlite", "file:"+url.PathEscape(filepath.Join(dir, "data.db"))+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatalf("open raw sqlite: %v", err)
	}
	raw.SetMaxOpenConns(1)
	if err := upgradesnap.Materialize(ctx, raw, string(dumpText)); err != nil {
		t.Fatalf("materialise v0.86.0 snapshot: %v", err)
	}
	if err := raw.Close(); err != nil {
		t.Fatalf("close raw: %v", err)
	}

	db, err := storagesqlite.Open(storage.Config{DataDir: dir, EncryptionStatus: storage.EncryptionStatusDisabledWithDiskEncryption})
	if err != nil {
		t.Fatalf("Open on the v0.86.0 snapshot: %v", err)
	}
	t.Cleanup(func() { _ = db.Close(context.Background()) })
	h, ok := db.(interface{ SQL() *sql.DB })
	if !ok {
		t.Fatal("storage handle does not expose SQL()")
	}
	log := coreag.NewSQLEventLog(h.SQL())

	mgr, err := graphview.NewManager(graphview.WithEventLog(log))
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	lib, err := mgr.LoadGraphSpec("chat_default")
	if err != nil {
		t.Fatalf("LoadGraphSpec: %v", err)
	}

	// A chat turn recorded by the previous release: events, no spec row.
	// Its run_start digest even matches the library file today.
	const runID = "chat-01J00000000000000000LEGACY"
	ev := &fakeTurnEvents{runID: runID}
	ev.add("", coreag.EventRunStart, map[string]any{"graph_id": "chat_default", "spec_digest": coreag.SpecDigest(lib)})
	ev.fire("history_in", "history_read")
	ev.fire("compact_history", "compact")
	ev.fire("ask_user", "ask")
	ev.add("", coreag.EventRunComplete, map[string]any{"completed_nodes": 3})
	if _, err := log.Append(ev.batch); err != nil {
		t.Fatalf("append legacy run: %v", err)
	}
	if _, found, err := log.LoadRunSpec(runID); err != nil || found {
		t.Fatalf("fixture has a stored spec (found %v err %v) — it no longer models a pre-snapshot run", found, err)
	}

	spec, err := graphview.New(mgr).MaterializeRun(ctx, runID)
	if err != nil {
		t.Fatalf("MaterializeRun(pre-snapshot run): %v — old runs must not break", err)
	}
	if spec.SpecProvenance != coreag.SpecProvenanceLibraryFallback {
		t.Errorf("SpecProvenance = %q, want %q — a reconstruction must say it is one",
			spec.SpecProvenance, coreag.SpecProvenanceLibraryFallback)
	}
	mg, err := coreag.LoadYAML([]byte(spec.YAML))
	if err != nil {
		t.Fatalf("materialized YAML does not parse: %v", err)
	}
	if mg.SpecProvenance != coreag.SpecProvenanceLibraryFallback {
		t.Errorf("YAML spec_provenance = %q — the frontend banner keys on it", mg.SpecProvenance)
	}
	if !strings.Contains(mg.Description, "DEGRADED") {
		t.Errorf("reconstruction's description does not say so:\n%s", mg.Description)
	}
	if err := coreag.Validate(mg); err != nil {
		t.Errorf("reconstruction does not validate: %v", err)
	}
}

// A persisted spec that is not the one run_start says ran is refused,
// not served as exact and not silently swapped for a reconstruction.
// The run has a real node fire so projection would otherwise succeed:
// the ONLY thing standing between this run and a materialized graph is
// the run_start digest check (review F1 — an earlier fixture with no
// fires errored on "no recorded node fires" first, so disabling the
// check changed nothing).
func TestMaterializeRun_PersistedSpecMustMatchRunStartDigest(t *testing.T) {
	t.Parallel()
	log := openSQLEventLog(t)
	mgr, err := graphview.NewManager(graphview.WithEventLog(log))
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	g := materializeRPCGraph()
	const runID = "chat-mismatch"
	if err := log.RecordRunSpec(runID, g); err != nil {
		t.Fatalf("RecordRunSpec: %v", err)
	}
	ev := &fakeTurnEvents{runID: runID}
	ev.add("", coreag.EventRunStart, map[string]any{"graph_id": g.ID, "spec_digest": "sha256:not-this-spec"})
	ev.fire("first", "transform")
	ev.add("", coreag.EventRunComplete, map[string]any{"completed_nodes": 1})
	if _, err := log.Append(ev.batch); err != nil {
		t.Fatalf("append: %v", err)
	}
	_, err = graphview.New(mgr).MaterializeRun(context.Background(), runID)
	if err == nil {
		t.Fatal("MaterializeRun served a stored spec whose digest contradicts run_start")
	}
	if !strings.Contains(err.Error(), "spec_digest") {
		t.Errorf("error %q does not name the spec_digest mismatch", err)
	}
}

// A stored row that is corrupt — JSON that no longer decodes, or content
// that no longer hashes to its recorded digest — must make
// materialization ERROR. It must never fall through to the in-memory
// maps or the library reconstruction (review F2): the run HAS a recorded
// spec, so a library_fallback answer would quietly replace "what ran"
// with "what the file says now". The library graph exists here, so a
// fall-through would succeed — that is what makes the test bite.
func TestMaterializeRun_CorruptStoredSpecErrorsNeverFallsBack(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		corrupt string
	}{
		{"bad_json", `UPDATE agent_graph_run_specs SET spec_json = '{not json' WHERE run_id = ?`},
		{"digest_mismatch", `UPDATE agent_graph_run_specs SET spec_json = replace(spec_json, 'Second as it ran', 'Second, tampered') WHERE run_id = ? AND spec_json LIKE '%Second as it ran%'`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			log, db := openSQLEventLogDB(t)
			mgr, err := graphview.NewManager(graphview.WithDataDir(t.TempDir()), graphview.WithEventLog(log))
			if err != nil {
				t.Fatalf("NewManager: %v", err)
			}
			a := graphview.New(mgr)
			if err := a.SaveGraph(ctx, graphview.GraphSpec{ID: "edited_later", YAML: editedGraphV1}, "user"); err != nil {
				t.Fatalf("SaveGraph: %v", err)
			}
			g, err := mgr.LoadGraphSpec("edited_later")
			if err != nil {
				t.Fatalf("LoadGraphSpec: %v", err)
			}
			const runID = "chat-corrupt"
			if err := log.RecordRunSpec(runID, g); err != nil {
				t.Fatalf("RecordRunSpec: %v", err)
			}
			ev := &fakeTurnEvents{runID: runID}
			ev.add("", coreag.EventRunStart, map[string]any{"graph_id": g.ID})
			ev.fire("first", "transform")
			ev.fire("second", "transform")
			ev.add("", coreag.EventRunComplete, map[string]any{"completed_nodes": 2})
			if _, err := log.Append(ev.batch); err != nil {
				t.Fatalf("append: %v", err)
			}
			res, err := db.Exec(tc.corrupt, runID)
			if err != nil {
				t.Fatalf("corrupt: %v", err)
			}
			if n, _ := res.RowsAffected(); n != 1 {
				t.Fatalf("corruption touched %d rows, want 1 — fixture no longer corrupts the row", n)
			}
			spec, err := a.MaterializeRun(ctx, runID)
			if err == nil {
				t.Errorf("MaterializeRun served a run whose stored spec is corrupt (provenance %q)", spec.SpecProvenance)
			}
			if spec.SpecProvenance == coreag.SpecProvenanceLibraryFallback {
				t.Error("a corrupt stored spec fell through to the library reconstruction")
			}
		})
	}
}

// SpecDigest must ignore presentation metadata (a layout-only edit is
// not a different spec) and must see a semantic change. Moved here from
// the deleted materialize_spec_digest_test.go: the digest is still the
// cross-check runSpecFor holds a persisted spec to.
func TestSpecDigest_StableUnderLayoutSensitiveToSpec(t *testing.T) {
	t.Parallel()
	mgr, err := graphview.NewManager()
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	g, err := mgr.LoadGraphSpec("chat_default")
	if err != nil {
		t.Fatalf("LoadGraphSpec: %v", err)
	}
	base := coreag.SpecDigest(g)
	if !strings.HasPrefix(base, "sha256:") {
		t.Fatalf("SpecDigest = %q, want sha256:<hex>", base)
	}
	if again := coreag.SpecDigest(g); again != base {
		t.Fatalf("SpecDigest not deterministic: %q vs %q", base, again)
	}
	moved := g
	moved.Layout = map[string]coreag.NodeLayout{g.Nodes[0].ID: {X: 10, Y: 20}}
	if coreag.SpecDigest(moved) != base {
		t.Error("a layout-only change altered the spec digest")
	}
	edited := g
	edited.SystemPrompt = g.SystemPrompt + " (edited)"
	if coreag.SpecDigest(edited) == base {
		t.Error("a semantic change did not alter the spec digest")
	}
}
